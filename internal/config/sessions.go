package config

import "path/filepath"

const sessionDirName = "sessions"

func SessionsDir() string {
	return filepath.Join(ConfigDir(), sessionDirName)
}
