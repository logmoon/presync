package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"presync/internal/config"
)

type pairAcceptRequest struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

type pairAcceptResponse struct {
	Status string `json:"status"`
	PIN    string `json:"pin"`
}

type profilesResponse struct {
	Profiles []string `json:"profiles"`
}

// Connect pairs with a server or reconnects to a stored one, then syncs. It
// returns the address of the server it used so callers can start the watcher.
func Connect(pairingURL, destOverride string, fresh bool, progress func(path string, done bool, err error)) (string, error) {
	if pairingURL != "" {
		// With a token it is a pairing URL; without one it selects a stored
		// server to reconnect to.
		if addr, _, err := parsePairingURL(pairingURL); err == nil {
			_, err := doPairing(pairingURL, destOverride, fresh, progress)
			return addr, err
		}
		addr := normalizeAddr(pairingURL)
		if _, err := reconnectAndSync(fresh, progress, addr); err != nil {
			return "", err
		}
		return addr, nil
	}
	return reconnectAndSync(fresh, progress, "")
}

func ensureHTTP(addr string) string {
	if len(addr) > 7 && addr[:7] == "http://" {
		return addr
	}
	return "http://" + addr
}

// normalizeAddr strips any scheme so stored addresses are plain host:port.
func normalizeAddr(addr string) string {
	for _, scheme := range []string{"http://", "https://"} {
		if len(addr) > len(scheme) && addr[:len(scheme)] == scheme {
			return addr[len(scheme):]
		}
	}
	return addr
}

// ForgetServer removes a stored server entry. With an empty address the first
// entry is removed (the one reconnect would use); otherwise the entry with a
// matching address.
func ForgetServer(address string) error {
	sf, err := config.LoadServers()
	if err != nil {
		return fmt.Errorf("loading servers: %w", err)
	}
	if len(sf.Servers) == 0 {
		return fmt.Errorf("no paired server to forget")
	}

	if address == "" {
		if err := config.RemoveServer(sf.Servers[0].Address); err != nil {
			return fmt.Errorf("forgetting server: %w", err)
		}
		fmt.Printf("Forgot server %s\n", sf.Servers[0].Address)
		return nil
	}

	addr := normalizeAddr(address)
	if err := config.RemoveServer(addr); err != nil {
		return fmt.Errorf("forgetting server %s: %w", addr, err)
	}
	fmt.Printf("Forgot server %s\n", addr)
	return nil
}

func doPairing(pairingURL, destOverride string, fresh bool, progress func(path string, done bool, err error)) (string, error) {
	serverAddr, sessionToken, err := parsePairingURL(pairingURL)
	if err != nil {
		return "", fmt.Errorf("parsing pairing URL: %w", err)
	}

	fmt.Printf("Connecting to %s...\n", serverAddr)

	deviceName, _ := os.Hostname()
	if deviceName == "" {
		deviceName = "presync-client"
	}

	pin, err := pairAccept(serverAddr, sessionToken, deviceName)
	if err != nil {
		return "", fmt.Errorf("pairing accept: %w", err)
	}

	fmt.Printf("PIN: %s\n", pin)
	fmt.Printf("Verify this matches the server PIN, then confirm on the server.\n")
	fmt.Println("Waiting for server to confirm pairing...")

	deviceID, longLivedToken, destinations, err := pollPairConfirmed(serverAddr, sessionToken, 60*time.Second)
	if err != nil {
		return "", fmt.Errorf("pairing confirm: %w", err)
	}

	fmt.Println("Paired successfully!")

	if len(destinations) == 0 {
		profiles, err := fetchProfiles(serverAddr, longLivedToken)
		if err != nil {
			return "", fmt.Errorf("fetching profiles: %w", err)
		}

		if len(profiles) == 0 {
			fmt.Println("No profiles configured on server.")
			return serverAddr, nil
		}

		if destOverride != "" {
			// Client-side destination chosen on the command line
			destinations = make(map[string]string)
			for _, name := range profiles {
				destinations[name] = filepath.Join(destOverride, name)
			}
		} else {
			fmt.Print("No server-side destination configured.\nWhere should files be stored? ")
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			baseDir := strings.TrimSpace(input)

			if baseDir == "" {
				return "", fmt.Errorf("no destination specified")
			}

			destinations = make(map[string]string)
			for _, name := range profiles {
				destinations[name] = filepath.Join(baseDir, name)
			}
		}
	}

	entry := config.ServerEntry{
		Address:  serverAddr,
		Token:    longLivedToken,
		DeviceID: deviceID,
		Name:     deviceName,
	}
	for name, path := range destinations {
		entry.Profiles = append(entry.Profiles, config.ServerProfile{
			Name:        name,
			Destination: path,
		})
	}

	if err := config.EnsureDirs(); err != nil {
		return "", fmt.Errorf("preparing client config directory: %w", err)
	}

	if err := config.SaveServer(entry); err != nil {
		return "", fmt.Errorf("saving server: %w", err)
	}

	fmt.Printf("Server saved. Syncing %d profile(s)...\n\n", len(destinations))

	var totalSynced, totalErrors int
	for name, destDir := range destinations {
		fmt.Printf("[%s] → %s\n", name, destDir)

		result, err := SyncProfile(ensureHTTP(serverAddr), longLivedToken, name, deviceID, destDir, fresh, progress)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  sync failed: %v\n", err)
			totalErrors++
			continue
		}

		totalSynced += result.FilesSynced
		totalErrors += result.Errors

		if result.FilesSynced == 0 && result.Errors == 0 {
			fmt.Println("  already up to date")
		}
		fmt.Println()
	}

	fmt.Printf("Sync complete. %d file(s) synced", totalSynced)
	if totalErrors > 0 {
		fmt.Printf(", %d error(s)", totalErrors)
	}
	fmt.Println()

	return serverAddr, nil
}

func reconnectAndSync(fresh bool, progress func(path string, done bool, err error), address string) (string, error) {
	sf, err := config.LoadServers()
	if err != nil {
		return "", fmt.Errorf("loading servers: %w", err)
	}

	if len(sf.Servers) == 0 {
		return "", fmt.Errorf("no paired server — run 'presync connect <pairing-url>' first")
	}

	var entry *config.ServerEntry
	if address != "" {
		for i := range sf.Servers {
			if sf.Servers[i].Address == address {
				entry = &sf.Servers[i]
				break
			}
		}
		if entry == nil {
			return "", fmt.Errorf("no stored pairing for server %q — run 'presync connect <pairing-url>' first", address)
		}
	} else {
		entry = &sf.Servers[0]
	}

	serverAddr := entry.Address
	token := entry.Token
	deviceID := entry.DeviceID

	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://%s/health", serverAddr))
	if err != nil {
		return "", fmt.Errorf("server not reachable at %s: %w", serverAddr, err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	fmt.Printf("Connected to %s\n", serverAddr)

	profiles, err := fetchProfiles(serverAddr, token)
	if err != nil {
		return "", fmt.Errorf("fetching profiles: %w", err)
	}

	if len(profiles) == 0 {
		fmt.Println("No profiles configured on server.")
		return serverAddr, nil
	}

	fmt.Printf("Syncing %d profile(s)...\n\n", len(profiles))

	var totalSynced, totalErrors int
	for _, name := range profiles {
		var destDir string
		for _, p := range entry.Profiles {
			if p.Name == name {
				destDir = p.Destination
				break
			}
		}
		if destDir == "" {
			fmt.Fprintf(os.Stderr, "  skipping %s: no destination configured\n", name)
			continue
		}

		fmt.Printf("[%s] → %s\n", name, destDir)

		result, err := SyncProfile(ensureHTTP(serverAddr), token, name, deviceID, destDir, fresh, progress)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  sync failed: %v\n", err)
			totalErrors++
			continue
		}

		totalSynced += result.FilesSynced
		totalErrors += result.Errors

		if result.FilesSynced == 0 && result.Errors == 0 {
			fmt.Println("  already up to date")
		}
		fmt.Println()
	}

	fmt.Printf("Sync complete. %d file(s) synced", totalSynced)
	if totalErrors > 0 {
		fmt.Printf(", %d error(s)", totalErrors)
	}
	fmt.Println()

	return serverAddr, nil
}

func parsePairingURL(raw string) (serverAddr, sessionToken string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}

	// Only http/https pairing URLs are accepted. Anything else (file://,
	// javascript:, ...) is a phishing or injection vector.
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("invalid pairing URL scheme %q: must be http or https", u.Scheme)
	}

	host := u.Host
	if host == "" {
		return "", "", fmt.Errorf("no host in URL")
	}

	token := u.Query().Get("token")
	if token == "" {
		return "", "", fmt.Errorf("no token in URL")
	}

	if !strings.Contains(host, ":") {
		host = host + ":7700"
	}

	return host, token, nil
}

func fetchProfiles(serverAddr, token string) ([]string, error) {
	c := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/profiles", serverAddr), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("X-Presync-Token", token)

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("profiles request failed (%d): %s", resp.StatusCode, string(body))
	}

	var result profilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding profiles: %w", err)
	}

	return result.Profiles, nil
}

func pairAccept(serverAddr, sessionToken, deviceName string) (pin string, err error) {
	c := &http.Client{Timeout: 5 * time.Second}

	body, _ := json.Marshal(pairAcceptRequest{
		Token: sessionToken,
		Name:  deviceName,
	})

	resp, err := c.Post(
		fmt.Sprintf("http://%s/pair/accept", serverAddr),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("pair accept failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var result pairAcceptResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding accept response: %w", err)
	}

	return result.PIN, nil
}

type pairConfirmedResponse struct {
	Confirmed    bool              `json:"confirmed"`
	DeviceID     string            `json:"device_id"`
	Token        string            `json:"token"`
	Destinations map[string]string `json:"destinations,omitempty"`
}

func pollPairConfirmed(serverAddr, sessionToken string, timeout time.Duration) (deviceID, longLivedToken string, destinations map[string]string, err error) {
	c := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)

		resp, err := c.Get(fmt.Sprintf("http://%s/pair/confirmed?token=%s", serverAddr, url.QueryEscape(sessionToken)))
		if err != nil {
			continue
		}

		// Non-OK responses are real failures (rejected session, server error),
		// not "not yet confirmed" — surface them instead of masking as a timeout.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return "", "", nil, fmt.Errorf("pairing status request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var result pairConfirmedResponse
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if decodeErr := json.Unmarshal(body, &result); decodeErr != nil {
			continue
		}

		if result.Confirmed {
			return result.DeviceID, result.Token, result.Destinations, nil
		}
	}

	return "", "", nil, fmt.Errorf("timed out waiting for server to confirm pairing")
}

func pairDiscard(serverAddr, sessionToken string) {
	c := &http.Client{Timeout: 3 * time.Second}
	c.Post(
		fmt.Sprintf("http://%s/pair/discard", serverAddr),
		"application/json",
		bytes.NewReader(nil),
	)
}
