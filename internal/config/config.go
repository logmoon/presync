package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultPort                    = 7700
	defaultLogLevel                = "info"
	defaultAutoSyncCooldownMinutes = 15
	maxAutoSyncCooldownMinutes     = 1440
	configDirName                  = "presync"
	configFileName                 = "config.toml"
	pidFileName                    = "presync.pid"
	watchPIDFileName               = "watch.pid"
	logDirName                     = "logs"
	logFileName                    = "presync.log"
	watchLogFileName               = "watch.log"
	stateDirName                   = "state"

	DirectionPull = "pull"
	DirectionPush = "push"
)

type Config struct {
	Daemon   DaemonConfig `toml:"daemon"`
	Profiles []Profile    `toml:"profiles"`
}

type DaemonConfig struct {
	Port                    int    `toml:"port"`
	LogLevel                string `toml:"log_level"`
	AutoSync                bool   `toml:"auto_sync"`
	AutoSyncCooldownMinutes int    `toml:"auto_sync_cooldown_minutes"`
}

type Profile struct {
	Name        string `toml:"name"`
	Source      string `toml:"source"`
	Direction   string `toml:"direction"`   // "pull" (default) or "push"
	Destination string `toml:"destination"` // push only: server-side receive folder
	Mode        string `toml:"mode"`
	AutoSync    *bool  `toml:"auto_sync"`

	Destinations map[string]string `toml:"destinations"`
}

func defaults() Config {
	return Config{
		Daemon: DaemonConfig{
			Port:                    defaultPort,
			LogLevel:                defaultLogLevel,
			AutoSyncCooldownMinutes: defaultAutoSyncCooldownMinutes,
		},
	}
}

func ConfigDir() string {
	if env := os.Getenv("PRESYNC_CONFIG_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", configDirName)
	}
	return filepath.Join(home, ".config", configDirName)
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), configFileName)
}

func PIDPath() string {
	return filepath.Join(ConfigDir(), pidFileName)
}

// WatchPIDPath is the PID file for the detached client auto-sync watcher.
func WatchPIDPath() string {
	return filepath.Join(ConfigDir(), watchPIDFileName)
}

func LogDir() string {
	return filepath.Join(ConfigDir(), logDirName)
}

// StateDir holds the per-device last-synced baseline files. The server uses
// them to compute deltas without relying on recipient mtimes (Android SAF
// cannot preserve them). Owned by internal/transfer.
func StateDir() string {
	return filepath.Join(ConfigDir(), stateDirName)
}

func LogPath() string {
	return filepath.Join(LogDir(), logFileName)
}

// WatchLogPath is the log file for the detached client auto-sync watcher.
func WatchLogPath() string {
	return filepath.Join(LogDir(), watchLogFileName)
}

func EnsureDirs() error {
	dirs := []string{ConfigDir(), LogDir(), SessionsDir(), StateDir()}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}
	return nil
}

func Load() (Config, error) {
	cfg := defaults()

	path := ConfigPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	normalize(&cfg)

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func normalize(cfg *Config) {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Direction == "" {
			cfg.Profiles[i].Direction = DirectionPull
		}
	}
}

// AutoSyncEligible reports whether a profile participates in automatic
// syncing. Push profiles never auto-sync, and a profile-level auto_sync
// overrides the daemon-level default.
func (c Config) AutoSyncEligible(p Profile) bool {
	if p.Direction == DirectionPush {
		return false
	}
	if p.AutoSync != nil {
		return *p.AutoSync
	}
	return c.Daemon.AutoSync
}

func SaveConfig(cfg Config) error {
	normalize(&cfg)

	if err := validate(cfg); err != nil {
		return err
	}

	path := ConfigPath()
	tmp := path + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating config temp file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(cfg); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("encoding config: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing config temp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming config temp file: %w", err)
	}

	return nil
}

func validate(cfg Config) error {
	if cfg.Daemon.Port < 1 || cfg.Daemon.Port > 65535 {
		return fmt.Errorf("invalid daemon port %d: must be 1-65535", cfg.Daemon.Port)
	}

	validLogLevels := map[string]bool{"debug": true, "info": true, "error": true}
	if !validLogLevels[cfg.Daemon.LogLevel] {
		return fmt.Errorf("invalid log level %q: must be debug, info, or error", cfg.Daemon.LogLevel)
	}

	if cfg.Daemon.AutoSyncCooldownMinutes < 1 || cfg.Daemon.AutoSyncCooldownMinutes > maxAutoSyncCooldownMinutes {
		return fmt.Errorf("invalid auto_sync_cooldown_minutes %d: must be 1-%d", cfg.Daemon.AutoSyncCooldownMinutes, maxAutoSyncCooldownMinutes)
	}

	seen := make(map[string]bool, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		if p.Mode != "mirror" && p.Mode != "additive" {
			return fmt.Errorf("profile %q has invalid mode %q: must be mirror or additive", p.Name, p.Mode)
		}

		switch p.Direction {
		case DirectionPull:
			if p.Destination != "" {
				return fmt.Errorf("profile %q: destination is only valid for push profiles (use destinations for pull)", p.Name)
			}
		case DirectionPush:
			if p.Destination == "" {
				return fmt.Errorf("profile %q: push profiles require a destination folder", p.Name)
			}
			if len(p.Destinations) > 0 {
				return fmt.Errorf("profile %q: push profiles use destination, not destinations", p.Name)
			}
			if p.AutoSync != nil {
				return fmt.Errorf("profile %q: auto_sync is only valid for pull profiles", p.Name)
			}
		default:
			return fmt.Errorf("profile %q has invalid direction %q: must be pull or push", p.Name, p.Direction)
		}

		// Profile names flow into session filenames and CLI/UI filters — they
		// must be non-empty, unique, and path-safe.
		if p.Name == "" {
			return fmt.Errorf("profile name must not be empty")
		}
		if strings.ContainsAny(p.Name, "/\\") {
			return fmt.Errorf("profile name %q must not contain path separators", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate profile name %q", p.Name)
		}
		seen[p.Name] = true
	}

	return nil
}
