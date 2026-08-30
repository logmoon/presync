package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWithProfile(t *testing.T) {
	dir := t.TempDir()

	cfgContent := `[daemon]
port = 7700
log_level = "info"

[[profiles]]
name = "Music"
source = "/home/user/Music"
mode = "mirror"

[profiles.destinations]
local = "/mnt/sdcard/Music"
laptop = "/home/laptop/Music"

[[profiles]]
name = "Podcasts"
source = "/home/user/Podcasts"
mode = "additive"

[profiles.destinations]
local = "/mnt/sdcard/Podcasts"
laptop = "/home/laptop/Podcasts"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(cfg.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(cfg.Profiles))
	}

	// First profile
	p := cfg.Profiles[0]
	if p.Name != "Music" {
		t.Errorf("profile 0 name: got %q, want %q", p.Name, "Music")
	}
	if p.Source != "/home/user/Music" {
		t.Errorf("profile 0 source: got %q, want %q", p.Source, "/home/user/Music")
	}
	if p.Destinations["local"] != "/mnt/sdcard/Music" {
		t.Errorf("profile 0 destination local: got %q, want %q", p.Destinations["local"], "/mnt/sdcard/Music")
	}
	if p.Destinations["laptop"] != "/home/laptop/Music" {
		t.Errorf("profile 0 destination laptop: got %q, want %q", p.Destinations["laptop"], "/home/laptop/Music")
	}
	if p.Mode != "mirror" {
		t.Errorf("profile 0 mode: got %q, want %q", p.Mode, "mirror")
	}

	// Second profile
	p2 := cfg.Profiles[1]
	if p2.Name != "Podcasts" {
		t.Errorf("profile 1 name: got %q, want %q", p2.Name, "Podcasts")
	}
	if p2.Mode != "additive" {
		t.Errorf("profile 1 mode: got %q, want %q", p2.Mode, "additive")
	}
}

func TestLoadWithoutProfiles(t *testing.T) {
	dir := t.TempDir()

	cfgContent := `[daemon]
port = 7700
log_level = "info"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(cfg.Profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(cfg.Profiles))
	}

	if cfg.Daemon.Port != 7700 {
		t.Errorf("daemon port: got %d, want %d", cfg.Daemon.Port, 7700)
	}
}

func TestLoadInvalidMode(t *testing.T) {
	dir := t.TempDir()

	cfgContent := `[daemon]
port = 7700
log_level = "info"

[[profiles]]
name = "Bad"
source = "/tmp/a"
mode = "invalid"

[profiles.destinations]
local = "/tmp/b"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}
}

func TestLoadNoConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with no config file should return defaults, got error: %v", err)
	}

	if len(cfg.Profiles) != 0 {
		t.Errorf("expected 0 profiles from defaults, got %d", len(cfg.Profiles))
	}

	if cfg.Daemon.Port != 7700 {
		t.Errorf("default port: got %d, want %d", cfg.Daemon.Port, 7700)
	}
}

func TestSaveConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg := Config{
		Daemon: DaemonConfig{Port: 8123, LogLevel: "debug"},
		Profiles: []Profile{
			{
				Name: "Music",
				Mode: "mirror",
				Destinations: map[string]string{
					"local":        "/srv/music",
					"abcdef123456": "/home/user/Music",
				},
			},
		},
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}

	if loaded.Daemon.Port != 8123 || loaded.Daemon.LogLevel != "debug" {
		t.Errorf("daemon roundtrip mismatch: %+v", loaded.Daemon)
	}
	if len(loaded.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(loaded.Profiles))
	}
	p := loaded.Profiles[0]
	if p.Name != "Music" || p.Mode != "mirror" {
		t.Errorf("profile roundtrip mismatch: %+v", p)
	}
	if p.Destinations["local"] != "/srv/music" || p.Destinations["abcdef123456"] != "/home/user/Music" {
		t.Errorf("destinations map roundtrip mismatch: %v", p.Destinations)
	}

	// No temp files left behind
	if _, err := os.Stat(ConfigPath() + ".tmp"); !os.IsNotExist(err) {
		t.Error("expected temp file to be cleaned up")
	}
}

func TestSaveConfigRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	// Invalid mode must be rejected by SaveConfig, matching Load()
	bad := Config{
		Daemon:   DaemonConfig{Port: 7700, LogLevel: "info"},
		Profiles: []Profile{{Name: "Music", Mode: "sideways"}},
	}
	if err := SaveConfig(bad); err == nil {
		t.Fatal("expected SaveConfig to reject invalid mode")
	}

	// Empty profile name rejected
	empty := Config{
		Daemon:   DaemonConfig{Port: 7700, LogLevel: "info"},
		Profiles: []Profile{{Name: "", Mode: "mirror"}},
	}
	if err := SaveConfig(empty); err == nil {
		t.Fatal("expected SaveConfig to reject empty profile name")
	}

	// Path-unsafe profile name rejected
	unsafe := Config{
		Daemon:   DaemonConfig{Port: 7700, LogLevel: "info"},
		Profiles: []Profile{{Name: "a/b", Mode: "mirror"}},
	}
	if err := SaveConfig(unsafe); err == nil {
		t.Fatal("expected SaveConfig to reject path-unsafe profile name")
	}

	// Duplicate profile names rejected
	dupes := Config{
		Daemon: DaemonConfig{Port: 7700, LogLevel: "info"},
		Profiles: []Profile{
			{Name: "Music", Mode: "mirror"},
			{Name: "Music", Mode: "additive"},
		},
	}
	if err := SaveConfig(dupes); err == nil {
		t.Fatal("expected SaveConfig to reject duplicate profile names")
	}

	// The config file must be untouched after rejected saves
	if _, err := os.Stat(ConfigPath()); !os.IsNotExist(err) {
		t.Error("expected no config file written for rejected saves")
	}
}
