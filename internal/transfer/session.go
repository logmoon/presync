package transfer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"presync/internal/config"
)

type FileState string

const (
	FilePending    FileState = "pending"
	FileInProgress FileState = "in_progress"
	FileDone       FileState = "done"
	FileError      FileState = "error"
)

const (
	MaxRetries           = 3
	staleSessionAge      = 7 * 24 * time.Hour
	activeSessionTimeout = 30 * time.Minute
)

type FileSession struct {
	Path     string    `json:"path"`
	State    FileState `json:"state"`
	Offset   int64     `json:"offset"`
	Attempts int       `json:"attempts"`
	SHA256   string    `json:"sha256,omitempty"`
	// Size and ModTime describe the source file this session entry was created
	// from. They let a resume tell "source changed" from "recipient cannot
	// preserve mtimes" (Android SAF writes a fresh mtime).
	Size    int64 `json:"size,omitempty"`
	ModTime int64 `json:"mod_time,omitempty"`
}

type Session struct {
	DeviceID  string        `json:"device_id"`
	Profile   string        `json:"profile"`
	Direction string        `json:"direction"`
	Files     []FileSession `json:"files"`
	// SourceFiles is the client-reported source manifest for push sessions. It
	// lets the server persist a last-synced baseline at completion without
	// needing a second manifest from the client.
	SourceFiles []StateFile `json:"source_files"`
	ToDelete    []string    `json:"to_delete,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	LastUpdated time.Time   `json:"last_updated"`
	Path        string      `json:"-"`
	mu          sync.Mutex
}

type sessionFile struct {
	DeviceID    string        `json:"device_id"`
	Profile     string        `json:"profile"`
	Direction   string        `json:"direction,omitempty"`
	Files       []FileSession `json:"files"`
	SourceFiles []StateFile   `json:"source_files"`
	ToDelete    []string      `json:"to_delete,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	LastUpdated time.Time     `json:"last_updated"`
}

func NewSession(deviceID, profile string, filePaths []string) *Session {
	return NewSessionWithDirection(deviceID, profile, config.DirectionPull, filePaths)
}

func NewSessionWithDirection(deviceID, profile, direction string, filePaths []string) *Session {
	files := make([]FileSession, len(filePaths))
	for i, p := range filePaths {
		files[i] = FileSession{
			Path:  p,
			State: FilePending,
		}
	}

	now := time.Now()
	return &Session{
		DeviceID:    deviceID,
		Profile:     profile,
		Direction:   direction,
		Files:       files,
		CreatedAt:   now,
		LastUpdated: now,
		Path:        sessionPath(deviceID, profile),
	}
}

func sessionPath(deviceID, profile string) string {
	// Nanosecond timestamp + sanitized identifiers: two sessions for the same
	// device+profile can never collide, and the identifiers can never escape
	// the sessions directory.
	ts := time.Now().UTC().Format("20060102T150405.000000000Z")
	name := fmt.Sprintf("%s-%s-%s.json", sanitizeName(deviceID), sanitizeName(profile), ts)
	return filepath.Join(config.SessionsDir(), name)
}

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

// ResolveSession maps a client-supplied session identifier to a path strictly
// inside SessionsDir. Only the basename is used, so a caller cannot read or
// write session files outside the sessions directory.
func ResolveSession(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("session is required")
	}
	base := filepath.Base(name)
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("invalid session")
	}
	return filepath.Join(config.SessionsDir(), base), nil
}

func LoadSession(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parsing session file: %w", err)
	}

	direction := sf.Direction
	if direction == "" {
		direction = config.DirectionPull
	}

	return &Session{
		DeviceID:    sf.DeviceID,
		Profile:     sf.Profile,
		Direction:   direction,
		Files:       sf.Files,
		SourceFiles: sf.SourceFiles,
		ToDelete:    sf.ToDelete,
		CreatedAt:   sf.CreatedAt,
		LastUpdated: sf.LastUpdated,
		Path:        path,
	}, nil
}

func (s *Session) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.LastUpdated = time.Now().UTC()

	sf := sessionFile{
		DeviceID:    s.DeviceID,
		Profile:     s.Profile,
		Direction:   s.Direction,
		Files:       s.Files,
		SourceFiles: s.SourceFiles,
		ToDelete:    s.ToDelete,
		CreatedAt:   s.CreatedAt,
		LastUpdated: s.LastUpdated,
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	tmpPath := s.Path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing session temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.Path); err != nil {
		return fmt.Errorf("renaming session file: %w", err)
	}

	return nil
}

func (s *Session) MarkInProgress(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			s.Files[i].State = FileInProgress
			return
		}
	}
}

func (s *Session) MarkDone(path, sha256 string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			s.Files[i].State = FileDone
			s.Files[i].SHA256 = sha256
			return
		}
	}
}

func (s *Session) MarkError(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			s.Files[i].Attempts++
			if s.Files[i].Attempts >= MaxRetries {
				s.Files[i].State = FileError
			} else {
				s.Files[i].State = FilePending
			}
			return
		}
	}
}

func (s *Session) UpdateOffset(path string, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			s.Files[i].Offset = offset
			return
		}
	}
}

func (s *Session) RemoveFile(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]FileSession, 0, len(s.Files))
	for _, f := range s.Files {
		if f.Path != path {
			filtered = append(filtered, f)
		}
	}
	s.Files = filtered
}

// UpsertFile marks a file that the current delta says must be uploaded. It adds
// the file when missing, and resets any prior state (including a stale done
// entry whose source changed or whose destination was removed) so the upload
// happens again.
func (s *Session) UpsertFile(path string, size, modTime int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path != path {
			continue
		}
		s.Files[i].State = FilePending
		s.Files[i].Offset = 0
		s.Files[i].Attempts = 0
		s.Files[i].SHA256 = ""
		s.Files[i].Size = size
		s.Files[i].ModTime = modTime
		return
	}

	s.Files = append(s.Files, FileSession{
		Path:    path,
		State:   FilePending,
		Size:    size,
		ModTime: modTime,
	})
}

// SetFileMeta records the source metadata for an existing file entry without
// disturbing its state, offset, or hash.
func (s *Session) SetFileMeta(path string, size, modTime int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			s.Files[i].Size = size
			s.Files[i].ModTime = modTime
			return
		}
	}
}

// SetSourceFiles replaces the client-reported source manifest snapshot (push).
func (s *Session) SetSourceFiles(files []StateFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SourceFiles = files
}

// SourceFileSnapshot returns a copy of the stored push source snapshot.
func (s *Session) SourceFileSnapshot() []StateFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SourceFiles == nil {
		return nil
	}
	result := make([]StateFile, len(s.SourceFiles))
	copy(result, s.SourceFiles)
	return result
}

func (s *Session) SetFileSize(path string, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			s.Files[i].Size = size
			return
		}
	}
}

func (s *Session) SetPlannedDeletes(paths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ToDelete = paths
}

func (s *Session) PlannedDeletes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, len(s.ToDelete))
	copy(result, s.ToDelete)
	return result
}

func (s *Session) PendingFiles() []FileSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []FileSession
	for _, f := range s.Files {
		// in_progress files are still work remaining — a resume log that shows
		// "pending=0" while a file is mid-transfer is misleading.
		if f.State == FilePending || f.State == FileInProgress {
			result = append(result, f)
		}
	}
	return result
}

func (s *Session) InProgressFile() *FileSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].State == FileInProgress {
			return &s.Files[i]
		}
	}
	return nil
}

func (s *Session) GetFile(path string) *FileSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Files {
		if s.Files[i].Path == path {
			return &s.Files[i]
		}
	}
	return nil
}

func (s *Session) IsComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, f := range s.Files {
		if f.State != FileDone {
			return false
		}
	}
	return true
}

func (s *Session) IsStale() bool {
	if s.LastUpdated.IsZero() {
		return time.Since(s.CreatedAt) > activeSessionTimeout
	}
	return time.Since(s.LastUpdated) > activeSessionTimeout
}

func (s *Session) HasErrors() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, f := range s.Files {
		if f.State == FileError {
			return true
		}
	}
	return false
}

func FindSessionForDevice(deviceID, profile string) (*Session, error) {
	dir := config.SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	prefix := deviceID + "-" + profile + "-"
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		name := e.Name()
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			matches = append(matches, filepath.Join(dir, name))
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	sort.Strings(matches)
	latest := matches[len(matches)-1]

	return LoadSession(latest)
}

// FindSessionForDeviceDirection returns the newest session for a device+profile
// only when it matches the requested direction. Sessions created before the
// direction field existed are treated as pull sessions.
func FindSessionForDeviceDirection(deviceID, profile, direction string) (*Session, error) {
	sess, err := FindSessionForDevice(deviceID, profile)
	if err != nil || sess == nil {
		return sess, err
	}
	if sess.Direction != direction {
		return nil, nil
	}
	return sess, nil
}

func PruneStaleSessions() error {
	dir := config.SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading sessions directory: %w", err)
	}

	cutoff := time.Now().Add(-staleSessionAge)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}

		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
	}

	return nil
}

func DeleteSession(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting session file: %w", err)
	}
	return nil
}

// DeleteSessionsForDevice removes every session file belonging to a device,
// used when the device is unpaired.
func DeleteSessionsForDevice(deviceID string) error {
	dir := config.SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading sessions directory: %w", err)
	}

	prefix := sanitizeName(deviceID) + "-"
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("deleting session %s: %w", e.Name(), err)
			}
		}
	}
	return nil
}
