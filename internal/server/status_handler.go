package server

import (
	"net/http"

	"presync/internal/config"
)

func handleProfiles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		names := make([]string, len(cfg.Profiles))
		for i, p := range cfg.Profiles {
			names[i] = p.Name
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"profiles": names,
		})
	}
}

func handleStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := getConfig()

		devices, err := config.LoadDevices()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load devices"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"port":    cfg.Daemon.Port,
			"devices": len(devices.Devices),
		})
	}
}
