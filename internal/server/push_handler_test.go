package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"presync/internal/config"
	"presync/internal/diff"
	"presync/internal/transfer"
)

func setupPushHandlerTest(t *testing.T, mode string, destFiles, srcFiles map[string]string) (string, string) {
	t.Helper()

	t.Setenv("PRESYNC_CONFIG_DIR", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	destDir := t.TempDir()
	for name, content := range destFiles {
		writeTestFile(t, destDir, name, content)
	}

	srcDir := t.TempDir()
	for name, content := range srcFiles {
		writeTestFile(t, srcDir, name, content)
	}

	cfg := config.Config{
		Daemon: config.DaemonConfig{Port: 7700, LogLevel: "info"},
		Profiles: []config.Profile{
			{
				Name:        "PhoneMusic",
				Direction:   config.DirectionPush,
				Destination: destDir,
				Mode:        mode,
			},
		},
	}
	newServer(&cfg)
	setupLogger("error")

	return destDir, srcDir
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating parent dirs: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func pushStart(t *testing.T, srcDir string) pushStartResponse {
	t.Helper()

	manifest, err := diff.BuildManifest(srcDir)
	if err != nil {
		t.Fatalf("building source manifest: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"device_id":       "phone1",
		"profile":         "PhoneMusic",
		"source_manifest": manifest,
	})

	handler := handlePushStart()
	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("push start returned %d: %s", w.Code, w.Body.String())
	}

	var resp pushStartResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding push start: %v", err)
	}
	return resp
}

func pushChunk(t *testing.T, session, path string, offset int64, data []byte) (int, map[string]interface{}) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost,
		"/push/file?session="+filepath.Base(session)+"&path="+path+"&offset="+itoa(offset),
		bytes.NewReader(data))
	w := httptest.NewRecorder()
	handlePushFile()(w, req)

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return w.Code, resp
}

func pushConfirm(t *testing.T, session, path, hash string) syncConfirmResponse {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"session": session,
		"path":    path,
		"sha256":  hash,
	})
	req := httptest.NewRequest(http.MethodPost, "/push/confirm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handlePushConfirm()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("push confirm returned %d: %s", w.Code, w.Body.String())
	}

	var resp syncConfirmResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding push confirm: %v", err)
	}
	return resp
}

func pushComplete(t *testing.T, session string) (int, string) {
	t.Helper()

	// Send only the basename: clients may send either form, and the handler
	// must delete the resolved session file.
	body, _ := json.Marshal(map[string]string{"session": filepath.Base(session)})
	req := httptest.NewRequest(http.MethodPost, "/push/complete", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handlePushComplete()(w, req)
	return w.Code, w.Body.String()
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func TestPushStartUploadConfirmComplete(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive",
		nil, map[string]string{"sub/a.mp3": "hello"})

	resp := pushStart(t, srcDir)
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Files[0].Path != "sub/a.mp3" || resp.Files[0].Offset != 0 {
		t.Fatalf("unexpected file info: %+v", resp.Files[0])
	}

	code, body := pushChunk(t, resp.Session, "sub/a.mp3", 0, []byte("hello"))
	if code != http.StatusOK {
		t.Fatalf("chunk upload returned %d: %v", code, body)
	}

	confirm := pushConfirm(t, resp.Session, "sub/a.mp3", hashBytes([]byte("hello")))
	if !confirm.OK {
		t.Fatalf("expected confirm ok, got %+v", confirm)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "sub", "a.mp3"))
	if err != nil {
		t.Fatalf("reading synced file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content mismatch: got %q", data)
	}

	code, completeBody := pushComplete(t, resp.Session)
	if code != http.StatusOK {
		t.Fatalf("complete returned %d: %s", code, completeBody)
	}
}

func TestPushStartRejectsTraversal(t *testing.T) {
	_, srcDir := setupPushHandlerTest(t, "additive", nil, nil)

	manifest := diff.Manifest{Files: []diff.FileEntry{
		{Path: "../escape.txt", Size: 3, ModTime: 1},
	}}
	body, _ := json.Marshal(map[string]interface{}{
		"device_id":       "phone1",
		"profile":         "PhoneMusic",
		"source_manifest": manifest,
	})
	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handlePushStart()(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal path, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(srcDir), "escape.txt")); !os.IsNotExist(err) {
		t.Error("traversal file was created")
	}
}

func TestPushResumeFromServerOffset(t *testing.T) {
	_, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{"a.mp3": "hello"})

	first := pushStart(t, srcDir)
	if code, body := pushChunk(t, first.Session, "a.mp3", 0, []byte("hel")); code != http.StatusOK {
		t.Fatalf("partial chunk returned %d: %v", code, body)
	}

	// Restart returns the offset the server already holds.
	second := pushStart(t, srcDir)
	if second.Session != first.Session {
		t.Fatalf("expected same session, got %q and %q", second.Session, first.Session)
	}
	if len(second.Files) != 1 || second.Files[0].Offset != 3 {
		t.Fatalf("expected offset 3, got %+v", second.Files)
	}

	code, body := pushChunk(t, second.Session, "a.mp3", 0, []byte("hel"))
	if code != http.StatusConflict {
		t.Fatalf("expected 409 for stale offset, got %d: %v", code, body)
	}
	if body["offset"] != float64(3) {
		t.Errorf("expected server offset 3, got %v", body["offset"])
	}

	if code, body := pushChunk(t, second.Session, "a.mp3", 3, []byte("lo")); code != http.StatusOK {
		t.Fatalf("resumed chunk returned %d: %v", code, body)
	}
	if confirm := pushConfirm(t, second.Session, "a.mp3", hashBytes([]byte("hello"))); !confirm.OK {
		t.Fatalf("expected confirm ok, got %+v", confirm)
	}
}

func TestPushChecksumMismatchRetries(t *testing.T) {
	_, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{"a.mp3": "hello"})

	resp := pushStart(t, srcDir)
	pushChunk(t, resp.Session, "a.mp3", 0, []byte("hello"))

	confirm := pushConfirm(t, resp.Session, "a.mp3", hashBytes([]byte("wrong")))
	if confirm.OK {
		t.Fatal("expected checksum mismatch")
	}
	if confirm.Action != "retry" {
		t.Errorf("expected retry action, got %q", confirm.Action)
	}

	sess, err := transfer.FindSessionForDeviceDirection("phone1", "PhoneMusic", config.DirectionPush)
	if err != nil || sess == nil {
		t.Fatalf("loading session: %v", err)
	}
	if sess.GetFile("a.mp3").State != transfer.FilePending {
		t.Errorf("expected pending after first mismatch, got %q", sess.GetFile("a.mp3").State)
	}
}

func TestPushEmptyFile(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{"empty.mp3": ""})

	resp := pushStart(t, srcDir)
	confirm := pushConfirm(t, resp.Session, "empty.mp3", hashBytes(nil))
	if !confirm.OK {
		t.Fatalf("expected empty-file confirm ok, got %+v", confirm)
	}

	info, err := os.Stat(filepath.Join(destDir, "empty.mp3"))
	if err != nil {
		t.Fatalf("stat empty file: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected empty file, got %d bytes", info.Size())
	}
}

func TestPushMirrorDeletesOnlyAfterComplete(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "mirror",
		map[string]string{"keep.mp3": "keep", "gone.mp3": "gone"},
		map[string]string{"keep.mp3": "keep"})

	resp := pushStart(t, srcDir)

	// The mirror deletion is planned but must not happen before completion.
	if _, err := os.Stat(filepath.Join(destDir, "gone.mp3")); err != nil {
		t.Fatalf("file deleted too early: %v", err)
	}

	// keep.mp3 is size+mtime ambiguous at best; if the delta is empty the
	// session is immediately complete.
	for _, f := range resp.Files {
		if f.Path == "keep.mp3" {
			pushChunk(t, resp.Session, f.Path, 0, []byte("keep"))
			if confirm := pushConfirm(t, resp.Session, f.Path, hashBytes([]byte("keep"))); !confirm.OK {
				t.Fatalf("confirm keep.mp3: %+v", confirm)
			}
		}
	}

	code, body := pushComplete(t, resp.Session)
	if code != http.StatusOK {
		t.Fatalf("complete returned %d: %s", code, body)
	}

	if _, err := os.Stat(filepath.Join(destDir, "gone.mp3")); !os.IsNotExist(err) {
		t.Errorf("expected gone.mp3 to be deleted, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "keep.mp3")); err != nil {
		t.Errorf("keep.mp3 must survive: %v", err)
	}
}

func TestPushMirrorRefusesEmptySource(t *testing.T) {
	_, srcDir := setupPushHandlerTest(t, "mirror",
		map[string]string{"gone.mp3": "gone"}, nil)

	manifest, err := diff.BuildManifest(srcDir)
	if err != nil {
		t.Fatalf("building source manifest: %v", err)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"device_id":       "phone1",
		"profile":         "PhoneMusic",
		"source_manifest": manifest,
	})
	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handlePushStart()(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for empty mirror source, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPushAdditiveNeverDeletes(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive",
		map[string]string{"keep.mp3": "keep", "extra.mp3": "extra"},
		map[string]string{"keep.mp3": "keep"})

	resp := pushStart(t, srcDir)
	for _, f := range resp.Files {
		pushChunk(t, resp.Session, f.Path, 0, []byte("keep"))
		pushConfirm(t, resp.Session, f.Path, hashBytes([]byte("keep")))
	}
	if code, body := pushComplete(t, resp.Session); code != http.StatusOK {
		t.Fatalf("complete returned %d: %s", code, body)
	}

	if _, err := os.Stat(filepath.Join(destDir, "extra.mp3")); err != nil {
		t.Errorf("additive push deleted a file: %v", err)
	}
}

func TestPushDirectionGuards(t *testing.T) {
	setupPushHandlerTest(t, "additive", nil, map[string]string{"a.mp3": "hello"})

	// A push profile cannot be pulled.
	pullBody, _ := json.Marshal(map[string]interface{}{
		"device_id":            "phone1",
		"profile":              "PhoneMusic",
		"destination_manifest": diff.Manifest{},
	})
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(pullBody))
	w := httptest.NewRecorder()
	handleSyncStart()(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 pulling a push profile, got %d: %s", w.Code, w.Body.String())
	}

	// And a pull profile cannot be pushed.
	cfg := getConfig()
	cfg.Profiles = []config.Profile{{Name: "Music", Direction: config.DirectionPull, Source: t.TempDir(), Mode: "additive"}}
	cfgMu.Lock()
	activeCfg = &cfg
	cfgMu.Unlock()

	pushBody, _ := json.Marshal(map[string]interface{}{
		"device_id":       "phone1",
		"profile":         "Music",
		"source_manifest": diff.Manifest{},
	})
	req = httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(pushBody))
	w = httptest.NewRecorder()
	handlePushStart()(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 pushing a pull profile, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPushResumeAddsNewSourceFile(t *testing.T) {
	_, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{"a.mp3": "hello"})

	first := pushStart(t, srcDir)
	if len(first.Files) != 1 {
		t.Fatalf("expected 1 initial file, got %d", len(first.Files))
	}

	writeTestFile(t, srcDir, "b.mp3", "world")

	second := pushStart(t, srcDir)
	if second.Session != first.Session {
		t.Fatalf("expected resumed session, got %q and %q", second.Session, first.Session)
	}
	if len(second.Files) != 2 {
		t.Fatalf("expected both files after resume, got %+v", second.Files)
	}
}

func TestPushResumeDropsRemovedSourceFiles(t *testing.T) {
	_, srcDir := setupPushHandlerTest(t, "additive",
		nil, map[string]string{"a.mp3": "aaa", "b.mp3": "bbb"})

	first := pushStart(t, srcDir)
	if len(first.Files) != 2 {
		t.Fatalf("expected 2 initial files, got %+v", first.Files)
	}

	if err := os.Remove(filepath.Join(srcDir, "a.mp3")); err != nil {
		t.Fatalf("removing source file: %v", err)
	}

	second := pushStart(t, srcDir)
	if second.Session != first.Session {
		t.Fatalf("expected resumed session, got %q and %q", second.Session, first.Session)
	}
	if len(second.Files) != 1 || second.Files[0].Path != "b.mp3" {
		t.Fatalf("expected only b.mp3 after resume, got %+v", second.Files)
	}
}

func TestPushChecksumMismatchResetsOffsetAndRetries(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{"a.mp3": "hello"})

	resp := pushStart(t, srcDir)
	pushChunk(t, resp.Session, "a.mp3", 0, []byte("hello"))

	bad := pushConfirm(t, resp.Session, "a.mp3", hashBytes([]byte("wrong")))
	if bad.OK {
		t.Fatal("expected checksum mismatch")
	}

	sess, err := transfer.FindSessionForDeviceDirection("phone1", "PhoneMusic", config.DirectionPush)
	if err != nil || sess == nil {
		t.Fatalf("loading session: %v", err)
	}
	if got := sess.GetFile("a.mp3").Offset; got != 0 {
		t.Errorf("expected offset reset to 0, got %d", got)
	}

	// The retry must be able to upload the whole file again.
	if code, body := pushChunk(t, resp.Session, "a.mp3", 0, []byte("hello")); code != http.StatusOK {
		t.Fatalf("retry chunk returned %d: %v", code, body)
	}
	good := pushConfirm(t, resp.Session, "a.mp3", hashBytes([]byte("hello")))
	if !good.OK {
		t.Fatalf("expected retry confirm ok, got %+v", good)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "a.mp3"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("retried file content = %q, err=%v", data, err)
	}
}

func TestPushResumeReuploadsFileWhoseDestinationDisappeared(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{"a.mp3": "hello"})

	resp := pushStart(t, srcDir)
	pushChunk(t, resp.Session, "a.mp3", 0, []byte("hello"))
	if confirm := pushConfirm(t, resp.Session, "a.mp3", hashBytes([]byte("hello"))); !confirm.OK {
		t.Fatalf("initial confirm: %+v", confirm)
	}

	if err := os.Remove(filepath.Join(destDir, "a.mp3")); err != nil {
		t.Fatalf("removing destination file: %v", err)
	}

	again := pushStart(t, srcDir)
	if len(again.Files) != 1 || again.Files[0].Path != "a.mp3" {
		t.Fatalf("expected a.mp3 to be re-queued, got %+v", again.Files)
	}
}

func TestResolvePushPathRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	bad := []string{
		"",
		"/etc/passwd",
		"../escape",
		"a/../../escape",
		"..\\escape",
		"a\\b",
		"a.presync-tmp",
	}
	for _, rel := range bad {
		if _, err := resolvePushPath(root, rel); err == nil {
			t.Errorf("expected resolvePushPath to reject %q", rel)
		}
	}

	if _, err := resolvePushPath(root, "sub/a.mp3"); err != nil {
		t.Errorf("expected safe path to resolve: %v", err)
	}
}

func TestHandlePushStart_ResumeIgnoresDestinationMTime(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{
		"a.txt": "hello",
		"b.txt": "world",
	})

	srcManifest, err := diff.BuildManifest(srcDir)
	if err != nil {
		t.Fatalf("building source manifest: %v", err)
	}
	srcIndex := manifestIndex(srcManifest)
	a, b := srcIndex["a.txt"], srcIndex["b.txt"]

	first := pushStart(t, srcDir)
	sess, err := transfer.LoadSession(first.Session)
	if err != nil {
		t.Fatalf("loading session: %v", err)
	}

	// Simulate a completed upload of a.txt: the server-side file exists with a
	// receive-time mtime, and the session records it as done.
	if err := os.WriteFile(filepath.Join(destDir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing destination file: %v", err)
	}
	destTime := time.Unix(a.ModTime+5000, 0)
	if err := os.Chtimes(filepath.Join(destDir, "a.txt"), destTime, destTime); err != nil {
		t.Fatalf("setting destination mtime: %v", err)
	}
	hash, err := transfer.ComputeFileHash(filepath.Join(srcDir, "a.txt"))
	if err != nil {
		t.Fatalf("hashing source: %v", err)
	}
	sess.SetFileMeta("a.txt", a.Size, a.ModTime)
	sess.SetFileMeta("b.txt", b.Size, b.ModTime)
	sess.MarkDone("a.txt", hash)
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	second := pushStart(t, srcDir)
	states := make(map[string]string, len(second.Files))
	for _, f := range second.Files {
		states[f.Path] = f.State
	}
	if states["a.txt"] != string(transfer.FileDone) {
		t.Errorf("done upload was re-queued because of the destination mtime: %q", states["a.txt"])
	}
	if states["b.txt"] != string(transfer.FilePending) {
		t.Errorf("expected b.txt pending, got %q", states["b.txt"])
	}
}

func TestHandlePushStart_SecondPushAfterCompleteUploadsNothing(t *testing.T) {
	destDir, srcDir := setupPushHandlerTest(t, "additive", nil, map[string]string{
		"a.txt": "hello",
		"b.txt": "world",
	})

	srcManifest, err := diff.BuildManifest(srcDir)
	if err != nil {
		t.Fatalf("building source manifest: %v", err)
	}

	// Server already holds both files at receive-time mtimes.
	for _, f := range srcManifest.Files {
		content, err := os.ReadFile(filepath.Join(srcDir, f.Path))
		if err != nil {
			t.Fatalf("reading source %s: %v", f.Path, err)
		}
		destPath := filepath.Join(destDir, f.Path)
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			t.Fatalf("writing destination %s: %v", f.Path, err)
		}
		destTime := time.Unix(f.ModTime+5000, 0)
		if err := os.Chtimes(destPath, destTime, destTime); err != nil {
			t.Fatalf("setting destination mtime: %v", err)
		}
	}

	paths := make([]string, 0, len(srcManifest.Files))
	for _, f := range srcManifest.Files {
		paths = append(paths, f.Path)
	}
	sess := transfer.NewSessionWithDirection("phone1", "PhoneMusic", config.DirectionPush, paths)
	sess.SetSourceFiles(transfer.StateFilesFromManifest(srcManifest))
	for _, f := range srcManifest.Files {
		hash, err := transfer.ComputeFileHash(filepath.Join(srcDir, f.Path))
		if err != nil {
			t.Fatalf("hashing %s: %v", f.Path, err)
		}
		sess.SetFileMeta(f.Path, f.Size, f.ModTime)
		sess.MarkDone(f.Path, hash)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	completeBody, _ := json.Marshal(map[string]string{"session": sess.Path})
	completeReq := httptest.NewRequest(http.MethodPost, "/push/complete", bytes.NewReader(completeBody))
	completeW := httptest.NewRecorder()
	handlePushComplete()(completeW, completeReq)
	if completeW.Code != http.StatusOK {
		t.Fatalf("complete returned %d: %s", completeW.Code, completeW.Body.String())
	}

	state, err := transfer.LoadState("phone1", "PhoneMusic")
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if state == nil || len(state.Files) != 2 {
		t.Fatalf("expected 2 baseline entries, got %+v", state)
	}

	second := pushStart(t, srcDir)
	if len(second.Files) != 0 {
		t.Errorf("expected no uploads on an unchanged second push, got %+v", second.Files)
	}
}

func TestHandlePushComplete_EmptySourceClearsBaseline(t *testing.T) {
	_, _ = setupPushHandlerTest(t, "additive", map[string]string{"keep.txt": "keep"}, nil)

	if err := transfer.SaveState(&transfer.SyncState{
		DeviceID:  "phone1",
		Profile:   "PhoneMusic",
		Direction: config.DirectionPush,
		Files: []transfer.StateFile{
			{Path: "keep.txt", Size: 4, ModTime: 1},
		},
	}); err != nil {
		t.Fatalf("saving previous state: %v", err)
	}

	// A known-empty source snapshot must be distinguishable from "unknown".
	sess := transfer.NewSessionWithDirection("phone1", "PhoneMusic", config.DirectionPush, nil)
	sess.SetSourceFiles([]transfer.StateFile{})
	if err := sess.Save(); err != nil {
		t.Fatalf("saving session: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"session": sess.Path})
	req := httptest.NewRequest(http.MethodPost, "/push/complete", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handlePushComplete()(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete returned %d: %s", w.Code, w.Body.String())
	}

	state, err := transfer.LoadState("phone1", "PhoneMusic")
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if state == nil || len(state.Files) != 0 {
		t.Fatalf("expected an empty baseline for an empty source, got %+v", state)
	}
}
