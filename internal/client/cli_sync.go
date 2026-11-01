package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"presync/internal/diff"
	"presync/internal/transfer"
)

// idleStallTimeout aborts a file fetch that has received no bytes for this
// long. A wedged connection must not hang the sync forever.
const idleStallTimeout = 60 * time.Second

type SyncResult struct {
	FilesSynced int
	Errors      int
}

type syncStartResponse struct {
	Session     string     `json:"session"`
	Delta       diff.Delta `json:"delta"`
	Files       []fileInfo `json:"files"`
	Destination string     `json:"destination"`
}

type fileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	State  string `json:"state,omitempty"`
}

type syncConfirmResponse struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

func SyncProfile(serverAddr, token, profileName, deviceID, destDir string, fresh bool, progress func(path string, done bool, err error)) (SyncResult, error) {
	c := &http.Client{Timeout: 0}

	// The destination may not exist yet (fresh pairing, --dest path)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return SyncResult{}, fmt.Errorf("creating destination directory: %w", err)
	}

	destManifest, err := diff.BuildManifest(destDir)
	if err != nil {
		return SyncResult{}, fmt.Errorf("building destination manifest: %w", err)
	}

	startReq := map[string]interface{}{
		"device_id":            deviceID,
		"profile":              profileName,
		"destination_manifest": destManifest,
		"fresh":                fresh,
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
		// Files already completed in a resumed session need neither a hash nor
		// a transfer — the server verified them before marking them done.
		if f.State == string(transfer.FileDone) {
			result.FilesSynced++
			if progress != nil {
				progress(f.Path, true, nil)
			}
			continue
		}

		// Files with a SHA256 from the server are size+mtime-ambiguous files
		// needing verification against the local copy (fresh sessions).
		if f.SHA256 != "" {
			ok, err := verifyOrConfirm(c, serverAddr, token, sessionPath, f.Path, destDir, f.SHA256)
			if err != nil {
				result.Errors++
				if progress != nil {
					progress(f.Path, false, err)
				}
				continue
			}
			if ok {
				result.FilesSynced++
				if progress != nil {
					progress(f.Path, true, nil)
				}
				continue
			}
		}

		var fetchErr error
		for attempt := 1; attempt <= transfer.MaxRetries; attempt++ {
			fetchErr = fetchAndWriteFile(c, serverAddr, token, sessionPath, f.Path, destDir)
			if fetchErr == nil {
				break
			}
		}
		if fetchErr != nil {
			result.Errors++
			reportFileError(c, serverAddr, token, sessionPath, f.Path)
			if progress != nil {
				progress(f.Path, false, fetchErr)
			}
			continue
		}
		result.FilesSynced++
		if progress != nil {
			progress(f.Path, true, nil)
		}
	}

	// A session with failed files is left resumable: no deletions, no
	// complete. The next run picks up where this one stopped.
	if result.Errors > 0 {
		return result, nil
	}

	// Mirror-mode deletions: applied only after the file phase succeeds, when
	// the full delta is known. The server only populates ToDelete for mirror
	// profiles.
	for _, del := range startResp.Delta.ToDelete {
		if err := os.Remove(filepath.Join(destDir, del.Path)); err != nil && !os.IsNotExist(err) {
			logWarning := fmt.Sprintf("deleting %s: %v", del.Path, err)
			result.Errors++
			if progress != nil {
				progress(del.Path, false, fmt.Errorf("%s", logWarning))
			}
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

// verifyOrConfirm handles files the server marked with a SHA256: either a done
// file from a resumed session, or an ambiguous file whose local copy may
// already match. Returns true when the file is now confirmed done without a
// transfer. A mismatch means the file must be transferred.
func verifyOrConfirm(c *http.Client, serverAddr, token, sessionPath, filePath, destDir, serverHash string) (bool, error) {
	fullDest := filepath.Join(destDir, filePath)

	info, err := os.Stat(fullDest)
	if err != nil || !info.Mode().IsRegular() {
		return false, nil
	}

	localHash, err := transfer.ComputeFileHash(fullDest)
	if err != nil {
		return false, nil
	}
	if localHash != serverHash {
		return false, nil
	}

	confirmBody, _ := json.Marshal(map[string]string{
		"session": sessionPath,
		"path":    filePath,
		"sha256":  localHash,
	})
	confirmReq, err := http.NewRequest("POST", serverAddr+"/sync/confirm", bytes.NewReader(confirmBody))
	if err != nil {
		return false, fmt.Errorf("creating confirm request: %w", err)
	}
	confirmReq.Header.Set("Content-Type", "application/json")
	confirmReq.Header.Set("X-Presync-Token", token)

	confirmResp, err := c.Do(confirmReq)
	if err != nil {
		return false, fmt.Errorf("confirming file: %w", err)
	}
	defer confirmResp.Body.Close()

	if confirmResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(confirmResp.Body)
		return false, fmt.Errorf("confirm failed (%d): %s", confirmResp.StatusCode, string(body))
	}

	var confirmResult syncConfirmResponse
	if err := json.NewDecoder(confirmResp.Body).Decode(&confirmResult); err != nil {
		return false, fmt.Errorf("decoding confirm response: %w", err)
	}
	if !confirmResult.OK {
		return false, nil
	}
	return true, nil
}

func reportFileError(c *http.Client, serverAddr, token, sessionPath, filePath string) {
	body, _ := json.Marshal(map[string]string{
		"session": sessionPath,
		"path":    filePath,
	})
	req, err := http.NewRequest("POST", serverAddr+"/sync/error", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Presync-Token", token)
	resp, err := c.Do(req)
	if err == nil {
		resp.Body.Close()
	}
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

	fetchURL := fmt.Sprintf("%s/sync/file?session=%s&path=%s", serverAddr,
		url.QueryEscape(sessionPath), url.QueryEscape(filePath))
	if offset > 0 {
		fetchURL = fmt.Sprintf("%s&offset=%d", fetchURL, offset)
	}

	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		return fmt.Errorf("creating fetch request: %w", err)
	}
	req.Header.Set("X-Presync-Token", token)

	// Per-file stall watchdog: no bytes for idleStallTimeout aborts the fetch.
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	var lastRead atomic.Int64
	lastRead.Store(time.Now().UnixNano())
	watchStall(ctx, cancel, &lastRead, idleStallTimeout)

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
		existing, err := os.Open(tmpPath)
		if err != nil {
			return fmt.Errorf("opening existing temp: %w", err)
		}
		// Stream the existing prefix through the hasher — never load a large
		// partial file into memory.
		if _, err := io.Copy(h, existing); err != nil {
			existing.Close()
			return fmt.Errorf("hashing existing temp: %w", err)
		}
		existing.Close()
	}

	mw := io.MultiWriter(h, f)
	buf := make([]byte, transfer.ChunkSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := mw.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("writing chunk: %w", writeErr)
			}
			lastRead.Store(time.Now().UnixNano())
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

// watchStall cancels the request context when no bytes have been read for the
// idle timeout. It stops on context cancellation.
func watchStall(ctx context.Context, cancel context.CancelFunc, lastRead *atomic.Int64, idleTimeout time.Duration) {
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				last := time.Unix(0, lastRead.Load())
				if time.Since(last) > idleTimeout {
					cancel()
					return
				}
			}
		}
	}()
}
