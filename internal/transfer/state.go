package transfer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"presync/internal/config"
	"presync/internal/diff"
)

// StateFile is one entry in a device's last-synced baseline.
type StateFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	SHA256  string `json:"sha256,omitempty"`
}

// SyncState records what the server last delivered to (or received from) a
// device for a profile. Deltas compare the source against this baseline
// instead of the recipient's mtimes, which Android SAF cannot preserve.
type SyncState struct {
	DeviceID  string      `json:"device_id"`
	Profile   string      `json:"profile"`
	Direction string      `json:"direction"`
	Files     []StateFile `json:"files"`
	UpdatedAt time.Time   `json:"updated_at"`
}

func (s *SyncState) Manifest() diff.Manifest {
	files := make([]diff.FileEntry, len(s.Files))
	for i, f := range s.Files {
		files[i] = diff.FileEntry{
			Path:    f.Path,
			Size:    f.Size,
			ModTime: f.ModTime,
			SHA256:  f.SHA256,
		}
	}
	return diff.Manifest{Files: files}
}

func statePath(deviceID, profile string) string {
	name := fmt.Sprintf("%s-%s.json", sanitizeName(deviceID), sanitizeName(profile))
	return filepath.Join(config.StateDir(), name)
}

// LoadState returns the stored baseline, or (nil, nil) when none exists.
func LoadState(deviceID, profile string) (*SyncState, error) {
	data, err := os.ReadFile(statePath(deviceID, profile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sync state: %w", err)
	}

	var st SyncState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing sync state: %w", err)
	}
	return &st, nil
}

func SaveState(st *SyncState) error {
	if st == nil {
		return fmt.Errorf("sync state is nil")
	}

	st.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling sync state: %w", err)
	}

	path := statePath(st.DeviceID, st.Profile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing sync state temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming sync state file: %w", err)
	}
	return nil
}

// DeleteStateForDevice removes every baseline belonging to a device, used when
// the device is unpaired.
func DeleteStateForDevice(deviceID string) error {
	dir := config.StateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading state directory: %w", err)
	}

	prefix := sanitizeName(deviceID) + "-"
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < len(prefix) || e.Name()[:len(prefix)] != prefix {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("deleting sync state %s: %w", e.Name(), err)
		}
	}
	return nil
}

// BuildCompletionState merges the previous baseline and this session's
// completed files into the new baseline.
//
// A file present in the session uses the metadata recorded when it was served
// or uploaded (what the recipient actually has). A file not in the session
// keeps its previous baseline entry. sourcePaths, when non-nil, restricts the
// result to files that still exist at the source; nil means the source shape
// is unknown and every known entry is kept.
func BuildCompletionState(deviceID, profile, direction string, prev *SyncState, files []FileSession, sourcePaths map[string]bool) *SyncState {
	prevIndex := make(map[string]StateFile, 0)
	if prev != nil {
		for _, f := range prev.Files {
			prevIndex[f.Path] = f
		}
	}

	sessionIndex := make(map[string]FileSession, 0)
	for _, f := range files {
		if f.State == FileDone {
			sessionIndex[f.Path] = f
		}
	}

	st := &SyncState{
		DeviceID:  deviceID,
		Profile:   profile,
		Direction: direction,
	}

	seen := make(map[string]bool, len(prevIndex)+len(sessionIndex))
	add := func(path string, sf StateFile) {
		if sourcePaths != nil && !sourcePaths[path] {
			return
		}
		if seen[path] {
			return
		}
		seen[path] = true
		st.Files = append(st.Files, sf)
	}

	for path, p := range prevIndex {
		if sf, ok := sessionIndex[path]; ok {
			add(path, StateFile{Path: path, Size: sf.Size, ModTime: sf.ModTime, SHA256: sf.SHA256})
			continue
		}
		add(path, p)
	}
	for path, sf := range sessionIndex {
		if _, ok := prevIndex[path]; ok {
			continue
		}
		add(path, StateFile{Path: path, Size: sf.Size, ModTime: sf.ModTime, SHA256: sf.SHA256})
	}

	sort.Slice(st.Files, func(i, j int) bool {
		return st.Files[i].Path < st.Files[j].Path
	})
	return st
}

// StateFilesFromManifest converts a manifest into baseline entries.
func StateFilesFromManifest(m diff.Manifest) []StateFile {
	files := make([]StateFile, 0, len(m.Files))
	for _, f := range m.Files {
		files = append(files, StateFile{
			Path:    f.Path,
			Size:    f.Size,
			ModTime: f.ModTime,
			SHA256:  f.SHA256,
		})
	}
	return files
}
