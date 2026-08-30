package config

import (
	"os"
	"path/filepath"
	"sync"
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

func TestGetLocalToken_ConcurrentCreate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	// Simulates daemon (web UI) and CLI racing on first use: all callers must
	// end up with the SAME token — no caller's token may be silently invalidated.
	const callers = 16
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token, err := GetLocalToken()
			if err != nil {
				t.Errorf("caller %d: GetLocalToken: %v", i, err)
				return
			}
			tokens[i] = token
		}(i)
	}
	wg.Wait()

	for i := 1; i < callers; i++ {
		if tokens[i] != tokens[0] {
			t.Fatalf("caller %d got different token %q vs %q", i, tokens[i], tokens[0])
		}
	}

	// The persisted token matches what everyone holds
	data, err := os.ReadFile(filepath.Join(dir, localTokenFileName))
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if string(data) != tokens[0] {
		t.Errorf("file token %q does not match caller token %q", string(data), tokens[0])
	}
}

func TestGetLocalToken_HealsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	// Simulate a crash mid-write: truncated token file
	path := filepath.Join(dir, localTokenFileName)
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatalf("writing corrupt token: %v", err)
	}

	token, err := GetLocalToken()
	if err != nil {
		t.Fatalf("GetLocalToken with corrupt file: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("expected regenerated 64-char token, got %d chars", len(token))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading token file: %v", err)
	}
	if string(data) != token {
		t.Errorf("file content %q does not match regenerated token %q", string(data), token)
	}
}
