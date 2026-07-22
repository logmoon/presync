package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionsDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	sessionsDir := SessionsDir()
	expected := filepath.Join(dir, "sessions")

	if sessionsDir != expected {
		t.Errorf("expected %q, got %q", expected, sessionsDir)
	}

	if err := EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	info, err := os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("sessions dir does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("sessions path is not a directory")
	}
}
