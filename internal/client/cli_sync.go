package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"presync/internal/diff"
	"presync/internal/transfer"
)

type SyncResult struct {
	FilesSynced int
	Errors      int
}

func SyncProfile(serverAddr, token, profileName, deviceID, destDir string, fresh bool, progress func(path string, done bool, err error)) (SyncResult, error) {
	c := &http.Client{Timeout: 0}

	if fresh {
		existing, err := transfer.FindSessionForDevice(deviceID, profileName)
		if err == nil && existing != nil {
			transfer.DeleteSession(existing.Path)
		}
	}

	destManifest, err := diff.BuildManifest(destDir)
	if err != nil {
		return SyncResult{}, fmt.Errorf("building destination manifest: %w", err)
	}

	startReq := map[string]interface{}{
		"device_id":            deviceID,
		"profile":              profileName,
		"destination_manifest": destManifest,
	}
	startBody, _ := json.Marshal(startReq)

	req, err := http.NewRequest("POST", serverAddr+"/sync", bytes.NewReader(startBody))
	if err != nil {
		return SyncResult{}, fmt.Errorf("creating sync request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Presync-Token", token)

	resp, err := c.Do(req)
	if err != nil {
		return SyncResult{}, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return SyncResult{}, fmt.Errorf("sync start failed (%d): %s", resp.StatusCode, string(body))
	}

	var startResp syncStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		return SyncResult{}, fmt.Errorf("decoding sync response: %w", err)
	}

	// Use destination from server response if available
	if startResp.Destination != "" {
		destDir = startResp.Destination
	}

	sessionPath := startResp.Session
	result := SyncResult{}

	for _, f := range startResp.Files {
		err := fetchAndWriteFile(c, serverAddr, token, sessionPath, f.Path, destDir)
		if err != nil {
			result.Errors++
			if progress != nil {
				progress(f.Path, false, err)
			}
			continue
		}
		result.FilesSynced++
		if progress != nil {
			progress(f.Path, true, nil)
		}
	}

	completeBody, _ := json.Marshal(map[string]string{"session": sessionPath})
	completeReq, err := http.NewRequest("POST", serverAddr+"/sync/complete", bytes.NewReader(completeBody))
	if err != nil {
		return result, fmt.Errorf("creating complete request: %w", err)
	}
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.Header.Set("X-Presync-Token", token)

	completeResp, err := c.Do(completeReq)
	if err != nil {
		return result, fmt.Errorf("completing sync: %w", err)
	}
	defer completeResp.Body.Close()

	if completeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(completeResp.Body)
		return result, fmt.Errorf("sync complete failed (%d): %s", completeResp.StatusCode, string(body))
	}

	return result, nil
}

func fetchAndWriteFile(c *http.Client, serverAddr, token, sessionPath, filePath, destDir string) error {
	fullDest := filepath.Join(destDir, filePath)
	tmpPath := fullDest + ".presync-tmp"

	if err := os.MkdirAll(filepath.Dir(fullDest), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	var offset int64
	if info, err := os.Stat(tmpPath); err == nil {
		offset = info.Size()
	}

	fetchURL := fmt.Sprintf("%s/sync/file?session=%s&path=%s", serverAddr, sessionPath, filePath)
	if offset > 0 {
		fetchURL = fmt.Sprintf("%s&offset=%d", fetchURL, offset)
	}

	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		return fmt.Errorf("creating fetch request: %w", err)
	}
	req.Header.Set("X-Presync-Token", token)

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("fetching file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fetch failed (%d): %s", resp.StatusCode, string(body))
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening temp file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if offset > 0 {
		existing, err := os.ReadFile(tmpPath)
		if err != nil {
			return fmt.Errorf("reading existing temp: %w", err)
		}
		h.Write(existing)
	}

	mw := io.MultiWriter(h, f)
	buf := make([]byte, transfer.ChunkSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := mw.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("writing chunk: %w", writeErr)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("reading response: %w", readErr)
		}
	}

	hash := hex.EncodeToString(h.Sum(nil))

	confirmBody, _ := json.Marshal(map[string]string{
		"session": sessionPath,
		"path":    filePath,
		"sha256":  hash,
	})
	confirmReq, err := http.NewRequest("POST", serverAddr+"/sync/confirm", bytes.NewReader(confirmBody))
	if err != nil {
		return fmt.Errorf("creating confirm request: %w", err)
	}
	confirmReq.Header.Set("Content-Type", "application/json")
	confirmReq.Header.Set("X-Presync-Token", token)

	confirmResp, err := c.Do(confirmReq)
	if err != nil {
		return fmt.Errorf("confirming file: %w", err)
	}
	defer confirmResp.Body.Close()

	var confirmResult syncConfirmResponse
	if err := json.NewDecoder(confirmResp.Body).Decode(&confirmResult); err != nil {
		return fmt.Errorf("decoding confirm response: %w", err)
	}

	if confirmResult.OK {
		if err := os.Rename(tmpPath, fullDest); err != nil {
			return fmt.Errorf("renaming temp file: %w", err)
		}
	} else {
		os.Remove(tmpPath)
		return fmt.Errorf("checksum mismatch: %s", confirmResult.Message)
	}

	return nil
}
