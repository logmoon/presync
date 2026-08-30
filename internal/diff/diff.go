package diff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type FileEntry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	SHA256  string `json:"sha256,omitempty"`
}

type Manifest struct {
	Root    string      `json:"root"`
	Files   []FileEntry `json:"files"`
	BuiltAt int64       `json:"built_at"`
}

type FileAction struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Delta struct {
	ToAdd    []FileAction `json:"to_add"`
	ToUpdate []FileAction `json:"to_update"`
	ToDelete []FileAction `json:"to_delete"`
}

func BuildManifest(root string) (Manifest, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolving root path: %w", err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return Manifest{}, fmt.Errorf("stating root directory: %w", err)
	}
	if !info.IsDir() {
		return Manifest{}, fmt.Errorf("root path %s is not a directory", absRoot)
	}

	var files []FileEntry

	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		// Skip symlinks: a symlink-to-dir would otherwise be recorded as a
		// regular file and fail during transfer (EISDIR). A symlink-to-file
		// would be re-transferred on every sync (its size is the link target
		// path length). Neither is a file we can sync meaningfully.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		// Skip transfer staging files — they are never part of the real tree.
		if filepath.Ext(path) == ".presync-tmp" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stating file %s: %w", path, err)
		}

		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			return fmt.Errorf("computing relative path for %s: %w", path, err)
		}

		files = append(files, FileEntry{
			Path:    relPath,
			Size:    info.Size(),
			ModTime: info.ModTime().Truncate(time.Second).Unix(),
		})

		return nil
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("walking directory %s: %w", absRoot, err)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	return Manifest{
		Root:    absRoot,
		Files:   files,
		BuiltAt: time.Now().Truncate(time.Second).Unix(),
	}, nil
}

func ComputeHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening file for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing file: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func ComputeDelta(src, dst Manifest, mode string) Delta {
	srcIndex := make(map[string]FileEntry, len(src.Files))
	for _, f := range src.Files {
		srcIndex[f.Path] = f
	}

	dstIndex := make(map[string]FileEntry, len(dst.Files))
	for _, f := range dst.Files {
		dstIndex[f.Path] = f
	}

	var delta Delta

	for _, srcFile := range src.Files {
		dstFile, exists := dstIndex[srcFile.Path]
		if !exists {
			delta.ToAdd = append(delta.ToAdd, FileAction{
				Path:   srcFile.Path,
				Reason: "add",
			})
			continue
		}

		if srcFile.Size != dstFile.Size || srcFile.ModTime != dstFile.ModTime {
			delta.ToUpdate = append(delta.ToUpdate, FileAction{
				Path:   srcFile.Path,
				Reason: "update",
			})
			continue
		}

		// Size + mtime match, but both sides carry hashes: compare content.
		// This is the SHA-256 fallback for files whose metadata cannot
		// distinguish a change (same size, same-second mtime).
		if srcFile.SHA256 != "" && dstFile.SHA256 != "" && srcFile.SHA256 != dstFile.SHA256 {
			delta.ToUpdate = append(delta.ToUpdate, FileAction{
				Path:   srcFile.Path,
				Reason: "update",
			})
		}
	}

	if mode == "mirror" {
		for _, dstFile := range dst.Files {
			if _, exists := srcIndex[dstFile.Path]; !exists {
				delta.ToDelete = append(delta.ToDelete, FileAction{
					Path:   dstFile.Path,
					Reason: "delete",
				})
			}
		}
	}

	return delta
}
