package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"presync/internal/config"
	"presync/internal/diff"
	"presync/internal/transfer"
)

// maxPushChunkBytes bounds a single upload request. The client sends at most
// transfer.ChunkSize bytes; the extra headroom absorbs framing overhead.
const maxPushChunkBytes = transfer.ChunkSize + 64*1024

type pushStartRequest struct {
	DeviceID       string         `json:"device_id"`
	Profile        string         `json:"profile"`
	SourceManifest *diff.Manifest `json:"source_manifest"`
	Fresh          bool           `json:"fresh"`
}

type pushFileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	State  string `json:"state"`
}

type pushStartResponse struct {
	Session     string         `json:"session"`
	Files       []pushFileInfo `json:"files"`
	Destination string         `json:"destination"`
}

type pushConfirmRequest struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

func handlePushStart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		var req pushStartRequest
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
		if req.SourceManifest == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_manifest is required"})
			return
		}

		profile := findProfile(cfg, req.Profile)
		if profile == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("profile %q not found", req.Profile)})
			return
		}
		if profile.Direction != config.DirectionPush {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("profile %q is not a push profile", req.Profile)})
			return
		}

		if err := os.MkdirAll(profile.Destination, 0o755); err != nil {
			logError("creating push destination %s: %v", profile.Destination, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare destination folder"})
			return
		}

		sourceManifest := *req.SourceManifest
		destManifest, err := diff.BuildManifest(profile.Destination)
		if err != nil {
			logError("building push destination manifest: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read destination folder"})
			return
		}

		// A lost SAF grant or an unmounted folder shows up as an empty
		// manifest. Mirroring that would delete the entire server-side copy,
		// so refuse it explicitly instead of trusting the empty source.
		if profile.Mode == "mirror" && len(sourceManifest.Files) == 0 && len(destManifest.Files) > 0 {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "refusing to mirror an empty source manifest; check the device folder",
			})
			return
		}

		// Deltas compare the client's source against the last upload baseline.
		// Server-side destination mtimes are receive times and must not drive
		// re-uploads; fall back to the destination comparison on first sync.
		baseline, baseErr := transfer.LoadState(req.DeviceID, req.Profile)
		if baseErr != nil {
			logError("loading push sync state: %v", baseErr)
		}
		var delta diff.Delta
		if baseline != nil {
			delta = diff.ComputeDeltaWithBase(sourceManifest, destManifest, baseline.Manifest(), profile.Mode)
		} else {
			delta = diff.ComputeDelta(sourceManifest, destManifest, profile.Mode)
		}

		uploadPaths := make([]string, 0, len(delta.ToAdd)+len(delta.ToUpdate))
		for _, a := range delta.ToAdd {
			uploadPaths = append(uploadPaths, a.Path)
		}
		for _, a := range delta.ToUpdate {
			uploadPaths = append(uploadPaths, a.Path)
		}
		deletePaths := make([]string, 0, len(delta.ToDelete))
		for _, d := range delta.ToDelete {
			deletePaths = append(deletePaths, d.Path)
		}

		// Validate every path before creating any state. One bad path makes
		// the whole request invalid.
		for _, p := range uploadPaths {
			if _, err := resolvePushPath(profile.Destination, p); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		for _, p := range deletePaths {
			if _, err := resolvePushPath(profile.Destination, p); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}

		existing, err := transfer.FindSessionForDeviceDirection(req.DeviceID, req.Profile, config.DirectionPush)
		if err != nil {
			logError("finding push session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check existing session"})
			return
		}

		if existing != nil && (req.Fresh || existing.IsStale()) {
			deletePushSession(existing, profile)
			existing = nil
		}

		if existing != nil {
			existing.SetSourceFiles(transfer.StateFilesFromManifest(sourceManifest))

			// Reconcile against the source, not destination mtimes: a done
			// upload stays done when the client's file is unchanged and the
			// server still has it at the right size.
			dstIndex := manifestIndex(destManifest)
			deltaNeeds := make(map[string]bool, len(delta.ToAdd)+len(delta.ToUpdate))
			for _, a := range delta.ToAdd {
				deltaNeeds[a.Path] = true
			}
			for _, a := range delta.ToUpdate {
				deltaNeeds[a.Path] = true
			}
			for _, sf := range sourceManifest.Files {
				fs := existing.GetFile(sf.Path)
				if fs == nil {
					// Only new work enters the session; unchanged files stay
					// out of it instead of being re-uploaded on every resume.
					if deltaNeeds[sf.Path] {
						existing.UpsertFile(sf.Path, sf.Size, sf.ModTime)
					}
					continue
				}
				sourceChanged := fs.ModTime != 0 && (fs.Size != sf.Size || fs.ModTime != sf.ModTime)
				dst, inDst := dstIndex[sf.Path]
				missingAtDestination := !inDst || dst.Size != sf.Size
				if sourceChanged || missingAtDestination {
					existing.UpsertFile(sf.Path, sf.Size, sf.ModTime)
					continue
				}
				if fs.ModTime == 0 {
					// Legacy session without source metadata. The server
					// cannot prove the phone's current content matches what
					// was uploaded, so a done entry is re-uploaded once rather
					// than risk certifying a stale copy.
					if fs.State == transfer.FileDone {
						existing.UpsertFile(sf.Path, sf.Size, sf.ModTime)
						continue
					}
					existing.SetFileMeta(sf.Path, sf.Size, sf.ModTime)
				}
			}
			if profile.Mode == "mirror" {
				existing.SetPlannedDeletes(deletePaths)
			} else {
				existing.SetPlannedDeletes(nil)
			}

			files := resumePushSession(existing, profile, sourceManifest)
			if err := existing.Save(); err != nil {
				logError("saving resumed push session: %v", err)
			}
			pending := 0
			for _, f := range files {
				if f.State != string(transfer.FileDone) {
					pending++
				}
			}
			logInfo("push resumed: device=%s profile=%s pending=%d", req.DeviceID, req.Profile, pending)
			writeJSON(w, http.StatusOK, pushStartResponse{
				Session:     existing.Path,
				Files:       files,
				Destination: profile.Destination,
			})
			return
		}

		sess := transfer.NewSessionWithDirection(req.DeviceID, req.Profile, config.DirectionPush, uploadPaths)
		sess.SetSourceFiles(transfer.StateFilesFromManifest(sourceManifest))
		srcIndex := manifestIndex(sourceManifest)
		for _, p := range uploadPaths {
			if f, ok := srcIndex[p]; ok {
				sess.SetFileMeta(p, f.Size, f.ModTime)
			}
			// A new session starts from zero even if an orphaned temp file
			// survived an older, pruned session.
			full, _ := resolvePushPath(profile.Destination, p)
			os.Remove(full + ".presync-tmp")
		}
		if profile.Mode == "mirror" {
			sess.SetPlannedDeletes(deletePaths)
		}

		if err := sess.Save(); err != nil {
			logError("saving push session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save session"})
			return
		}

		files := make([]pushFileInfo, 0, len(uploadPaths))
		for _, p := range uploadPaths {
			size := int64(0)
			if f, ok := srcIndex[p]; ok {
				size = f.Size
			}
			files = append(files, pushFileInfo{Path: p, Size: size, State: string(transfer.FilePending)})
		}

		logInfo("push started: device=%s profile=%s files=%d deletes=%d", req.DeviceID, req.Profile, len(files), len(deletePaths))
		writeJSON(w, http.StatusOK, pushStartResponse{
			Session:     sess.Path,
			Files:       files,
			Destination: profile.Destination,
		})
	}
}

// resumePushSession returns every session file with its state and server-owned
// offset. Files removed from the source since the session started are dropped;
// a resumed upload must not wait forever on a file that no longer exists on the
// device. Done files are included so the UI can show true session progress.
func resumePushSession(sess *transfer.Session, profile *config.Profile, source diff.Manifest) []pushFileInfo {
	srcIndex := manifestIndex(source)
	files := make([]pushFileInfo, 0, len(sess.Files))
	var dropped []string

	for _, fs := range sess.Files {
		srcFile, ok := srcIndex[fs.Path]
		if !ok {
			dropped = append(dropped, fs.Path)
			continue
		}

		sess.SetFileMeta(fs.Path, srcFile.Size, srcFile.ModTime)

		offset := int64(0)
		if fs.State != transfer.FileDone {
			if full, err := resolvePushPath(profile.Destination, fs.Path); err == nil {
				offset = pushTempSize(full)
				if offset > srcFile.Size {
					os.Remove(full + ".presync-tmp")
					offset = 0
				}
			}
			sess.UpdateOffset(fs.Path, offset)
		}

		files = append(files, pushFileInfo{
			Path:   fs.Path,
			Size:   srcFile.Size,
			Offset: offset,
			State:  string(fs.State),
		})
	}

	// Remove after the range: RemoveFile rewrites the slice.
	for _, path := range dropped {
		sess.RemoveFile(path)
		if full, err := resolvePushPath(profile.Destination, path); err == nil {
			os.Remove(full + ".presync-tmp")
		}
	}

	return files
}

func deletePushSession(sess *transfer.Session, profile *config.Profile) {
	for _, fs := range sess.Files {
		if full, err := resolvePushPath(profile.Destination, fs.Path); err == nil {
			os.Remove(full + ".presync-tmp")
		}
	}
	if err := transfer.DeleteSession(sess.Path); err != nil {
		logError("deleting push session: %v", err)
	}
}

func handlePushFile() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		sessionPath := r.URL.Query().Get("session")
		filePath := r.URL.Query().Get("path")
		offsetStr := r.URL.Query().Get("offset")

		if sessionPath == "" || filePath == "" || offsetStr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session, path, and offset are required"})
			return
		}

		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil || offset < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid offset"})
			return
		}

		sess, err := resolveAndLoadSession(sessionPath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		if sess.Direction != config.DirectionPush {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a push session"})
			return
		}

		fs := sess.GetFile(filePath)
		if fs == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not in session"})
			return
		}
		if fs.State == transfer.FileDone {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file already completed"})
			return
		}

		profile := findProfile(cfg, sess.Profile)
		if profile == nil || profile.Direction != config.DirectionPush {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "push profile not found"})
			return
		}

		fullPath, err := resolvePushPath(profile.Destination, filePath)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		tempPath := fullPath + ".presync-tmp"

		current := pushTempSize(fullPath)
		if offset != current {
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":  "offset does not match server-side temp size",
				"offset": current,
			})
			return
		}
		if fs.Size > 0 && offset == fs.Size {
			// Nothing left to send; the client should confirm next.
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "offset": offset})
			return
		}
		if fs.Size > 0 && offset > fs.Size {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset exceeds expected file size"})
			return
		}

		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			logError("creating push parent dir: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare destination"})
			return
		}
		if err := ensureRealWithinRoot(profile.Destination, fullPath); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if isSymlink(tempPath) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refusing symlinked temp file"})
			return
		}

		limit := int64(maxPushChunkBytes)
		if fs.Size > 0 {
			remaining := fs.Size - offset
			if remaining+1 < limit {
				limit = remaining + 1
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)

		f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			logError("opening push temp: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to open temp file"})
			return
		}
		written, copyErr := io.Copy(f, r.Body)
		closeErr := f.Close()
		if copyErr != nil {
			logError("receiving chunk for %s: %v", filePath, copyErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write chunk"})
			return
		}
		if closeErr != nil {
			logError("closing push temp for %s: %v", filePath, closeErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize chunk"})
			return
		}

		newOffset := offset + written
		if fs.Size > 0 && newOffset > fs.Size {
			os.Remove(tempPath)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload exceeds expected file size"})
			return
		}

		sess.MarkInProgress(filePath)
		sess.UpdateOffset(filePath, newOffset)
		if err := sess.Save(); err != nil {
			logError("saving push session: %v", err)
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "offset": newOffset})
	}
}

func handlePushConfirm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		var req pushConfirmRequest
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
		if sess.Direction != config.DirectionPush {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a push session"})
			return
		}

		fs := sess.GetFile(req.Path)
		if fs == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not in session"})
			return
		}
		if fs.State == transfer.FileDone {
			writeJSON(w, http.StatusOK, syncConfirmResponse{OK: true, Action: "rename", Message: "already done"})
			return
		}

		profile := findProfile(cfg, sess.Profile)
		if profile == nil || profile.Direction != config.DirectionPush {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "push profile not found"})
			return
		}

		fullPath, err := resolvePushPath(profile.Destination, req.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		tempPath := fullPath + ".presync-tmp"

		if isSymlink(tempPath) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refusing symlinked temp file"})
			return
		}
		if _, err := os.Stat(tempPath); err != nil {
			if fs.Size != 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no uploaded data to confirm"})
				return
			}
			// An empty file has no chunks: materialize the temp so the normal
			// hash-and-rename path can verify and finalize it.
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare destination"})
				return
			}
			if err := ensureRealWithinRoot(profile.Destination, fullPath); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := os.WriteFile(tempPath, nil, 0o644); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create empty temp file"})
				return
			}
		}

		serverHash, err := transfer.ComputeFileHash(tempPath)
		if err != nil {
			logError("hashing push temp %s: %v", req.Path, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash uploaded file"})
			return
		}

		if serverHash != req.SHA256 {
			os.Remove(tempPath)
			sess.UpdateOffset(req.Path, 0)
			sess.MarkError(req.Path)
			if err := sess.Save(); err != nil {
				logError("saving push session: %v", err)
			}
			updated := sess.GetFile(req.Path)
			logInfo("push checksum mismatch: %s attempts=%d", req.Path, updated.Attempts)
			writeJSON(w, http.StatusOK, syncConfirmResponse{
				OK:      false,
				Action:  "retry",
				Message: fmt.Sprintf("hash mismatch, attempt %d of %d", updated.Attempts, transfer.MaxRetries),
			})
			return
		}

		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			logError("creating push destination dir: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare destination"})
			return
		}
		if err := ensureRealWithinRoot(profile.Destination, fullPath); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := replaceFile(tempPath, fullPath); err != nil {
			logError("renaming push temp %s: %v", req.Path, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize file"})
			return
		}

		sess.MarkDone(req.Path, serverHash)
		sess.UpdateOffset(req.Path, fs.Size)
		if err := sess.Save(); err != nil {
			logError("saving push session: %v", err)
		}

		logDebug("push file confirmed: %s", req.Path)
		writeJSON(w, http.StatusOK, syncConfirmResponse{
			OK:      true,
			Action:  "rename",
			Message: fmt.Sprintf("renamed %s.presync-tmp to %s", req.Path, req.Path),
		})
	}
}

func handlePushStatus() http.HandlerFunc {
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
		if sess.Direction != config.DirectionPush {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a push session"})
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

func handlePushComplete() http.HandlerFunc {
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
		if sess.Direction != config.DirectionPush {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a push session"})
			return
		}
		if !sess.IsComplete() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session not all files completed"})
			return
		}

		cfg := getConfig()
		profile := findProfile(cfg, sess.Profile)
		if profile == nil || profile.Direction != config.DirectionPush {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "push profile not found"})
			return
		}

		// Mirror deletions are applied only after every upload has been
		// verified, so an interrupted push never removes server files.
		if profile.Mode == "mirror" {
			for _, relPath := range sess.PlannedDeletes() {
				fullPath, err := resolvePushPath(profile.Destination, relPath)
				if err != nil {
					logError("refusing unsafe push delete %q: %v", relPath, err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid deletion path"})
					return
				}
				if _, statErr := os.Lstat(fullPath); statErr == nil {
					if err := ensureRealWithinRoot(profile.Destination, fullPath); err != nil {
						logError("refusing unsafe push delete %q: %v", relPath, err)
						writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid deletion path"})
						return
					}
					if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
						logError("deleting mirrored file %s: %v", relPath, err)
						writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to apply mirror deletion"})
						return
					}
				}
				os.Remove(fullPath + ".presync-tmp")
			}
		}

		// Persist what the server now holds for this device before dropping
		// the session. Future deltas compare the client's source against this
		// baseline instead of server-side mtimes (which are receive times).
		prev, err := transfer.LoadState(sess.DeviceID, sess.Profile)
		if err != nil {
			logError("loading previous push sync state: %v", err)
		}
		var sourcePaths map[string]bool
		if sources := sess.SourceFileSnapshot(); sources != nil {
			// An empty slice is a known-empty source, not an unknown one.
			sourcePaths = make(map[string]bool, len(sources))
			for _, f := range sources {
				sourcePaths[f.Path] = true
			}
		}
		state := transfer.BuildCompletionState(
			sess.DeviceID,
			sess.Profile,
			config.DirectionPush,
			prev,
			sess.Files,
			sourcePaths,
		)
		if err := transfer.SaveState(state); err != nil {
			logError("saving push sync state: %v", err)
		}

		// Delete the resolved path, not the client-supplied identifier: a
		// client may send only the basename.
		if err := transfer.DeleteSession(sess.Path); err != nil {
			logError("deleting push session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete session"})
			return
		}

		logInfo("push complete: session=%s", req.Session)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func handlePushError() http.HandlerFunc {
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
		if sess.Direction != config.DirectionPush {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a push session"})
			return
		}
		if sess.GetFile(req.Path) == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not in session"})
			return
		}

		sess.MarkError(req.Path)
		if err := sess.Save(); err != nil {
			logError("saving push session: %v", err)
		}
		logInfo("push file failed: %s (attempts=%d)", req.Path, sess.GetFile(req.Path).Attempts)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// resolvePushPath turns a client-supplied relative path into a path strictly
// inside the profile's receive folder. Absolute paths, traversal, and
// transfer-temp filenames are rejected.
func resolvePushPath(root, rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("invalid file path")
	}
	if filepath.IsAbs(rel) || strings.ContainsRune(rel, '\\') {
		return "", fmt.Errorf("file path %q must be relative", rel)
	}

	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path %q escapes the destination", rel)
	}
	if strings.HasSuffix(clean, ".presync-tmp") {
		return "", fmt.Errorf("file path %q uses a reserved suffix", rel)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving destination root: %w", err)
	}
	fullAbs, err := filepath.Abs(filepath.Join(rootAbs, clean))
	if err != nil {
		return "", fmt.Errorf("resolving destination path: %w", err)
	}
	inside, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path %q escapes the destination", rel)
	}

	return fullAbs, nil
}

// ensureRealWithinRoot rejects destinations whose parent resolves outside the
// receive folder through a symlink. Lexical resolvePushPath alone cannot see
// that, and a symlinked directory would redirect writes elsewhere.
func ensureRealWithinRoot(root, fullPath string) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolving destination root: %w", err)
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(fullPath))
	if err != nil {
		return fmt.Errorf("resolving destination path: %w", err)
	}
	inside, err := filepath.Rel(rootReal, parentReal)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return fmt.Errorf("destination path escapes the receive folder")
	}
	return nil
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func pushTempSize(fullPath string) int64 {
	info, err := os.Stat(fullPath + ".presync-tmp")
	if err != nil {
		return 0
	}
	return info.Size()
}

// replaceFile is the only place a real destination name appears. Go's rename
// replaces an existing destination atomically on all supported platforms; if
// it fails, the existing file is left untouched.
func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", src, dst, err)
	}
	return nil
}
