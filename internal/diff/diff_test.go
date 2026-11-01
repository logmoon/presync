package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestComputeDelta_EmptyManifests(t *testing.T) {
	src := Manifest{}
	dst := Manifest{}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToAdd) != 0 || len(delta.ToUpdate) != 0 || len(delta.ToDelete) != 0 {
		t.Errorf("expected empty delta, got add=%d update=%d delete=%d",
			len(delta.ToAdd), len(delta.ToUpdate), len(delta.ToDelete))
	}
}

func TestComputeDelta_FileOnlyInSrc(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToAdd) != 1 {
		t.Fatalf("expected 1 add, got %d", len(delta.ToAdd))
	}
	if delta.ToAdd[0].Path != "a.txt" || delta.ToAdd[0].Reason != "add" {
		t.Errorf("expected add for a.txt, got %+v", delta.ToAdd[0])
	}
}

func TestComputeDelta_FileOnlyInDst_MirrorMode(t *testing.T) {
	src := Manifest{}
	dst := Manifest{Files: []FileEntry{{Path: "b.txt", Size: 50, ModTime: 2000}}}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToDelete) != 1 {
		t.Fatalf("expected 1 delete, got %d", len(delta.ToDelete))
	}
	if delta.ToDelete[0].Path != "b.txt" || delta.ToDelete[0].Reason != "delete" {
		t.Errorf("expected delete for b.txt, got %+v", delta.ToDelete[0])
	}
}

func TestComputeDelta_FileOnlyInDst_AdditiveMode(t *testing.T) {
	src := Manifest{}
	dst := Manifest{Files: []FileEntry{{Path: "b.txt", Size: 50, ModTime: 2000}}}

	delta := ComputeDelta(src, dst, "additive")

	if len(delta.ToDelete) != 0 {
		t.Errorf("expected 0 deletes in additive mode, got %d", len(delta.ToDelete))
	}
}

func TestComputeDelta_FileSizeDiffers(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "c.txt", Size: 200, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{{Path: "c.txt", Size: 100, ModTime: 1000}}}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToUpdate) != 1 {
		t.Fatalf("expected 1 update, got %d", len(delta.ToUpdate))
	}
	if delta.ToUpdate[0].Path != "c.txt" || delta.ToUpdate[0].Reason != "update" {
		t.Errorf("expected update for c.txt, got %+v", delta.ToUpdate[0])
	}
}

func TestComputeDelta_FileModTimeDiffers(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "d.txt", Size: 100, ModTime: 2000}}}
	dst := Manifest{Files: []FileEntry{{Path: "d.txt", Size: 100, ModTime: 1000}}}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToUpdate) != 1 {
		t.Fatalf("expected 1 update, got %d", len(delta.ToUpdate))
	}
	if delta.ToUpdate[0].Path != "d.txt" {
		t.Errorf("expected update for d.txt, got %+v", delta.ToUpdate[0])
	}
}

func TestComputeDelta_FilesIdentical(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000}}}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToAdd) != 0 || len(delta.ToUpdate) != 0 || len(delta.ToDelete) != 0 {
		t.Errorf("expected empty delta for identical files, got add=%d update=%d delete=%d",
			len(delta.ToAdd), len(delta.ToUpdate), len(delta.ToDelete))
	}
}

func TestComputeDelta_HashFallback(t *testing.T) {
	// Same size + same mtime, but both manifests carry hashes: the hash
	// comparison is the fallback that catches silently-changed content.
	src := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000, SHA256: "aaaa"}}}
	dst := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000, SHA256: "bbbb"}}}

	delta := ComputeDelta(src, dst, "mirror")

	if len(delta.ToUpdate) != 1 {
		t.Fatalf("expected 1 update from hash mismatch, got %d", len(delta.ToUpdate))
	}
	if delta.ToUpdate[0].Path != "e.txt" {
		t.Errorf("expected update for e.txt, got %q", delta.ToUpdate[0].Path)
	}

	// Matching hashes: no update
	src2 := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000, SHA256: "aaaa"}}}
	dst2 := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000, SHA256: "aaaa"}}}
	delta2 := ComputeDelta(src2, dst2, "mirror")
	if len(delta2.ToUpdate) != 0 {
		t.Errorf("expected 0 updates for matching hashes, got %d", len(delta2.ToUpdate))
	}

	// Hash on only one side: metadata comparison governs (no fallback)
	src3 := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000, SHA256: "aaaa"}}}
	dst3 := Manifest{Files: []FileEntry{{Path: "e.txt", Size: 100, ModTime: 1000}}}
	delta3 := ComputeDelta(src3, dst3, "mirror")
	if len(delta3.ToUpdate) != 0 {
		t.Errorf("expected 0 updates when only one side has a hash, got %d", len(delta3.ToUpdate))
	}
}

func TestBuildManifest_SkipsSymlinksAndTempFiles(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatalf("writing real file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "leftover.presync-tmp"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}

	// Symlink to a file and a symlink to a directory must both be skipped
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("creating subdir: %v", err)
	}
	if err := os.Symlink(sub, filepath.Join(root, "dirlink")); err != nil {
		t.Fatalf("creating dir symlink: %v", err)
	}

	manifest, err := BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	if len(manifest.Files) != 1 {
		t.Fatalf("expected exactly 1 file (real.txt), got %d: %+v", len(manifest.Files), manifest.Files)
	}
	if manifest.Files[0].Path != "real.txt" {
		t.Errorf("expected real.txt, got %q", manifest.Files[0].Path)
	}
}

func TestComputeDelta_MixedActions(t *testing.T) {
	src := Manifest{Files: []FileEntry{
		{Path: "add.txt", Size: 10, ModTime: 100},
		{Path: "same.txt", Size: 20, ModTime: 200},
		{Path: "update.txt", Size: 30, ModTime: 300},
	}}
	dst := Manifest{Files: []FileEntry{
		{Path: "same.txt", Size: 20, ModTime: 200},
		{Path: "update.txt", Size: 99, ModTime: 300},
		{Path: "delete.txt", Size: 40, ModTime: 400},
	}}

	delta := ComputeDelta(src, dst, "mirror")

	paths := func(actions []FileAction) []string {
		var out []string
		for _, a := range actions {
			out = append(out, a.Path)
		}
		sort.Strings(out)
		return out
	}

	addPaths := paths(delta.ToAdd)
	if len(addPaths) != 1 || addPaths[0] != "add.txt" {
		t.Errorf("expected adds [add.txt], got %v", addPaths)
	}

	updatePaths := paths(delta.ToUpdate)
	if len(updatePaths) != 1 || updatePaths[0] != "update.txt" {
		t.Errorf("expected updates [update.txt], got %v", updatePaths)
	}

	deletePaths := paths(delta.ToDelete)
	if len(deletePaths) != 1 || deletePaths[0] != "delete.txt" {
		t.Errorf("expected deletes [delete.txt], got %v", deletePaths)
	}
}

func TestComputeDelta_AdditiveModeNeverDeletes(t *testing.T) {
	src := Manifest{Files: []FileEntry{
		{Path: "a.txt", Size: 10, ModTime: 100},
	}}
	dst := Manifest{Files: []FileEntry{
		{Path: "b.txt", Size: 20, ModTime: 200},
		{Path: "c.txt", Size: 30, ModTime: 300},
	}}

	delta := ComputeDelta(src, dst, "additive")

	if len(delta.ToDelete) != 0 {
		t.Errorf("expected 0 deletes in additive mode, got %d", len(delta.ToDelete))
	}
	if len(delta.ToAdd) != 1 {
		t.Errorf("expected 1 add in additive mode, got %d", len(delta.ToAdd))
	}
}

func TestComputeHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hashme.txt")
	content := []byte("hello presync")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := ComputeHash(path)
	if err != nil {
		t.Fatalf("ComputeHash failed: %v", err)
	}

	expected := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expected[:])

	if hash != expectedHex {
		t.Errorf("expected hash %s, got %s", expectedHex, hash)
	}
}

func TestComputeHash_NonexistentFile(t *testing.T) {
	_, err := ComputeHash("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestBuildManifest(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "a.txt", []byte("aaa"))
	writeFile(t, dir, "sub/b.txt", []byte("bb"))
	writeFile(t, dir, "c.txt", []byte("ccc"))

	manifest, err := BuildManifest(dir)
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}

	if manifest.Root != dir {
		t.Errorf("expected root %s, got %s", dir, manifest.Root)
	}

	if len(manifest.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(manifest.Files))
	}

	paths := []string{manifest.Files[0].Path, manifest.Files[1].Path, manifest.Files[2].Path}
	expectedPaths := []string{"a.txt", "c.txt", "sub/b.txt"}
	for i, p := range paths {
		if p != expectedPaths[i] {
			t.Errorf("expected file[%d] path %s, got %s", i, expectedPaths[i], p)
		}
	}

	if manifest.Files[0].Size != 3 {
		t.Errorf("expected a.txt size 3, got %d", manifest.Files[0].Size)
	}
	if manifest.Files[1].Size != 3 {
		t.Errorf("expected c.txt size 3, got %d", manifest.Files[1].Size)
	}
	if manifest.Files[2].Size != 2 {
		t.Errorf("expected sub/b.txt size 2, got %d", manifest.Files[2].Size)
	}

	if manifest.BuiltAt == 0 {
		t.Error("expected BuiltAt to be set")
	}
}

func TestBuildManifest_NonexistentRoot(t *testing.T) {
	_, err := BuildManifest("/nonexistent/directory")
	if err == nil {
		t.Error("expected error for nonexistent root, got nil")
	}
}

func TestBuildManifest_FileAsRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := BuildManifest(path)
	if err == nil {
		t.Error("expected error when root is a file, got nil")
	}
}

func TestBuildManifest_SkipsEmptyDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "real.txt", []byte("data"))

	manifest, err := BuildManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(manifest.Files) != 1 {
		t.Errorf("expected 1 file (empty dirs skipped), got %d", len(manifest.Files))
	}
}

func TestBuildManifest_SortOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "z.txt", []byte("1"))
	writeFile(t, dir, "m.txt", []byte("2"))
	writeFile(t, dir, "a.txt", []byte("3"))

	manifest, err := BuildManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	paths := make([]string, len(manifest.Files))
	for i, f := range manifest.Files {
		paths[i] = f.Path
	}

	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)

	for i := range paths {
		if paths[i] != sorted[i] {
			t.Errorf("files not sorted: got %v, expected %v", paths, sorted)
			break
		}
	}
}

func TestEndToEnd_BuildAndDiff(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	writeFile(t, srcDir, "keep.txt", []byte("same"))
	writeFile(t, srcDir, "change.txt", []byte("old content"))
	writeFile(t, srcDir, "new.txt", []byte("new"))

	writeFile(t, dstDir, "keep.txt", []byte("same"))
	writeFile(t, dstDir, "change.txt", []byte("different content"))
	writeFile(t, dstDir, "extra.txt", []byte("to be deleted"))

	// Touch change.txt in dst to differ in modtime
	time.Sleep(1100 * time.Millisecond)
	writeFile(t, dstDir, "change.txt", []byte("different content"))

	srcManifest, err := BuildManifest(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dstManifest, err := BuildManifest(dstDir)
	if err != nil {
		t.Fatal(err)
	}

	delta := ComputeDelta(srcManifest, dstManifest, "mirror")

	if len(delta.ToAdd) != 1 || delta.ToAdd[0].Path != "new.txt" {
		t.Errorf("expected add [new.txt], got %v", delta.ToAdd)
	}
	if len(delta.ToUpdate) != 1 || delta.ToUpdate[0].Path != "change.txt" {
		t.Errorf("expected update [change.txt], got %v", delta.ToUpdate)
	}
	if len(delta.ToDelete) != 1 || delta.ToDelete[0].Path != "extra.txt" {
		t.Errorf("expected delete [extra.txt], got %v", delta.ToDelete)
	}
}

func TestComputeDeltaWithBase_UnchangedSourceIgnoresDestinationMTime(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	// Android SAF cannot preserve mtimes: the recipient's copy was written now.
	dst := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 9999}}}

	delta := ComputeDeltaWithBase(src, dst, base, "mirror")

	if len(delta.ToAdd) != 0 || len(delta.ToUpdate) != 0 || len(delta.ToDelete) != 0 {
		t.Errorf("expected unchanged file to be skipped, got add=%d update=%d delete=%d",
			len(delta.ToAdd), len(delta.ToUpdate), len(delta.ToDelete))
	}
}

func TestComputeDeltaWithBase_NewFileIsAdded(t *testing.T) {
	src := Manifest{Files: []FileEntry{
		{Path: "a.txt", Size: 100, ModTime: 1000},
		{Path: "new.txt", Size: 50, ModTime: 2000},
	}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 9999}}}

	delta := ComputeDeltaWithBase(src, dst, base, "mirror")

	if len(delta.ToAdd) != 1 || delta.ToAdd[0].Path != "new.txt" {
		t.Fatalf("expected add [new.txt], got %v", delta.ToAdd)
	}
	if len(delta.ToUpdate) != 0 {
		t.Errorf("expected no updates, got %v", delta.ToUpdate)
	}
}

func TestComputeDeltaWithBase_SourceChangedIsUpdated(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 101, ModTime: 1001}}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 9999}}}

	delta := ComputeDeltaWithBase(src, dst, base, "mirror")

	if len(delta.ToUpdate) != 1 || delta.ToUpdate[0].Path != "a.txt" {
		t.Fatalf("expected update [a.txt], got %v", delta.ToUpdate)
	}
}

func TestComputeDeltaWithBase_MissingAtDestinationIsUpdated(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{}

	delta := ComputeDeltaWithBase(src, dst, base, "mirror")

	if len(delta.ToUpdate) != 1 || delta.ToUpdate[0].Path != "a.txt" {
		t.Fatalf("expected update [a.txt], got %v", delta.ToUpdate)
	}
}

func TestComputeDeltaWithBase_SizeMismatchAtDestinationIsUpdated(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 42, ModTime: 1000}}}

	delta := ComputeDeltaWithBase(src, dst, base, "mirror")

	if len(delta.ToUpdate) != 1 || delta.ToUpdate[0].Path != "a.txt" {
		t.Fatalf("expected update [a.txt], got %v", delta.ToUpdate)
	}
}

func TestComputeDeltaWithBase_MirrorDeletes(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{
		{Path: "a.txt", Size: 100, ModTime: 9999},
		{Path: "gone.txt", Size: 10, ModTime: 9999},
	}}

	delta := ComputeDeltaWithBase(src, dst, base, "mirror")

	if len(delta.ToDelete) != 1 || delta.ToDelete[0].Path != "gone.txt" {
		t.Fatalf("expected delete [gone.txt], got %v", delta.ToDelete)
	}
}

func TestComputeDeltaWithBase_AdditiveNeverDeletes(t *testing.T) {
	src := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	base := Manifest{Files: []FileEntry{{Path: "a.txt", Size: 100, ModTime: 1000}}}
	dst := Manifest{Files: []FileEntry{
		{Path: "a.txt", Size: 100, ModTime: 9999},
		{Path: "gone.txt", Size: 10, ModTime: 9999},
	}}

	delta := ComputeDeltaWithBase(src, dst, base, "additive")

	if len(delta.ToDelete) != 0 {
		t.Errorf("expected no deletes in additive mode, got %v", delta.ToDelete)
	}
}

func writeFile(t *testing.T, dir, relPath string, content []byte) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
