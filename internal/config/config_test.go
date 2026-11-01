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
		Daemon: DaemonConfig{Port: 8123, LogLevel: "debug", AutoSyncCooldownMinutes: 15},
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
		Daemon:   DaemonConfig{Port: 7700, LogLevel: "info", AutoSyncCooldownMinutes: 15},
		Profiles: []Profile{{Name: "Music", Mode: "sideways"}},
	}
	if err := SaveConfig(bad); err == nil {
		t.Fatal("expected SaveConfig to reject invalid mode")
	}

	// Empty profile name rejected
	empty := Config{
		Daemon:   DaemonConfig{Port: 7700, LogLevel: "info", AutoSyncCooldownMinutes: 15},
		Profiles: []Profile{{Name: "", Mode: "mirror"}},
	}
	if err := SaveConfig(empty); err == nil {
		t.Fatal("expected SaveConfig to reject empty profile name")
	}

	// Path-unsafe profile name rejected
	unsafe := Config{
		Daemon:   DaemonConfig{Port: 7700, LogLevel: "info", AutoSyncCooldownMinutes: 15},
		Profiles: []Profile{{Name: "a/b", Mode: "mirror"}},
	}
	if err := SaveConfig(unsafe); err == nil {
		t.Fatal("expected SaveConfig to reject path-unsafe profile name")
	}

	// Duplicate profile names rejected
	dupes := Config{
		Daemon: DaemonConfig{Port: 7700, LogLevel: "info", AutoSyncCooldownMinutes: 15},
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

func TestLoadPushProfile(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `[daemon]
port = 7700
log_level = "info"

[[profiles]]
name = "PhoneMusic"
direction = "push"
destination = "/home/user/PhoneMusic"
mode = "mirror"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(cfg.Profiles))
	}
	p := cfg.Profiles[0]
	if p.Direction != DirectionPush {
		t.Errorf("direction: got %q, want %q", p.Direction, DirectionPush)
	}
	if p.Destination != "/home/user/PhoneMusic" {
		t.Errorf("destination: got %q", p.Destination)
	}
}

func TestLoadDirectionDefaultsToPull(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `[daemon]
port = 7700
log_level = "info"

[[profiles]]
name = "Music"
source = "/home/user/Music"
mode = "additive"

[profiles.destinations]
local = "/tmp/music"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Profiles[0].Direction != DirectionPull {
		t.Errorf("direction: got %q, want %q", cfg.Profiles[0].Direction, DirectionPull)
	}
}

func TestValidateDirectionRules(t *testing.T) {
	base := func() Config {
		return Config{
			Daemon: DaemonConfig{Port: 7700, LogLevel: "info", AutoSyncCooldownMinutes: 15},
		}
	}

	tests := []struct {
		name    string
		profile Profile
		wantErr bool
	}{
		{
			name:    "push requires destination",
			profile: Profile{Name: "p", Direction: DirectionPush, Mode: "additive"},
			wantErr: true,
		},
		{
			name:    "push rejects destinations map",
			profile: Profile{Name: "p", Direction: DirectionPush, Destination: "/tmp/p", Mode: "additive", Destinations: map[string]string{"local": "/tmp/x"}},
			wantErr: true,
		},
		{
			name:    "push accepts mirror",
			profile: Profile{Name: "p", Direction: DirectionPush, Destination: "/tmp/p", Mode: "mirror"},
			wantErr: false,
		},
		{
			name:    "pull rejects destination",
			profile: Profile{Name: "p", Direction: DirectionPull, Destination: "/tmp/p", Mode: "additive"},
			wantErr: true,
		},
		{
			name:    "invalid direction",
			profile: Profile{Name: "p", Direction: "sideways", Mode: "additive"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			cfg.Profiles = []Profile{tt.profile}
			err := validate(cfg)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestAutoSyncEligible(t *testing.T) {
	on := true
	off := false

	tests := []struct {
		name    string
		daemon  bool
		profile Profile
		want    bool
	}{
		{name: "daemon off, no override", daemon: false, profile: Profile{Direction: DirectionPull}, want: false},
		{name: "daemon on, no override", daemon: true, profile: Profile{Direction: DirectionPull}, want: true},
		{name: "daemon on, profile off", daemon: true, profile: Profile{Direction: DirectionPull, AutoSync: &off}, want: false},
		{name: "daemon off, profile on", daemon: false, profile: Profile{Direction: DirectionPull, AutoSync: &on}, want: true},
		{name: "push never eligible", daemon: true, profile: Profile{Direction: DirectionPush}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Daemon: DaemonConfig{AutoSync: tt.daemon}}
			if got := cfg.AutoSyncEligible(tt.profile); got != tt.want {
				t.Fatalf("AutoSyncEligible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadAutoSyncConfig(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `[daemon]
port = 7700
log_level = "info"
auto_sync = true
auto_sync_cooldown_minutes = 5

[[profiles]]
name = "Music"
source = "/home/user/Music"
mode = "mirror"

[profiles.destinations]
local = "/mnt/music"

[[profiles]]
name = "Videos"
source = "/home/user/Videos"
mode = "additive"
auto_sync = false

[profiles.destinations]
local = "/mnt/videos"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.Daemon.AutoSync {
		t.Error("daemon auto_sync: got false, want true")
	}
	if cfg.Daemon.AutoSyncCooldownMinutes != 5 {
		t.Errorf("cooldown: got %d, want 5", cfg.Daemon.AutoSyncCooldownMinutes)
	}
	if !cfg.AutoSyncEligible(cfg.Profiles[0]) {
		t.Error("Music should be eligible via daemon default")
	}
	if cfg.AutoSyncEligible(cfg.Profiles[1]) {
		t.Error("Videos should not be eligible via profile override")
	}
}

func TestLoadAutoSyncDefaults(t *testing.T) {
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
	if cfg.Daemon.AutoSync {
		t.Error("auto_sync should default to false")
	}
	if cfg.Daemon.AutoSyncCooldownMinutes != defaultAutoSyncCooldownMinutes {
		t.Errorf("cooldown default: got %d, want %d", cfg.Daemon.AutoSyncCooldownMinutes, defaultAutoSyncCooldownMinutes)
	}
}

func TestLoadAutoSyncInvalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "cooldown zero",
			body: "[daemon]\nport = 7700\nlog_level = \"info\"\nauto_sync_cooldown_minutes = 0\n",
		},
		{
			name: "cooldown too large",
			body: "[daemon]\nport = 7700\nlog_level = \"info\"\nauto_sync_cooldown_minutes = 1441\n",
		},
		{
			name: "push profile with auto_sync",
			body: "[daemon]\nport = 7700\nlog_level = \"info\"\n\n[[profiles]]\nname = \"p\"\ndirection = \"push\"\ndestination = \"/tmp/p\"\nmode = \"additive\"\nauto_sync = true\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tt.body), 0o644); err != nil {
				t.Fatalf("writing test config: %v", err)
			}
			t.Setenv("PRESYNC_CONFIG_DIR", dir)
			if _, err := Load(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
