package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"presync/internal/config"
	"presync/internal/pairing"
)

type pairResponse struct {
	Token string `json:"token"`
	PIN   string `json:"pin"`
}

type pairAcceptRequest struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

type pairConfirmResponse struct {
	DeviceID       string            `json:"device_id"`
	LongLivedToken string            `json:"token"`
	Destinations   map[string]string `json:"destinations,omitempty"`
}

func handlePairStart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		session, err := pairing.NewSession()
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, pairResponse{
			Token: session.Token,
			PIN:   session.PIN,
		})
	}
}

func handlePairAccept() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req pairAcceptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		session := pairing.GetSession()
		if session == nil {
			writeJSON(w, http.StatusGone, map[string]string{"error": "no active pairing session or session expired"})
			return
		}

		if session.Token != req.Token {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "token mismatch"})
			return
		}

		if err := pairing.AcceptSession(req.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "pin": session.PIN})
	}
}

func handlePairConfirm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		token, deviceID, destinations, err := pairing.ConfirmSession()
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}

		// Look up device name by ID from devices.json (used for the legacy
		// name-keyed destination entries and for display).
		df, loadErr := config.LoadDevices()
		var deviceName string
		if loadErr == nil {
			for _, d := range df.Devices {
				if d.ID == deviceID {
					deviceName = d.Name
					break
				}
			}
		}
		if deviceName == "" {
			deviceName = "unknown"
		}

		// Write destinations to config.toml keyed by the device ID (stable per
		// device), keeping a legacy name-keyed entry for pre-ID configs.
		// The config is reloaded from disk first: the daemon's in-memory copy
		// may be stale if the user edited config.toml while the daemon ran.
		cfg, cfgErr := config.Load()
		if cfgErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("reloading config: %v", cfgErr)})
			return
		}

		if len(destinations) > 0 {
			for i := range cfg.Profiles {
				if cfg.Profiles[i].Direction == config.DirectionPush {
					continue
				}
				if cfg.Profiles[i].Destinations == nil {
					cfg.Profiles[i].Destinations = make(map[string]string)
				}
				if dest, ok := destinations[cfg.Profiles[i].Name]; ok {
					cfg.Profiles[i].Destinations[deviceID] = dest
					// Upgrade: remove the legacy name-keyed entry for this device
					if deviceName != "" {
						delete(cfg.Profiles[i].Destinations, deviceName)
					}
				}
			}
			if err := config.SaveConfig(cfg); err != nil {
				logError("saving destinations to config: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("paired but failed to save destinations: %v", err)})
				return
			}

			// Create destination directories on disk
			for _, dest := range destinations {
				if err := os.MkdirAll(dest, 0o755); err != nil {
					logError("creating destination directory %s: %v", dest, err)
				}
			}
		} else {
			// No server-side destinations — remove any stale entry for this device
			changed := false
			for i := range cfg.Profiles {
				if cfg.Profiles[i].Direction == config.DirectionPush {
					continue
				}
				if cfg.Profiles[i].Destinations != nil {
					if _, ok := cfg.Profiles[i].Destinations[deviceID]; ok {
						delete(cfg.Profiles[i].Destinations, deviceID)
						changed = true
					}
					if deviceName != "" {
						if _, ok := cfg.Profiles[i].Destinations[deviceName]; ok {
							delete(cfg.Profiles[i].Destinations, deviceName)
							changed = true
						}
					}
				}
			}
			if changed {
				if err := config.SaveConfig(cfg); err != nil {
					logError("cleaning destinations from config: %v", err)
				}
			}
		}

		// Reload daemon config
		reloadCfg, err := config.Load()
		if err == nil {
			cfgMu.Lock()
			activeCfg = &reloadCfg
			cfgMu.Unlock()
		}

		writeJSON(w, http.StatusOK, pairConfirmResponse{
			DeviceID:       deviceID,
			LongLivedToken: token,
			Destinations:   destinations,
		})
	}
}

func handlePairDestinations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		// Require an active pairing session
		session := pairing.GetSession()
		if session == nil {
			writeJSON(w, http.StatusGone, map[string]string{"error": "no active pairing session"})
			return
		}

		var req struct {
			Token        string            `json:"token"`
			Name         string            `json:"name"`
			Destinations map[string]string `json:"destinations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Token == "" || req.Token != session.Token {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "session token required to register destinations"})
			return
		}

		if req.Name == "" || len(req.Destinations) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and destinations are required"})
			return
		}

		// Store destinations on the pairing session only.
		// Config.toml is written during confirm, keyed by the actual device name.
		pairing.SetDestinations(req.Destinations)

		logInfo("destinations registered for device %q: %v", req.Name, req.Destinations)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handlePairDiscard() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		pairing.DiscardSession()
		writeJSON(w, http.StatusOK, map[string]string{"status": "discarded"})
	}
}

func handlePairStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		session := pairing.GetSession()
		if session == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"active": false})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"active":      true,
			"accepted":    session.Accepted,
			"device_name": session.DeviceName,
		})
	}
}

func handlePairConfirmed() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		sessionToken := r.URL.Query().Get("token")
		deviceID, token, destinations, ok := pairing.GetLastConfirmed(sessionToken)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]interface{}{"confirmed": false})
			return
		}

		resp := map[string]interface{}{
			"confirmed": true,
			"device_id": deviceID,
			"token":     token,
		}
		if len(destinations) > 0 {
			resp["destinations"] = destinations
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
