package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeFileHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello world")
	os.WriteFile(path, content, 0o644)

	hash, err := ComputeFileHash(path)
	if err != nil {
		t.Fatalf("ComputeFileHash: %v", err)
	}

	h := sha256.New()
	h.Write(content)
	expected := hex.EncodeToString(h.Sum(nil))

	if hash != expected {
		t.Errorf("expected %q, got %q", expected, hash)
	}
}

func TestComputeFileHash_NotFound(t *testing.T) {
	_, err := ComputeFileHash("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestSendFile_FullContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	content := bytes.Repeat([]byte("x"), ChunkSize*3+100)
	os.WriteFile(path, content, 0o644)

	w := httptest.NewRecorder()
	hash, err := SendFile(w, path, 0)
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if len(w.Body.Bytes()) != len(content) {
		t.Errorf("expected %d bytes, got %d", len(content), len(w.Body.Bytes()))
	}

	h := sha256.New()
	h.Write(content)
	expected := hex.EncodeToString(h.Sum(nil))
	if hash != expected {
		t.Errorf("hash mismatch: expected %q, got %q", expected, hash)
	}
}

func TestSendFile_WithOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	content := bytes.Repeat([]byte("y"), ChunkSize*2+50)
	os.WriteFile(path, content, 0o644)

	offset := int64(ChunkSize)
	w := httptest.NewRecorder()
	hash, err := SendFile(w, path, offset)
	if err != nil {
		t.Fatalf("SendFile with offset: %v", err)
	}

	expectedLen := len(content) - int(offset)
	if len(w.Body.Bytes()) != expectedLen {
		t.Errorf("expected %d bytes, got %d", expectedLen, len(w.Body.Bytes()))
	}

	h := sha256.New()
	h.Write(content)
	expected := hex.EncodeToString(h.Sum(nil))
	if hash != expected {
		t.Errorf("hash should be of full file, expected %q, got %q", expected, hash)
	}
}

func TestSendFile_NotFound(t *testing.T) {
	_, err := SendFile(httptest.NewRecorder(), "/nonexistent/file.bin", 0)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestReceiveFile(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "received.txt")
	content := []byte("test data for receive")

	req := httptest.NewRequest(http.MethodPost, "/sync/file", bytes.NewReader(content))

	hash, total, err := ReceiveFile(req, destPath)
	if err != nil {
		t.Fatalf("ReceiveFile: %v", err)
	}

	if total != int64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), total)
	}

	h := sha256.New()
	h.Write(content)
	expected := hex.EncodeToString(h.Sum(nil))
	if hash != expected {
		t.Errorf("hash mismatch: expected %q, got %q", expected, hash)
	}

	tmpPath := destPath + ".presync-tmp"
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("reading temp file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Error("temp file content mismatch")
	}
}

func TestReceiveFile_LargeContent(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "large.bin")
	content := bytes.Repeat([]byte("z"), ChunkSize*3+77)

	req := httptest.NewRequest(http.MethodPost, "/sync/file", bytes.NewReader(content))

	hash, total, err := ReceiveFile(req, destPath)
	if err != nil {
		t.Fatalf("ReceiveFile large: %v", err)
	}

	if total != int64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), total)
	}

	h := sha256.New()
	h.Write(content)
	expected := hex.EncodeToString(h.Sum(nil))
	if hash != expected {
		t.Errorf("hash mismatch: expected %q, got %q", expected, hash)
	}
}

func TestAtomicRename(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "file.txt.tmp")
	realPath := filepath.Join(dir, "file.txt")
	content := []byte("final content")

	os.WriteFile(tmpPath, content, 0o644)

	if err := AtomicRename(tmpPath, realPath); err != nil {
		t.Fatalf("AtomicRename: %v", err)
	}

	data, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("reading renamed file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Error("renamed file content mismatch")
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should not exist after rename")
	}
}

func TestChunkSize(t *testing.T) {
	if ChunkSize != 256*1024 {
		t.Errorf("expected ChunkSize 256KB, got %d", ChunkSize)
	}
}

func TestSendFile_ExactChunkBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.bin")
	content := bytes.Repeat([]byte("a"), ChunkSize)
	os.WriteFile(path, content, 0o644)

	w := httptest.NewRecorder()
	hash, err := SendFile(w, path, 0)
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	if len(w.Body.Bytes()) != ChunkSize {
		t.Errorf("expected %d bytes, got %d", ChunkSize, len(w.Body.Bytes()))
	}

	h := sha256.New()
	h.Write(content)
	expected := hex.EncodeToString(h.Sum(nil))
	if hash != expected {
		t.Errorf("hash mismatch")
	}
}

func TestReceiveFile_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "empty.txt")

	req := httptest.NewRequest(http.MethodPost, "/sync/file", strings.NewReader(""))

	hash, total, err := ReceiveFile(req, destPath)
	if err != nil {
		t.Fatalf("ReceiveFile empty: %v", err)
	}

	if total != 0 {
		t.Errorf("expected 0 bytes, got %d", total)
	}

	h := sha256.New()
	expected := hex.EncodeToString(h.Sum(nil))
	if hash != expected {
		t.Errorf("expected hash of empty content, got %q", hash)
	}
}
