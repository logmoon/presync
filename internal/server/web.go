package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"presync/internal/client"
	"presync/internal/config"
	"presync/internal/diff"
	"presync/internal/transfer"
)

const (
	webDeviceID      = "web-ui"
	maxSessionReturn = 20
)

var webSyncActive sync.Map

func webDestContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func handleWebToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		token, err := config.GetLocalToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get local token"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

func handleWebDeviceID() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		token := r.Header.Get("X-Presync-Token")
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
			return
		}

		dev, ok := config.ValidateToken(token)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"device_id": dev.ID, "device_name": dev.Name})
	}
}

func handleProfilesDetail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		type profileDetail struct {
			Name         string            `json:"name"`
			Source       string            `json:"source"`
			Destinations map[string]string `json:"destinations"`
			Mode         string            `json:"mode"`
		}

		profiles := make([]profileDetail, len(cfg.Profiles))
		for i, p := range cfg.Profiles {
			profiles[i] = profileDetail{
				Name:         p.Name,
				Source:       p.Source,
				Destinations: p.Destinations,
				Mode:         p.Mode,
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"profiles": profiles,
		})
	}
}

func handleWebSync() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		var req struct {
			Profile      string   `json:"profile"`
			Destinations []string `json:"destinations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Profile == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "profile is required"})
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

		if _, err := os.Stat(profile.Source); os.IsNotExist(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("source directory %q does not exist", profile.Source)})
			return
		}

		existing, _ := transfer.FindSessionForDevice(webDeviceID, req.Profile)
		if existing != nil && !existing.IsComplete() {
			if existing.IsStale() {
				transfer.DeleteSession(existing.Path)
			} else {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"ok":      true,
					"session": existing.Path,
					"files":   len(existing.Files),
				})
				return
			}
		}

		manifest, err := diff.BuildManifest(profile.Source)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to read source: %v", err)})
			return
		}

		var filePaths []string
		for _, f := range manifest.Files {
			filePaths = append(filePaths, f.Path)
		}

		sess := transfer.NewSession(webDeviceID, req.Profile, filePaths)
		if err := sess.Save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
			return
		}

		serverAddr := fmt.Sprintf("http://localhost:%d", cfg.Daemon.Port)
		token, _ := config.GetLocalToken()
		sessionPath := sess.Path
		profileName := profile.Name

		// Guard: skip if a web sync is already in progress for this profile
		if _, loaded := webSyncActive.LoadOrStore(profileName, true); loaded {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"ok":      true,
				"session": sessionPath,
				"files":   len(filePaths),
			})
			return
		}

		// Sync to selected destinations in background
		go func() {
			defer webSyncActive.Delete(profileName)
			for destName, destPath := range profile.Destinations {
				if len(req.Destinations) > 0 && !webDestContains(req.Destinations, destName) {
					continue
				}
				logInfo("web sync: profile=%s dest=%s path=%s", profileName, destName, destPath)
				_, err := client.SyncProfile(serverAddr, token, profileName, webDeviceID, destPath, false, nil)
				if err != nil {
					logError("web sync failed for profile %s dest %s: %v", profileName, destName, err)
				}
			}
		}()

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"session": sessionPath,
			"files":   len(filePaths),
		})
	}
}

func handleWebSessions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		type sessionSummary struct {
			Path     string `json:"path"`
			DeviceID string `json:"device_id"`
			Profile  string `json:"profile"`
			Total    int    `json:"total"`
			Done     int    `json:"done"`
			Errors   int    `json:"errors"`
			Complete bool   `json:"complete"`
		}

		dir := config.SessionsDir()
		entries, err := os.ReadDir(dir)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": []sessionSummary{}})
			return
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		var sessions []sessionSummary
		start := 0
		if len(entries) > maxSessionReturn {
			start = len(entries) - maxSessionReturn
		}

		for _, e := range entries[start:] {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			sess, err := transfer.LoadSession(path)
			if err != nil {
				continue
			}

			// Auto-cleanup: delete stale incomplete sessions
			if !sess.IsComplete() && sess.IsStale() {
				transfer.DeleteSession(path)
				continue
			}

			// Auto-cleanup: delete completed sessions too (they're done)
			if sess.IsComplete() {
				transfer.DeleteSession(path)
				continue
			}

			var done, errors int
			for _, f := range sess.Files {
				switch f.State {
				case transfer.FileDone:
					done++
				case transfer.FileError:
					errors++
				}
			}

			sessions = append(sessions, sessionSummary{
				Path:     sess.Path,
				DeviceID: sess.DeviceID,
				Profile:  sess.Profile,
				Total:    len(sess.Files),
				Done:     done,
				Errors:   errors,
				Complete: sess.IsComplete(),
			})
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": sessions})
	}
}

func staticFileHandler(webDir string) http.HandlerFunc {
	fs := http.Dir(webDir)
	fileServer := http.FileServer(fs)

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/" {
			path = "/index.html"
		}

		cleaned := filepath.Clean(path)
		fullPath := filepath.Join(webDir, cleaned)

		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			// SPA fallback: serve index.html for any path that doesn't match a real file.
			// Use http.ServeContent directly so this works for all HTTP methods,
			// not just GET/HEAD (which is all http.FileServer supports).
			indexPath := filepath.Join(webDir, "index.html")
			f, err := os.Open(indexPath)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			stat, err := f.Stat()
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, indexPath, stat.ModTime(), f)
			return
		}

		fileServer.ServeHTTP(w, r)
	}
}
