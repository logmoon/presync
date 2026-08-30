package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// GetLocalToken returns the local CLI token, creating it atomically on first
// use. The O_CREATE|O_EXCL create guarantees that concurrent callers (daemon
// and CLI are separate processes) never overwrite each other: the loser of the
// race re-reads the winner's token. A corrupt/truncated file is healed by
// regeneration.
func GetLocalToken() (string, error) {
	path := LocalTokenPath()

	// Fast path: existing valid token
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); validLocalToken(token) {
			return token, nil
		}
	}

	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating local token: %w", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				// Lost the creation race — use the winner's token, unless it is
				// corrupt (truncated/empty), in which case heal and retry.
				data, rerr := os.ReadFile(path)
				if rerr == nil {
					if winner := strings.TrimSpace(string(data)); validLocalToken(winner) {
						return winner, nil
					}
				}
				if rerr := os.Remove(path); rerr != nil {
					return "", fmt.Errorf("removing corrupt local token: %w", rerr)
				}
				continue
			}
			return "", fmt.Errorf("creating local token: %w", err)
		}

		if _, werr := f.Write([]byte(token)); werr != nil {
			f.Close()
			os.Remove(path)
			return "", fmt.Errorf("writing local token: %w", werr)
		}
		if cerr := f.Close(); cerr != nil {
			os.Remove(path)
			return "", fmt.Errorf("closing local token: %w", cerr)
		}

		return token, nil
	}

	return "", fmt.Errorf("unable to create local token after retries")
}
