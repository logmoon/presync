package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"presync/internal/config"
)

func TestFetchAutoSyncConfig(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auto-sync" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotToken = r.Header.Get("X-Presync-Token")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"cooldown_minutes": 10,
			"profiles":         []string{"Music"},
		})
	}))
	defer server.Close()

	cfg, err := FetchAutoSyncConfig(server.Listener.Addr().String(), "token123")
	if err != nil {
		t.Fatalf("FetchAutoSyncConfig: %v", err)
	}
	if gotToken != "token123" {
		t.Errorf("token header: got %q", gotToken)
	}
	if cfg.CooldownMinutes != 10 {
		t.Errorf("cooldown: got %d, want 10", cfg.CooldownMinutes)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0] != "Music" {
		t.Errorf("profiles: got %v", cfg.Profiles)
	}
}

func TestWatchCycleRespectsCooldown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	syncCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/auto-sync":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"cooldown_minutes": 15,
				"profiles":         []string{"Music"},
			})
		default:
			if strings.HasPrefix(r.URL.Path, "/sync") {
				syncCalls++
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	if err := config.SaveServer(config.ServerEntry{
		Address:        addr,
		Token:          "token123",
		DeviceID:       "device1",
		Profiles:       []config.ServerProfile{{Name: "Music", Destination: t.TempDir()}},
		LastAutoSyncAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := runWatchCycle("")
	if err != nil {
		t.Fatalf("runWatchCycle: %v", err)
	}
	if res.Triggered {
		t.Fatal("expected cooldown to suppress the trigger")
	}
	if res.CooldownRemaining <= 0 {
		t.Errorf("expected positive cooldown remaining, got %v", res.CooldownRemaining)
	}
	if syncCalls != 0 {
		t.Errorf("expected no sync calls during cooldown, got %d", syncCalls)
	}
}

func TestWatchCycleNoEligibleProfiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/auto-sync":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"cooldown_minutes": 15,
				"profiles":         []string{},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	if err := config.SaveServer(config.ServerEntry{
		Address:  addr,
		Token:    "token123",
		DeviceID: "device1",
		Profiles: []config.ServerProfile{{Name: "Music", Destination: t.TempDir()}},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := runWatchCycle("")
	if err != nil {
		t.Fatalf("runWatchCycle: %v", err)
	}
	if res.Triggered {
		t.Fatal("expected no trigger without eligible profiles")
	}
}
