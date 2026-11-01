package server

import (
	"net/http"
)

// handleAutoSync reports which pull profiles may be synced automatically and
// the cooldown clients must respect. Eligibility lives in the server's
// config.toml; the on/off switch is owned by each client.
func handleAutoSync() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg := getConfig()

		profiles := make([]string, 0, len(cfg.Profiles))
		for _, p := range cfg.Profiles {
			if cfg.AutoSyncEligible(p) {
				profiles = append(profiles, p.Name)
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"cooldown_minutes": cfg.Daemon.AutoSyncCooldownMinutes,
			"profiles":         profiles,
		})
	}
}
