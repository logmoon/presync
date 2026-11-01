package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AutoSyncConfig is the server's auto-sync eligibility: which pull profiles
// clients may sync automatically and the minimum time between triggers.
type AutoSyncConfig struct {
	CooldownMinutes int      `json:"cooldown_minutes"`
	Profiles        []string `json:"profiles"`
}

// FetchAutoSyncConfig asks the server which profiles are eligible for
// auto-sync. The server address may be plain host:port or include a scheme.
func FetchAutoSyncConfig(serverAddr, token string) (AutoSyncConfig, error) {
	c := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequest("GET", ensureHTTP(serverAddr)+"/auto-sync", nil)
	if err != nil {
		return AutoSyncConfig{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("X-Presync-Token", token)

	resp, err := c.Do(req)
	if err != nil {
		return AutoSyncConfig{}, fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return AutoSyncConfig{}, fmt.Errorf("auto-sync request failed (%d): %s", resp.StatusCode, string(body))
	}

	var result AutoSyncConfig
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return AutoSyncConfig{}, fmt.Errorf("decoding auto-sync config: %w", err)
	}

	return result, nil
}
