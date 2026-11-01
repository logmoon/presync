package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"presync/internal/config"
)

func TestHandleAutoSyncReportsEligiblePullProfiles(t *testing.T) {
	off := false
	cfg := config.Config{
		Daemon: config.DaemonConfig{Port: 7700, LogLevel: "info", AutoSync: true, AutoSyncCooldownMinutes: 5},
		Profiles: []config.Profile{
			{Name: "Music", Direction: config.DirectionPull, Mode: "mirror"},
			{Name: "Videos", Direction: config.DirectionPull, Mode: "additive", AutoSync: &off},
			{Name: "Photos", Direction: config.DirectionPush, Destination: "/tmp/photos", Mode: "additive"},
		},
	}

	cfgMu.Lock()
	prev := activeCfg
	activeCfg = &cfg
	cfgMu.Unlock()
	t.Cleanup(func() {
		cfgMu.Lock()
		activeCfg = prev
		cfgMu.Unlock()
	})

	req := httptest.NewRequest(http.MethodGet, "/auto-sync", nil)
	w := httptest.NewRecorder()
	handleAutoSync()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200: %s", w.Code, w.Body.String())
	}

	var resp struct {
		CooldownMinutes int      `json:"cooldown_minutes"`
		Profiles        []string `json:"profiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if resp.CooldownMinutes != 5 {
		t.Errorf("cooldown: got %d, want 5", resp.CooldownMinutes)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0] != "Music" {
		t.Errorf("profiles: got %v, want [Music]", resp.Profiles)
	}
}

func TestHandleAutoSyncRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/auto-sync", nil)
	w := httptest.NewRecorder()
	handleAutoSync()(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
}
