package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"presync/internal/config"
	"presync/internal/diff"
	"presync/internal/transfer"
)

func setupSyncHandlerTest(t *testing.T, srcFiles map[string]string) (string, string) {
	t.Helper()

	cfgDir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", cfgDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	srcDir := t.TempDir()
	for name, content := range srcFiles {
		path := filepath.Join(srcDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing file: %v", err)
		}
	}

	cfg := config.Config{
		Daemon: config.DaemonConfig{Port: 7700, LogLevel: "info"},
		Profiles: []config.Profile{
			{Name: "music", Source: srcDir, Mode: "mirror"},
		},
	}
	newServer(&cfg)
	setupLogger("error")

	return cfgDir, srcDir
}

func TestHandleSyncFile_OffsetBeyondSize(t *testing.T) {
	_, _ = setupSyncHandlerTest(t, map[string]string{"a.txt": "hello"})

	sess := transfer.NewSession("dev1", "music", []string{"a.txt"})
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	handler := handleSyncFile()
	req := httptest.NewRequest(http.MethodGet,
		"/sync/file?session="+filepath.Base(sess.Path)+"&path=a.txt&offset=100", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416 for offset beyond size, got %d", w.Code)
	}

	// The file must NOT have been marked in_progress — it stays resumable
	reloaded, err := transfer.LoadSession(sess.Path)
	if err != nil {
		t.Fatalf("reloading session: %v", err)
	}
	if reloaded.Files[0].State != transfer.FilePending {
		t.Errorf("expected file to stay pending after failed fetch, got %q", reloaded.Files[0].State)
	}

	// A negative offset is rejected outright
	req2 := httptest.NewRequest(http.MethodGet,
		"/sync/file?session="+filepath.Base(sess.Path)+"&path=a.txt&offset=-5", nil)
	w2 := httptest.NewRecorder()
	handler(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative offset, got %d", w2.Code)
	}
}

func TestHandleSyncFile_MissingSourceStaysPending(t *testing.T) {
	_, _ = setupSyncHandlerTest(t, map[string]string{"a.txt": "hello"})

	// Session references a file that was removed from the source
	sess := transfer.NewSession("dev1", "music", []string{"gone.txt"})
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	handler := handleSyncFile()
	req := httptest.NewRequest(http.MethodGet,
		"/sync/file?session="+filepath.Base(sess.Path)+"&path=gone.txt", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing source, got %d", w.Code)
	}

	reloaded, err := transfer.LoadSession(sess.Path)
	if err != nil {
		t.Fatalf("reloading session: %v", err)
	}
	if reloaded.Files[0].State != transfer.FilePending {
		t.Errorf("expected file to stay pending, got %q", reloaded.Files[0].State)
	}
}

func TestHandleSyncStart_FreshDeletesExistingSession(t *testing.T) {
	_, srcDir := setupSyncHandlerTest(t, map[string]string{"a.txt": "hello"})

	// Create an existing interrupted session (in_progress file)
	old := transfer.NewSession("dev1", "music", []string{"a.txt"})
	old.MarkInProgress("a.txt")
	if err := old.Save(); err != nil {
		t.Fatalf("saving old session: %v", err)
	}

	info, err := os.Stat(filepath.Join(srcDir, "a.txt"))
	if err != nil {
		t.Fatalf("stating source: %v", err)
	}
	dstManifest := diff.Manifest{Files: []diff.FileEntry{
		{Path: "a.txt", Size: info.Size(), ModTime: info.ModTime().Unix()},
	}}

	body, _ := json.Marshal(map[string]interface{}{
		"device_id":            "dev1",
		"profile":              "music",
		"destination_manifest": dstManifest,
		"fresh":                true,
	})

	handler := handleSyncStart()
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Old session file must be gone; a new session was created
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Errorf("expected old session deleted by --fresh, stat err=%v", err)
	}
	found, err := transfer.FindSessionForDevice("dev1", "music")
	if err != nil {
		t.Fatalf("FindSessionForDevice: %v", err)
	}
	if found == nil {
		t.Fatal("expected a new session")
	}
	if found.Path == old.Path {
		t.Error("expected a different session path after --fresh")
	}
}

func TestHandleSyncError_MarksFileError(t *testing.T) {
	_, _ = setupSyncHandlerTest(t, map[string]string{"a.txt": "hello"})

	sess := transfer.NewSession("dev1", "music", []string{"a.txt"})
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"session": sess.Path,
		"path":    "a.txt",
	})

	handler := handleSyncError()
	req := httptest.NewRequest(http.MethodPost, "/sync/error", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	reloaded, err := transfer.LoadSession(sess.Path)
	if err != nil {
		t.Fatalf("reloading session: %v", err)
	}
	if reloaded.Files[0].Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", reloaded.Files[0].Attempts)
	}
	if reloaded.Files[0].State != transfer.FilePending {
		t.Errorf("expected pending after 1 error, got %q", reloaded.Files[0].State)
	}

	// After MaxRetries errors the file reaches the error state
	handler = handleSyncError()
	for i := 0; i < transfer.MaxRetries; i++ {
		body, _ = json.Marshal(map[string]string{
			"session": sess.Path,
			"path":    "a.txt",
		})
		req := httptest.NewRequest(http.MethodPost, "/sync/error", bytes.NewReader(body))
		w2 := httptest.NewRecorder()
		handler(w2, req)
		if w2.Code != http.StatusOK {
			t.Fatalf("error %d: expected 200, got %d", i+1, w2.Code)
		}
	}
	reloaded, _ = transfer.LoadSession(sess.Path)
	if reloaded.Files[0].State != transfer.FileError {
		t.Errorf("expected error state after %d attempts, got %q", transfer.MaxRetries, reloaded.Files[0].State)
	}
}

func TestHandleSyncStart_ResumeReportsDoneState(t *testing.T) {
	_, srcDir := setupSyncHandlerTest(t, map[string]string{
		"a.txt": "hello",
		"b.txt": "world",
	})

	hash, err := transfer.ComputeFileHash(filepath.Join(srcDir, "a.txt"))
	if err != nil {
		t.Fatalf("hashing source: %v", err)
	}

	sess := transfer.NewSession("dev1", "music", []string{"a.txt", "b.txt"})
	sess.MarkDone("a.txt", hash)
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	// The client still has a.txt at the destination, so it is not in the
	// delta and must remain done. b.txt is missing, so it stays pending.
	srcManifest, err := diff.BuildManifest(srcDir)
	if err != nil {
		t.Fatalf("building source manifest: %v", err)
	}
	var dstFiles []diff.FileEntry
	for _, f := range srcManifest.Files {
		if f.Path == "a.txt" {
			dstFiles = append(dstFiles, f)
		}
	}

	body, _ := json.Marshal(map[string]interface{}{
		"device_id":            "dev1",
		"profile":              "music",
		"destination_manifest": diff.Manifest{Files: dstFiles},
	})

	handler := handleSyncStart()
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp syncStartResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	states := make(map[string]string, len(resp.Files))
	for _, f := range resp.Files {
		states[f.Path] = f.State
	}
	if states["a.txt"] != string(transfer.FileDone) {
		t.Errorf("expected a.txt state done, got %q", states["a.txt"])
	}
	if states["b.txt"] != string(transfer.FilePending) {
		t.Errorf("expected b.txt state pending, got %q", states["b.txt"])
	}
}

func TestHandleSyncStart_ResumeRequeuesDoneFileMissingAtDestination(t *testing.T) {
	_, srcDir := setupSyncHandlerTest(t, map[string]string{"a.txt": "hello", "b.txt": "world"})

	hash, err := transfer.ComputeFileHash(filepath.Join(srcDir, "a.txt"))
	if err != nil {
		t.Fatalf("hashing source: %v", err)
	}

	// b.txt keeps the session incomplete so the resume path is used.
	sess := transfer.NewSession("dev1", "music", []string{"a.txt", "b.txt"})
	sess.MarkDone("a.txt", hash)
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	// The client reports an empty destination: the done entry must be
	// re-queued rather than reported as already complete.
	body, _ := json.Marshal(map[string]interface{}{
		"device_id":            "dev1",
		"profile":              "music",
		"destination_manifest": diff.Manifest{},
	})
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handleSyncStart()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp syncStartResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	var found bool
	for _, f := range resp.Files {
		if f.Path != "a.txt" {
			continue
		}
		found = true
		if f.State != string(transfer.FilePending) {
			t.Errorf("expected a.txt pending, got %q", f.State)
		}
		if f.SHA256 != "" {
			t.Errorf("expected stale done hash cleared, got %q", f.SHA256)
		}
	}
	if !found {
		t.Fatalf("a.txt missing from response: %+v", resp.Files)
	}
}
