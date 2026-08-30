package transfer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"presync/internal/config"
)

func TestNewSession(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3", "b.mp3", "c.mp3"})

	if s.DeviceID != "abc12345" {
		t.Errorf("expected device ID 'abc12345', got %q", s.DeviceID)
	}
	if s.Profile != "music" {
		t.Errorf("expected profile 'music', got %q", s.Profile)
	}
	if len(s.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(s.Files))
	}
	for i, f := range s.Files {
		if f.State != FilePending {
			t.Errorf("file %d: expected state pending, got %q", i, f.State)
		}
	}
	if s.Path == "" {
		t.Error("expected non-empty path")
	}
}

func TestSessionMarkStates(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3", "b.mp3"})

	s.MarkInProgress("a.mp3")
	f := s.GetFile("a.mp3")
	if f == nil || f.State != FileInProgress {
		t.Error("expected a.mp3 to be in_progress")
	}

	s.MarkDone("a.mp3", "hash123")
	f = s.GetFile("a.mp3")
	if f == nil || f.State != FileDone || f.SHA256 != "hash123" {
		t.Error("expected a.mp3 to be done with hash")
	}

	s.MarkError("b.mp3")
	f = s.GetFile("b.mp3")
	if f == nil || f.State != FilePending {
		t.Error("expected b.mp3 to be pending after first error")
	}
	if f.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", f.Attempts)
	}

	s.MarkError("b.mp3")
	s.MarkError("b.mp3")
	f = s.GetFile("b.mp3")
	if f == nil || f.State != FileError {
		t.Error("expected b.mp3 to be error after 3 attempts")
	}
}

func TestSessionIsComplete(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3", "b.mp3"})

	if s.IsComplete() {
		t.Error("session should not be complete with pending files")
	}

	s.MarkDone("a.mp3", "hash1")
	if s.IsComplete() {
		t.Error("session should not be complete with one pending file")
	}

	s.MarkDone("b.mp3", "hash2")
	if !s.IsComplete() {
		t.Error("session should be complete")
	}
}

func TestSessionHasErrors(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3"})

	if s.HasErrors() {
		t.Error("session should not have errors initially")
	}

	s.MarkError("a.mp3")
	if s.HasErrors() {
		t.Error("session should not have error after 1 attempt (retryable)")
	}

	s.MarkError("a.mp3")
	s.MarkError("a.mp3")
	if !s.HasErrors() {
		t.Error("session should have error after 3 attempts")
	}
}

func TestSessionPendingFiles(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3", "b.mp3", "c.mp3"})

	pending := s.PendingFiles()
	if len(pending) != 3 {
		t.Errorf("expected 3 pending, got %d", len(pending))
	}

	s.MarkInProgress("a.mp3")
	s.MarkDone("b.mp3", "hash")

	// in_progress files are still work remaining and must be counted.
	pending = s.PendingFiles()
	if len(pending) != 2 {
		t.Errorf("expected 2 pending (incl. in_progress), got %d", len(pending))
	}
	if pending[0].Path != "a.mp3" {
		t.Errorf("expected a.mp3 first, got %q", pending[0].Path)
	}
	if pending[1].Path != "c.mp3" {
		t.Errorf("expected c.mp3 second, got %q", pending[1].Path)
	}
}

func TestSessionInProgressFile(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3", "b.mp3"})

	if f := s.InProgressFile(); f != nil {
		t.Error("expected no in-progress file")
	}

	s.MarkInProgress("b.mp3")
	f := s.InProgressFile()
	if f == nil || f.Path != "b.mp3" {
		t.Error("expected b.mp3 in progress")
	}
}

func TestSessionUpdateOffset(t *testing.T) {
	s := NewSession("abc12345", "music", []string{"a.mp3"})

	s.MarkInProgress("a.mp3")
	s.UpdateOffset("a.mp3", 1024)

	f := s.GetFile("a.mp3")
	if f == nil || f.Offset != 1024 {
		t.Errorf("expected offset 1024, got %v", f)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	s := NewSession("abc12345", "music", []string{"a.mp3", "b.mp3"})
	s.MarkInProgress("a.mp3")
	s.UpdateOffset("a.mp3", 512)

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadSession(s.Path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if loaded.DeviceID != s.DeviceID {
		t.Errorf("device ID mismatch: %q vs %q", loaded.DeviceID, s.DeviceID)
	}
	if loaded.Profile != s.Profile {
		t.Errorf("profile mismatch: %q vs %q", loaded.Profile, s.Profile)
	}
	if len(loaded.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(loaded.Files))
	}
	if loaded.Files[0].State != FileInProgress {
		t.Errorf("expected a.mp3 in_progress, got %q", loaded.Files[0].State)
	}
	if loaded.Files[0].Offset != 512 {
		t.Errorf("expected offset 512, got %d", loaded.Files[0].Offset)
	}
}

func TestResolveSessionContainment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	s := NewSession("abc12345", "music", []string{"a.mp3"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Plain filename resolves inside SessionsDir
	base := filepath.Base(s.Path)
	resolved, err := ResolveSession(base)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if filepath.Dir(resolved) != config.SessionsDir() {
		t.Errorf("expected resolution inside SessionsDir, got %q", resolved)
	}

	// Full path is collapsed to its basename (still contained)
	resolved2, err := ResolveSession(s.Path)
	if err != nil {
		t.Fatalf("ResolveSession(full path): %v", err)
	}
	if resolved2 != resolved {
		t.Errorf("expected %q, got %q", resolved, resolved2)
	}

	// Traversal attempts collapse to a basename inside SessionsDir — never escape
	for _, evil := range []string{"../../../../etc/passwd", "/etc/passwd", "..", "a/../../b.json"} {
		r, err := ResolveSession(evil)
		if err != nil {
			continue
		}
		if filepath.Dir(r) != config.SessionsDir() {
			t.Errorf("traversal %q escaped SessionsDir: %q", evil, r)
		}
	}

	// Empty is rejected
	if _, err := ResolveSession(""); err == nil {
		t.Error("expected error for empty session name")
	}
}

func TestSessionFilenamesUniqueAndSanitized(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	// Two sessions for the same device+profile must never collide
	a := NewSession("dev1", "music", []string{"a.mp3"})
	b := NewSession("dev1", "music", []string{"b.mp3"})
	if a.Path == b.Path {
		t.Fatal("expected distinct session filenames for same device+profile")
	}

	// Path-unsafe identifiers are sanitized into the filename
	c := NewSession("dev/1", "mu\\sic", []string{"c.mp3"})
	if c.Path == "" {
		t.Fatal("expected a session path")
	}
	if len(c.Path) != len(filepath.Join(config.SessionsDir(), "dev_1-mu_sic-.json"))+len("20060102T150405.000000000Z")-1 {
		t.Logf("sanitized session path: %s", c.Path)
	}
	base := filepath.Base(c.Path)
	if len(base) != len("dev_1-mu_sic-20060102T150405.000000000Z.json") {
		t.Errorf("unexpected sanitized filename: %s", base)
	}
}

func TestFindSessionForDevice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	s := NewSession("abc12345", "music", []string{"a.mp3"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	found, err := FindSessionForDevice("abc12345", "music")
	if err != nil {
		t.Fatalf("FindSessionForDevice: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find session")
	}
	if found.DeviceID != "abc12345" {
		t.Errorf("expected device ID abc12345, got %q", found.DeviceID)
	}

	notFound, err := FindSessionForDevice("xyz99999", "music")
	if err != nil {
		t.Fatalf("FindSessionForDevice: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent device")
	}
}

func TestPruneStaleSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	s := NewSession("abc12345", "music", []string{"a.mp3"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File should not be pruned yet
	PruneStaleSessions()
	if _, err := os.Stat(s.Path); os.IsNotExist(err) {
		t.Error("session should not be pruned (too new)")
	}

	// Make the file look old
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(s.Path, oldTime, oldTime)

	PruneStaleSessions()
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("session should have been pruned")
	}
}

func TestDeleteSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	s := NewSession("abc12345", "music", []string{"a.mp3"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := DeleteSession(s.Path); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("session file should have been deleted")
	}

	// Deleting non-existent should not error
	if err := DeleteSession(filepath.Join(dir, "nonexistent.json")); err != nil {
		t.Errorf("deleting nonexistent should not error: %v", err)
	}
}
