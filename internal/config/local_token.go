package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const localTokenFileName = "local-token"

func LocalTokenPath() string {
	return filepath.Join(ConfigDir(), localTokenFileName)
}

func HasLocalToken() bool {
	_, err := os.Stat(LocalTokenPath())
	return err == nil
}

// validLocalToken rejects any file content that is not a full 64-char hex
// token — a truncated file from a crash mid-write must never be accepted as
// the local auth secret.
func validLocalToken(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// acquireLocalTokenLock serializes token creation across processes. The lock
// is an exclusive-create file; a stale lock (crashed process) is broken after
// the grace period. The critical section is a few file writes.
func acquireLocalTokenLock(path string) (func(), error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(3 * time.Second)

	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating local token lock: %w", err)
		}

		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 10*time.Second {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for local token lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// GetLocalToken returns the local CLI token, creating it atomically on first
// use. Creation is serialized with a lock file and published with an atomic
// rename, so concurrent callers (daemon and CLI are separate processes) always
// end up with the same complete token. A corrupt/truncated file is healed.
func GetLocalToken() (string, error) {
	path := LocalTokenPath()

	// Fast path: existing valid token
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); validLocalToken(token) {
			return token, nil
		}
	}

	release, err := acquireLocalTokenLock(path)
	if err != nil {
		return "", err
	}
	defer release()

	// Another process may have created the token while we waited for the lock.
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); validLocalToken(token) {
			return token, nil
		}
	}

	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating local token: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), localTokenFileName+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("creating local token temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(token); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing local token temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing local token temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return "", fmt.Errorf("setting local token permissions: %w", err)
	}

	// Atomic publication: readers see either no file, the old corrupt file,
	// or the complete new token — never a partially written one.
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("publishing local token: %w", err)
	}

	return token, nil
}
