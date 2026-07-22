package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"presync/internal/config"
	"presync/internal/diff"
	"presync/internal/transfer"
)

type syncStartRequest struct {
	DeviceID            string        `json:"device_id"`
	Profile             string        `json:"profile"`
	DestinationManifest diff.Manifest `json:"destination_manifest"`
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
}

type syncConfirmRequest struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

type syncConfirmResponse struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

type syncStatusResponse struct {
	Session  string          `json:"session"`
	Total    int             `json:"total"`
	Done     int             `json:"done"`
	Errors   int             `json:"errors"`
	Current  string          `json:"current,omitempty"`
	Complete bool            `json:"complete"`
	Files    []fileStateInfo `json:"files"`
}

type fileStateInfo struct {
	Path     string             `json:"path"`
	State    transfer.FileState `json:"state"`
	Offset   int64              `json:"offset"`
	Attempts int                `json:"attempts"`
}

type syncCompleteResponse struct {
	OK bool `json:"ok"`
}

func handleSyncStart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		var req syncStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.DeviceID == "" || req.Profile == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id and profile are required"})
			return
		}

		var profile *config.Profile
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == req.Profile {
				profile = &cfg.Profiles[i]
				break
			}
		}
		if profile == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("profile %q not found", req.Profile)})
			return
		}

		// Look up device name from devices.json to resolve destination
		var deviceName string
		if req.DeviceID != "local" && req.DeviceID != webDeviceID {
			df, err := config.LoadDevices()
			if err == nil {
				for _, d := range df.Devices {
					if d.ID == req.DeviceID {
						deviceName = d.Name
						break
					}
				}
			}
		}

		// Resolve destination from profile's destinations map
		var destination string
		if deviceName != "" && profile.Destinations != nil {
			destination = profile.Destinations[deviceName]
		}
		if destination == "" {
			destination = profile.Destinations["local"]
		}

		existing, err := transfer.FindSessionForDevice(req.DeviceID, req.Profile)
		if err != nil {
			logError("finding existing session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check existing session"})
			return
		}

		var sess *transfer.Session
		if existing != nil && !existing.IsComplete() {
			if existing.IsStale() {
				transfer.DeleteSession(existing.Path)
			} else {
				sess = existing
			}
		}
		if sess == nil {
			sourceManifest, err := diff.BuildManifest(profile.Source)
			if err != nil {
				logError("building source manifest: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build source manifest"})
				return
			}

			delta := diff.ComputeDelta(sourceManifest, req.DestinationManifest, profile.Mode)

			var filePaths []string
			for _, f := range sourceManifest.Files {
				for _, a := range delta.ToAdd {
					if f.Path == a.Path {
						filePaths = append(filePaths, f.Path)
						break
					}
				}
				for _, a := range delta.ToUpdate {
					if f.Path == a.Path {
						filePaths = append(filePaths, f.Path)
						break
					}
				}
			}

			sess = transfer.NewSession(req.DeviceID, req.Profile, filePaths)
			if err := sess.Save(); err != nil {
				logError("saving session: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save session"})
				return
			}

			var files []fileInfo
			for _, fp := range filePaths {
				for _, f := range sourceManifest.Files {
					if f.Path == fp {
						files = append(files, fileInfo{
							Path:   f.Path,
							Size:   f.Size,
							SHA256: f.SHA256,
						})
						break
					}
				}
			}

			logInfo("sync started: device=%s profile=%s files=%d adds=%d updates=%d deletes=%d",
				req.DeviceID, req.Profile, len(filePaths), len(delta.ToAdd), len(delta.ToUpdate), len(delta.ToDelete))

			writeJSON(w, http.StatusOK, syncStartResponse{
				Session:     sess.Path,
				Delta:       delta,
				Files:       files,
				Destination: destination,
			})
			return
		}

		var files []fileInfo
		for _, fs := range sess.Files {
			files = append(files, fileInfo{
				Path:   fs.Path,
				SHA256: fs.SHA256,
			})
		}

		pending := sess.PendingFiles()
		logInfo("sync resumed: device=%s profile=%s pending=%d", req.DeviceID, req.Profile, len(pending))

		writeJSON(w, http.StatusOK, syncStartResponse{
			Session: sess.Path,
			Delta: diff.Delta{
				ToAdd: make([]diff.FileAction, 0),
			},
			Files:       files,
			Destination: destination,
		})
	}
}

func handleSyncFile() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		sessionPath := r.URL.Query().Get("session")
		filePath := r.URL.Query().Get("path")
		offsetStr := r.URL.Query().Get("offset")

		if sessionPath == "" || filePath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session and path are required"})
			return
		}

		sess, err := transfer.LoadSession(sessionPath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}

		fs := sess.GetFile(filePath)
		if fs == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not in session"})
			return
		}

		if fs.State == transfer.FileDone {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file already completed"})
			return
		}

		var offset int64
		if offsetStr != "" {
			if _, err := fmt.Sscanf(offsetStr, "%d", &offset); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid offset"})
				return
			}
		}

		sess.MarkInProgress(filePath)
		if offset > 0 {
			sess.UpdateOffset(filePath, offset)
		}
		if err := sess.Save(); err != nil {
			logError("saving session state: %v", err)
		}

		profile := findProfile(cfg, sess.Profile)
		if profile == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "profile not found"})
			return
		}

		fullPath := filepath.Join(profile.Source, filePath)

		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("file %s not found on server", filePath)})
			return
		}

		hash, err := transfer.ComputeFileHash(fullPath)
		if err != nil {
			logError("hashing file %s: %v", filePath, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash file"})
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Presync-Offset", fmt.Sprintf("%d", offset))
		w.Header().Set("X-Presync-SHA256", hash)

		if _, err := transfer.SendFile(w, fullPath, offset); err != nil {
			logError("sending file %s: %v", filePath, err)
			return
		}

		logDebug("sent file: %s offset=%d hash=%s", filePath, offset, hash)
	}
}

func handleSyncConfirm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		var req syncConfirmRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Session == "" || req.Path == "" || req.SHA256 == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session, path, and sha256 are required"})
			return
		}

		sess, err := transfer.LoadSession(req.Session)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}

		fs := sess.GetFile(req.Path)
		if fs == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not in session"})
			return
		}

		profile := findProfile(cfg, sess.Profile)
		if profile == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "profile not found"})
			return
		}

		serverHash, err := transfer.ComputeFileHash(filepath.Join(profile.Source, req.Path))
		if err != nil {
			logError("computing hash for %s: %v", req.Path, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to compute hash"})
			return
		}

		if req.SHA256 == serverHash {
			sess.MarkDone(req.Path, serverHash)
			if err := sess.Save(); err != nil {
				logError("saving session: %v", err)
			}

			logDebug("file confirmed: %s", req.Path)
			writeJSON(w, http.StatusOK, syncConfirmResponse{
				OK:      true,
				Action:  "rename",
				Message: fmt.Sprintf("rename %s.presync-tmp to %s", req.Path, req.Path),
			})
		} else {
			sess.MarkError(req.Path)
			if err := sess.Save(); err != nil {
				logError("saving session: %v", err)
			}

			fs = sess.GetFile(req.Path)
			logInfo("checksum mismatch: %s attempts=%d", req.Path, fs.Attempts)
			writeJSON(w, http.StatusOK, syncConfirmResponse{
				OK:      false,
				Action:  "retry",
				Message: fmt.Sprintf("hash mismatch, attempt %d of %d", fs.Attempts, transfer.MaxRetries),
			})
		}
	}
}

func handleSyncStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		sessionPath := r.URL.Query().Get("session")
		if sessionPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session is required"})
			return
		}

		sess, err := transfer.LoadSession(sessionPath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}

		var done, errors int
		var current string
		var files []fileStateInfo

		for _, f := range sess.Files {
			files = append(files, fileStateInfo{
				Path:     f.Path,
				State:    f.State,
				Offset:   f.Offset,
				Attempts: f.Attempts,
			})
			switch f.State {
			case transfer.FileDone:
				done++
			case transfer.FileError:
				errors++
			case transfer.FileInProgress:
				current = f.Path
			}
		}

		writeJSON(w, http.StatusOK, syncStatusResponse{
			Session:  sess.Path,
			Total:    len(sess.Files),
			Done:     done,
			Errors:   errors,
			Current:  current,
			Complete: sess.IsComplete(),
			Files:    files,
		})
	}
}

func handleSyncComplete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		var req struct {
			Session string `json:"session"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Session == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session is required"})
			return
		}

		sess, err := transfer.LoadSession(req.Session)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}

		if !sess.IsComplete() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session not all files completed"})
			return
		}

		if err := transfer.DeleteSession(req.Session); err != nil {
			logError("deleting session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete session"})
			return
		}

		logInfo("sync complete: session=%s", req.Session)
		writeJSON(w, http.StatusOK, syncCompleteResponse{OK: true})
	}
}

func findProfile(cfg config.Config, name string) *config.Profile {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return &cfg.Profiles[i]
		}
	}
	return nil
}
