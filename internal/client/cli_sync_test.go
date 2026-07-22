package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupTestFiles(t *testing.T) (sourceDir, destDir string) {
	t.Helper()

	sourceDir = t.TempDir()
	destDir = t.TempDir()

	files := map[string]string{
		"hello.txt":   "hello world",
		"nested/a.go": "package main",
		"nested/b.go": "package util",
	}

	for name, content := range files {
		path := filepath.Join(sourceDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing file: %v", err)
		}
	}

	return sourceDir, destDir
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file for hash: %v", err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

type mockSyncSession struct {
	files   []mockFile
	confirm map[string]bool
}

type mockFile struct {
	path string
	data []byte
	hash string
}

func newMockServer(t *testing.T, token string, sourceDir string, files []mockFile) *httptest.Server {
	t.Helper()

	session := &mockSyncSession{
		files:   files,
		confirm: make(map[string]bool),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Presync-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			DeviceID string `json:"device_id"`
			Profile  string `json:"profile"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		type fileInfo struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256,omitempty"`
		}
		type syncResp struct {
			Session string     `json:"session"`
			Delta   struct{}   `json:"delta"`
			Files   []fileInfo `json:"files"`
		}

		resp := syncResp{Session: "test-session"}
		for _, f := range session.files {
			resp.Files = append(resp.Files, fileInfo{
				Path:   f.path,
				Size:   int64(len(f.data)),
				SHA256: f.hash,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/sync/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Presync-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		path := r.URL.Query().Get("path")
		for _, f := range session.files {
			if f.path == path {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("X-Presync-SHA256", f.hash)
				w.Write(f.data)
				return
			}
		}
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
	})

	mux.HandleFunc("/sync/confirm", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Presync-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		var req struct {
			Session string `json:"session"`
			Path    string `json:"path"`
			SHA256  string `json:"sha256"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		for _, f := range session.files {
			if f.path == req.Path {
				matched := req.SHA256 == f.hash
				session.confirm[req.Path] = matched

				type confirmResp struct {
					OK      bool   `json:"ok"`
					Action  string `json:"action"`
					Message string `json:"message,omitempty"`
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(confirmResp{
					OK:      matched,
					Action:  "rename",
					Message: fmt.Sprintf("rename %s.presync-tmp to %s", req.Path, req.Path),
				})
				return
			}
		}
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
	})

	mux.HandleFunc("/sync/complete", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Presync-Token") != token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		type completeResp struct {
			OK bool `json:"ok"`
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(completeResp{OK: true})
	})

	return httptest.NewServer(mux)
}

func TestSyncProfile_Success(t *testing.T) {
	sourceDir, destDir := setupTestFiles(t)
	token := "test-token-123"

	files := []mockFile{
		{path: "hello.txt", data: []byte("hello world"), hash: fileHash(t, filepath.Join(sourceDir, "hello.txt"))},
		{path: "nested/a.go", data: []byte("package main"), hash: fileHash(t, filepath.Join(sourceDir, "nested/a.go"))},
		{path: "nested/b.go", data: []byte("package util"), hash: fileHash(t, filepath.Join(sourceDir, "nested/b.go"))},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	var progressCalls []string
	result, err := SyncProfile(srv.URL, token, "test-profile", "device-1", destDir, false, func(path string, done bool, err error) {
		if err != nil {
			t.Errorf("progress callback error for %s: %v", path, err)
		}
		progressCalls = append(progressCalls, path)
	})

	if err != nil {
		t.Fatalf("SyncProfile: %v", err)
	}

	if result.FilesSynced != 3 {
		t.Errorf("expected 3 files synced, got %d", result.FilesSynced)
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
	if len(progressCalls) != 3 {
		t.Errorf("expected 3 progress calls, got %d", len(progressCalls))
	}

	for _, f := range files {
		destPath := filepath.Join(destDir, f.path)
		data, err := os.ReadFile(destPath)
		if err != nil {
			t.Errorf("reading synced file %s: %v", f.path, err)
			continue
		}
		if string(data) != string(f.data) {
			t.Errorf("file %s content mismatch: got %q, want %q", f.path, string(data), string(f.data))
		}
	}
}

func TestSyncProfile_EmptyDest(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()
	token := "test-token-123"

	if err := os.WriteFile(filepath.Join(sourceDir, "only.txt"), []byte("solo"), 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	files := []mockFile{
		{path: "only.txt", data: []byte("solo"), hash: fileHash(t, filepath.Join(sourceDir, "only.txt"))},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	result, err := SyncProfile(srv.URL, token, "test-profile", "device-1", destDir, false, nil)
	if err != nil {
		t.Fatalf("SyncProfile: %v", err)
	}

	if result.FilesSynced != 1 {
		t.Errorf("expected 1 file synced, got %d", result.FilesSynced)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "only.txt"))
	if err != nil {
		t.Fatalf("reading synced file: %v", err)
	}
	if string(data) != "solo" {
		t.Errorf("content mismatch: got %q, want %q", string(data), "solo")
	}
}

func TestSyncProfile_Unauthorized(t *testing.T) {
	sourceDir, destDir := setupTestFiles(t)
	wrongToken := "wrong-token"

	files := []mockFile{
		{path: "hello.txt", data: []byte("hello world"), hash: "abc123"},
	}

	srv := newMockServer(t, "correct-token", sourceDir, files)
	defer srv.Close()

	_, err := SyncProfile(srv.URL, wrongToken, "test-profile", "device-1", destDir, false, nil)
	if err == nil {
		t.Fatal("expected error for unauthorized token")
	}
}

func TestSyncProfile_ProgressCallback(t *testing.T) {
	sourceDir, destDir := setupTestFiles(t)
	token := "test-token-123"

	files := []mockFile{
		{path: "hello.txt", data: []byte("hello world"), hash: fileHash(t, filepath.Join(sourceDir, "hello.txt"))},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	type progressEntry struct {
		path string
		done bool
	}

	var calls []progressEntry
	_, err := SyncProfile(srv.URL, token, "test-profile", "device-1", destDir, false, func(path string, done bool, err error) {
		calls = append(calls, progressEntry{path: path, done: done})
	})

	if err != nil {
		t.Fatalf("SyncProfile: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 progress call, got %d", len(calls))
	}
	if calls[0].path != "hello.txt" {
		t.Errorf("expected path hello.txt, got %s", calls[0].path)
	}
	if !calls[0].done {
		t.Error("expected done=true")
	}
}

func TestSyncProfile_NilProgress(t *testing.T) {
	sourceDir, destDir := setupTestFiles(t)
	token := "test-token-123"

	files := []mockFile{
		{path: "hello.txt", data: []byte("hello world"), hash: fileHash(t, filepath.Join(sourceDir, "hello.txt"))},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	result, err := SyncProfile(srv.URL, token, "test-profile", "device-1", destDir, false, nil)
	if err != nil {
		t.Fatalf("SyncProfile: %v", err)
	}

	if result.FilesSynced != 1 {
		t.Errorf("expected 1 file synced, got %d", result.FilesSynced)
	}
}

func TestFetchAndWriteFile_ChunkedWrite(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()
	token := "test-token-123"

	bigContent := make([]byte, 1024*1024)
	for i := range bigContent {
		bigContent[i] = byte(i % 256)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "big.bin"), bigContent, 0o644); err != nil {
		t.Fatalf("writing big file: %v", err)
	}

	h := sha256.Sum256(bigContent)
	hash := hex.EncodeToString(h[:])

	files := []mockFile{
		{path: "big.bin", data: bigContent, hash: hash},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	c := &http.Client{}
	err := fetchAndWriteFile(c, srv.URL, token, "test-session", "big.bin", destDir)
	if err != nil {
		t.Fatalf("fetchAndWriteFile: %v", err)
	}

	destData, err := os.ReadFile(filepath.Join(destDir, "big.bin"))
	if err != nil {
		t.Fatalf("reading dest file: %v", err)
	}

	destHash := sha256.Sum256(destData)
	if hex.EncodeToString(destHash[:]) != hash {
		t.Errorf("destination file hash mismatch")
	}
}

func TestFetchAndWriteFile_MkdirNested(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()
	token := "test-token-123"

	content := []byte("deep nested file")
	nestedPath := filepath.Join(sourceDir, "a/b/c/file.txt")
	if err := os.MkdirAll(filepath.Dir(nestedPath), 0o755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}
	if err := os.WriteFile(nestedPath, content, 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	h := sha256.Sum256(content)
	hash := hex.EncodeToString(h[:])

	files := []mockFile{
		{path: "a/b/c/file.txt", data: content, hash: hash},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	c := &http.Client{}
	err := fetchAndWriteFile(c, srv.URL, token, "test-session", "a/b/c/file.txt", destDir)
	if err != nil {
		t.Fatalf("fetchAndWriteFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "a/b/c/file.txt"))
	if err != nil {
		t.Fatalf("reading nested file: %v", err)
	}
	if string(data) != "deep nested file" {
		t.Errorf("content mismatch: got %q", string(data))
	}
}

func TestSyncProfile_ServerDown(t *testing.T) {
	destDir := t.TempDir()

	_, err := SyncProfile("http://127.0.0.1:1", "token", "profile", "device", destDir, false, nil)
	if err == nil {
		t.Fatal("expected error when server is down")
	}
}

func TestSyncProfile_EmptyFiles(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()
	token := "test-token-123"

	srv := newMockServer(t, token, sourceDir, nil)
	defer srv.Close()

	result, err := SyncProfile(srv.URL, token, "test-profile", "device-1", destDir, false, nil)
	if err != nil {
		t.Fatalf("SyncProfile: %v", err)
	}

	if result.FilesSynced != 0 {
		t.Errorf("expected 0 files synced, got %d", result.FilesSynced)
	}
}

func TestSyncProfile_AllFail(t *testing.T) {
	sourceDir, destDir := setupTestFiles(t)
	token := "test-token-123"

	files := []mockFile{
		{path: "hello.txt", data: []byte("hello world"), hash: "wrong-hash"},
	}

	srv := newMockServer(t, token, sourceDir, files)
	defer srv.Close()

	result, err := SyncProfile(srv.URL, token, "test-profile", "device-1", destDir, false, func(path string, done bool, err error) {
		if err == nil {
			t.Errorf("expected error for %s", path)
		}
	})

	if err != nil {
		t.Fatalf("SyncProfile should not return error for individual file failures: %v", err)
	}

	if result.FilesSynced != 0 {
		t.Errorf("expected 0 files synced, got %d", result.FilesSynced)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}
