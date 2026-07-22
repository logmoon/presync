package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasLocalToken_False(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if HasLocalToken() {
		t.Error("expected HasLocalToken to return false when no token exists")
	}
}

func TestGetLocalToken_Creates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	token, err := GetLocalToken()
	if err != nil {
		t.Fatalf("GetLocalToken: %v", err)
	}

	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars: %s", len(token), token)
	}

	path := filepath.Join(dir, localTokenFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if string(data) != token {
		t.Errorf("file content %q does not match returned token %q", string(data), token)
	}
}

func TestGetLocalToken_Idempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	token1, err := GetLocalToken()
	if err != nil {
		t.Fatalf("GetLocalToken (first): %v", err)
	}

	token2, err := GetLocalToken()
	if err != nil {
		t.Fatalf("GetLocalToken (second): %v", err)
	}

	if token1 != token2 {
		t.Errorf("expected same token on second call, got %q and %q", token1, token2)
	}
}

func TestHasLocalToken_AfterCreate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if _, err := GetLocalToken(); err != nil {
		t.Fatalf("GetLocalToken: %v", err)
	}

	if !HasLocalToken() {
		t.Error("expected HasLocalToken to return true after creating token")
	}
}
