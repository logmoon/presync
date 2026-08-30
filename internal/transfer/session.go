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
}

type Session struct {
	DeviceID    string        `json:"device_id"`
	Profile     string        `json:"profile"`
	Files       []FileSession `json:"files"`
	CreatedAt   time.Time     `json:"created_at"`
	LastUpdated time.Time     `json:"last_updated"`
	Path        string        `json:"-"`
	mu          sync.Mutex
}

type sessionFile struct {
	DeviceID    string        `json:"device_id"`
	Profile     string        `json:"profile"`
	Files       []FileSession `json:"files"`
	CreatedAt   time.Time     `json:"created_at"`
	LastUpdated time.Time     `json:"last_updated"`
}

func NewSession(deviceID, profile string, filePaths []string) *Session {
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

	return &Session{
		DeviceID:    sf.DeviceID,
		Profile:     sf.Profile,
		Files:       sf.Files,
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
		Files:       s.Files,
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
