package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const localTokenFileName = "local-token"

func LocalTokenPath() string {
	return filepath.Join(ConfigDir(), localTokenFileName)
}

func HasLocalToken() bool {
	_, err := os.Stat(LocalTokenPath())
	return err == nil
}

func GetLocalToken() (string, error) {
	path := LocalTokenPath()

	data, err := os.ReadFile(path)
	if err == nil {
		token := string(data)
		if len(token) > 0 {
			return token, nil
		}
	}

	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating local token: %w", err)
	}

	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("writing local token: %w", err)
	}

	return token, nil
}
