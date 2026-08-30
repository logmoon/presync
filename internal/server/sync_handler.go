package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"presync/internal/config"
	"presync/internal/diff"
	"presync/internal/transfer"
)

type syncStartRequest struct {
	DeviceID            string        `json:"device_id"`
	Profile             string        `json:"profile"`
	DestinationManifest diff.Manifest `json:"destination_manifest"`
	Fresh               bool          `json:"fresh"`
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

		if !validSyncIdentifier(req.DeviceID) || !validSyncIdentifier(req.Profile) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id and profile must not contain path separators"})
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

		// Resolve destination from profile's destinations map. Rules:
		//   - deviceID "local" (CLI self-sync): the "local" entry.
		//   - webDeviceID (server-initiated web sync): no override — the caller
		//     already passes the right destination per iteration.
		//   - real paired devices: their registered entry only. An unregistered
		//     device keeps its own locally-chosen destination — the "local"
		//     fallback must never redirect a remote device's files to a
		//     server-machine path.
		var destination string
		switch {
		case req.DeviceID == "local":
			destination = profile.Destinations["local"]
		case req.DeviceID == webDeviceID:
			// no override
		default:
			if profile.Destinations != nil {
				// Device-ID keyed entry (current), then legacy name key.
				if d, ok := profile.Destinations[req.DeviceID]; ok {
					destination = d
				} else if deviceName != "" {
					destination = profile.Destinations[deviceName]
				}
			}
		}

		existing, err := transfer.FindSessionForDevice(req.DeviceID, req.Profile)
		if err != nil {
			logError("finding existing session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check existing session"})
			return
		}

		if req.Fresh && existing != nil {
			if err := transfer.DeleteSession(existing.Path); err != nil {
				logError("deleting session for --fresh: %v", err)
			}
			existing = nil
		}

		sourceManifest, err := diff.BuildManifest(profile.Source)
		if err != nil {
			logError("building source manifest: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build source manifest"})
			return
		}

		delta := diff.ComputeDelta(sourceManifest, req.DestinationManifest, profile.Mode)

		var sess *transfer.Session
		if existing != nil && !existing.IsComplete() {
			if existing.IsStale() {
				transfer.DeleteSession(existing.Path)
			} else {
				sess = existing
			}
		}

		if sess != nil {
			// Resume: the session file list is authoritative for what remains,
			// but the delta is recomputed so the client still receives
			// mirror-mode deletions for files it has since accumulated.
			var files []fileInfo
			for _, fs := range sess.Files {
				files = append(files, fileInfo{
					Path:   fs.Path,
					SHA256: fs.SHA256,
				})
			}

			pending := sess.PendingFiles()
			logInfo("sync resumed: device=%s profile=%s pending=%d delta_adds=%d deletes=%d",
				req.DeviceID, req.Profile, len(pending), len(delta.ToAdd), len(delta.ToDelete))

			writeJSON(w, http.StatusOK, syncStartResponse{
				Session:     sess.Path,
				Delta:       delta,
				Files:       files,
				Destination: destination,
			})
			return
		}

		// Fresh session. The session covers the delta, plus ambiguous files
		// (size+mtime match) whose SHA-256 the server computes so the client
		// can verify its local copy without re-transferring.
		filePaths, files, err := buildSessionFiles(sourceManifest, req.DestinationManifest, profile, delta)
		if err != nil {
			logError("building session files: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare session"})
			return
		}

		sess = transfer.NewSession(req.DeviceID, req.Profile, filePaths)
		if err := sess.Save(); err != nil {
			logError("saving session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save session"})
			return
		}

		logInfo("sync started: device=%s profile=%s files=%d adds=%d updates=%d deletes=%d verify=%d",
			req.DeviceID, req.Profile, len(filePaths), len(delta.ToAdd), len(delta.ToUpdate), len(delta.ToDelete), len(files)-len(delta.ToAdd)-len(delta.ToUpdate))

		writeJSON(w, http.StatusOK, syncStartResponse{
			Session:     sess.Path,
			Delta:       delta,
			Files:       files,
			Destination: destination,
		})
	}
}

// buildSessionFiles returns the list of session file paths and the response
// file info for a fresh session: delta files (adds/updates) plus ambiguous
// files that need hash verification against the client's local copy.
func buildSessionFiles(sourceManifest diff.Manifest, dstManifest diff.Manifest, profile *config.Profile, delta diff.Delta) ([]string, []fileInfo, error) {
	var filePaths []string
	var files []fileInfo
	seen := make(map[string]bool, len(delta.ToAdd)+len(delta.ToUpdate))

	for _, a := range delta.ToAdd {
		filePaths = append(filePaths, a.Path)
		seen[a.Path] = true
	}
	for _, a := range delta.ToUpdate {
		filePaths = append(filePaths, a.Path)
		seen[a.Path] = true
	}

	dstIndex := manifestIndex(dstManifest)

	for _, f := range sourceManifest.Files {
		if seen[f.Path] {
			files = append(files, fileInfo{Path: f.Path, Size: f.Size})
			continue
		}
		dst, ok := dstIndex[f.Path]
		if !ok || dst.Size != f.Size || dst.ModTime != f.ModTime {
			continue
		}
		// Ambiguous: same size+mtime. Hash the source so the client can verify
		// its local copy; only transfer if content differs.
		hash, err := transfer.ComputeFileHash(filepath.Join(profile.Source, f.Path))
		if err != nil {
			logError("hashing ambiguous file %s: %v", f.Path, err)
			continue
		}
		filePaths = append(filePaths, f.Path)
		files = append(files, fileInfo{Path: f.Path, Size: f.Size, SHA256: hash})
	}

	return filePaths, files, nil
}

func manifestIndex(m diff.Manifest) map[string]diff.FileEntry {
	idx := make(map[string]diff.FileEntry, len(m.Files))
	for _, f := range m.Files {
		idx[f.Path] = f
	}
	return idx
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

		sess, err := resolveAndLoadSession(sessionPath)
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
			offset, err = strconv.ParseInt(offsetStr, 10, 64)
			if err != nil || offset < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid offset"})
				return
			}
		}

		profile := findProfile(cfg, sess.Profile)
		if profile == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "profile not found"})
			return
		}

		// Validate the source file BEFORE mutating session state. A failure here
		// must leave the file pending (resumable), not stuck in_progress.
		fullPath := filepath.Join(profile.Source, filePath)

		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("file %s not found on server", filePath)})
			} else {
				logError("stating file %s: %v", filePath, err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read file metadata"})
			}
			return
		}
		if !info.Mode().IsRegular() {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("file %s is not a regular file", filePath)})
			return
		}

		if offset > info.Size() {
			writeJSON(w, http.StatusRequestedRangeNotSatisfiable, map[string]string{"error": fmt.Sprintf("offset %d exceeds file size %d", offset, info.Size())})
			return
		}

		hash, err := transfer.ComputeFileHash(fullPath)
		if err != nil {
			logError("hashing file %s: %v", filePath, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash file"})
			return
		}

		// File is verified readable and hashable — only now is the transfer
		// state marked in_progress.
		sess.MarkInProgress(filePath)
		if offset > 0 {
			sess.UpdateOffset(filePath, offset)
		}
		if err := sess.Save(); err != nil {
			logError("saving session state: %v", err)
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Presync-Offset", fmt.Sprintf("%d", offset))
		w.Header().Set("X-Presync-SHA256", hash)

		if err := transfer.StreamFile(w, fullPath, offset); err != nil {
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

		sess, err := resolveAndLoadSession(req.Session)
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

		sess, err := resolveAndLoadSession(sessionPath)
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

		sess, err := resolveAndLoadSession(req.Session)
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

func handleSyncError() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		var req struct {
			Session string `json:"session"`
			Path    string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Session == "" || req.Path == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session and path are required"})
			return
		}

		sess, err := resolveAndLoadSession(req.Session)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}

		if sess.GetFile(req.Path) == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not in session"})
			return
		}

		sess.MarkError(req.Path)
		if err := sess.Save(); err != nil {
			logError("saving session: %v", err)
		}

		logInfo("file failed after retries: %s (attempts=%d)", req.Path, sess.GetFile(req.Path).Attempts)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

// resolveAndLoadSession resolves a client-supplied session identifier to a
// path inside SessionsDir and loads it. Client input never reaches the
// filesystem verbatim.
func resolveAndLoadSession(raw string) (*transfer.Session, error) {
	path, err := transfer.ResolveSession(raw)
	if err != nil {
		return nil, err
	}
	return transfer.LoadSession(path)
}

// validSyncIdentifier rejects device/profile names that would escape the
// sessions directory when embedded in session filenames.
func validSyncIdentifier(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\")
}
