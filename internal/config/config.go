package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultPort     = 7700
	defaultLogLevel = "info"
	configDirName   = "presync"
	configFileName  = "config.toml"
	pidFileName     = "presync.pid"
	logDirName      = "logs"
	logFileName     = "presync.log"
)

type Config struct {
	Daemon   DaemonConfig `toml:"daemon"`
	Profiles []Profile    `toml:"profiles"`
}

type DaemonConfig struct {
	Port     int    `toml:"port"`
	LogLevel string `toml:"log_level"`
}

type Profile struct {
	Name         string            `toml:"name"`
	Source       string            `toml:"source"`
	Destinations map[string]string `toml:"destinations"`
	Mode         string            `toml:"mode"`
	AutoSync     *bool             `toml:"auto_sync"`
}

func defaults() Config {
	return Config{
		Daemon: DaemonConfig{
			Port:     defaultPort,
			LogLevel: defaultLogLevel,
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

func LogDir() string {
	return filepath.Join(ConfigDir(), logDirName)
}

func LogPath() string {
	return filepath.Join(LogDir(), logFileName)
}

func EnsureDirs() error {
	dirs := []string{ConfigDir(), LogDir(), SessionsDir()}
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

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func SaveConfig(cfg Config) error {
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

	seen := make(map[string]bool, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		if p.Mode != "mirror" && p.Mode != "additive" {
			return fmt.Errorf("profile %q has invalid mode %q: must be mirror or additive", p.Name, p.Mode)
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
