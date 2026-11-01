package transfer

import (
	"testing"

	"presync/internal/config"
	"presync/internal/diff"
)

func TestSaveLoadDeleteState(t *testing.T) {
	t.Setenv("PRESYNC_CONFIG_DIR", t.TempDir())

	st := &SyncState{
		DeviceID:  "dev1",
		Profile:   "music",
		Direction: "pull",
		Files: []StateFile{
			{Path: "a.txt", Size: 10, ModTime: 1000, SHA256: "abc"},
			{Path: "b.txt", Size: 20, ModTime: 2000},
		},
	}
	if err := SaveState(st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := LoadState("dev1", "music")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got == nil || len(got.Files) != 2 {
		t.Fatalf("unexpected state: %+v", got)
	}
	if got.Files[0].Path != "a.txt" || got.Files[0].SHA256 != "abc" || got.Files[0].ModTime != 1000 {
		t.Errorf("unexpected first entry: %+v", got.Files[0])
	}
	if got.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}

	if err := DeleteStateForDevice("dev1"); err != nil {
		t.Fatalf("DeleteStateForDevice: %v", err)
	}
	got, err = LoadState("dev1", "music")
	if err != nil {
		t.Fatalf("LoadState after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil state after delete, got %+v", got)
	}
}

func TestLoadStateMissingIsNil(t *testing.T) {
	t.Setenv("PRESYNC_CONFIG_DIR", t.TempDir())

	st, err := LoadState("nobody", "nothing")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st != nil {
		t.Errorf("expected nil state, got %+v", st)
	}
}

func TestBuildCompletionState(t *testing.T) {
	prev := &SyncState{
		DeviceID: "dev1",
		Profile:  "music",
		Files: []StateFile{
			{Path: "unchanged.txt", Size: 10, ModTime: 1000},
			{Path: "updated.txt", Size: 20, ModTime: 2000},
			{Path: "deleted-at-source.txt", Size: 30, ModTime: 3000},
		},
	}
	files := []FileSession{
		{Path: "updated.txt", State: FileDone, Size: 21, ModTime: 2001, SHA256: "newhash"},
		{Path: "new.txt", State: FileDone, Size: 40, ModTime: 4000, SHA256: "newfile"},
		{Path: "still-pending.txt", State: FilePending, Size: 50, ModTime: 5000},
	}
	sourcePaths := map[string]bool{
		"unchanged.txt": true,
		"updated.txt":   true,
		"new.txt":       true,
	}

	st := BuildCompletionState("dev1", "music", "pull", prev, files, sourcePaths)
	if len(st.Files) != 3 {
		t.Fatalf("expected 3 files, got %+v", st.Files)
	}

	byPath := make(map[string]StateFile, len(st.Files))
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if f, ok := byPath["unchanged.txt"]; !ok || f.Size != 10 || f.ModTime != 1000 {
		t.Errorf("unchanged entry not preserved: %+v", f)
	}
	if f, ok := byPath["updated.txt"]; !ok || f.Size != 21 || f.ModTime != 2001 || f.SHA256 != "newhash" {
		t.Errorf("session entry did not win: %+v", f)
	}
	if f, ok := byPath["new.txt"]; !ok || f.Size != 40 {
		t.Errorf("new session file missing: %+v", f)
	}
	if _, ok := byPath["deleted-at-source.txt"]; ok {
		t.Error("expected source-deleted file to be dropped from the baseline")
	}
	if _, ok := byPath["still-pending.txt"]; ok {
		t.Error("pending files must not enter the baseline")
	}
}

func TestBuildCompletionStateNilSourceKeepsEntries(t *testing.T) {
	files := []FileSession{
		{Path: "a.txt", State: FileDone, Size: 1, ModTime: 1},
	}
	st := BuildCompletionState("dev1", "p", "push", nil, files, nil)
	if len(st.Files) != 1 || st.Files[0].Path != "a.txt" {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestSetFileMetaPreservesState(t *testing.T) {
	sess := NewSession("dev1", "p", []string{"a"})
	sess.MarkInProgress("a")
	sess.UpdateOffset("a", 12)

	sess.SetFileMeta("a", 42, 1000)

	f := sess.GetFile("a")
	if f.State != FileInProgress {
		t.Errorf("state changed: %q", f.State)
	}
	if f.Offset != 12 {
		t.Errorf("offset changed: %d", f.Offset)
	}
	if f.Size != 42 || f.ModTime != 1000 {
		t.Errorf("metadata not set: %+v", f)
	}
}

func TestStateManifest(t *testing.T) {
	st := &SyncState{Files: []StateFile{{Path: "a", Size: 1, ModTime: 2, SHA256: "x"}}}
	m := st.Manifest()
	if len(m.Files) != 1 || m.Files[0].Path != "a" || m.Files[0].SHA256 != "x" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
}

func TestStateFilesFromManifest(t *testing.T) {
	m := diff.Manifest{Files: []diff.FileEntry{{Path: "a", Size: 1, ModTime: 2}}}
	files := StateFilesFromManifest(m)
	if len(files) != 1 || files[0].Path != "a" || files[0].Size != 1 || files[0].ModTime != 2 {
		t.Fatalf("unexpected files: %+v", files)
	}
}

func TestSessionSourceFilesRoundTrip(t *testing.T) {
	t.Setenv("PRESYNC_CONFIG_DIR", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("creating dirs: %v", err)
	}

	sess := NewSessionWithDirection("dev1", "p", "push", nil)
	sess.SetSourceFiles([]StateFile{})
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	loaded, err := LoadSession(sess.Path)
	if err != nil {
		t.Fatalf("loading session: %v", err)
	}
	if loaded.SourceFileSnapshot() == nil {
		t.Fatal("expected a known-empty source snapshot, got nil")
	}
	if len(loaded.SourceFileSnapshot()) != 0 {
		t.Fatalf("expected empty snapshot, got %+v", loaded.SourceFileSnapshot())
	}
}
