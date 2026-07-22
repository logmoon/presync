package pairing

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"sync"
	"time"

	"presync/internal/config"
)

const (
	sessionTTL = 60 * time.Second
	pinLength  = 4
)

type Session struct {
	Token        string
	PIN          string
	CreatedAt    time.Time
	Accepted     bool
	DeviceName   string
	Destinations map[string]string
}

var (
	mu      sync.Mutex
	current *Session

	// Last confirmed pairing result, stored briefly so the web UI and PC client
	// can retrieve the token after the CLI confirms (they don't call /pair/confirm).
	lastConfirmedDeviceID     string
	lastConfirmedToken        string
	lastConfirmedDestinations map[string]string
	lastConfirmedAt           time.Time
)

func NewSession() (*Session, error) {
	mu.Lock()
	defer mu.Unlock()

	if current != nil && time.Since(current.CreatedAt) < sessionTTL {
		return nil, fmt.Errorf("a pairing session is already active (wait %ds or restart daemon)",
			int(time.Until(current.CreatedAt.Add(sessionTTL)).Seconds()))
	}

	token, err := config.GenerateToken()
	if err != nil {
		return nil, fmt.Errorf("generating session token: %w", err)
	}

	pin := derivePIN(token)

	current = &Session{
		Token:     token,
		PIN:       pin,
		CreatedAt: time.Now(),
	}

	return current, nil
}

func GetSession() *Session {
	mu.Lock()
	defer mu.Unlock()

	if current == nil || time.Since(current.CreatedAt) >= sessionTTL {
		return nil
	}

	return current
}

func AcceptSession(deviceName string) error {
	mu.Lock()
	defer mu.Unlock()

	if current == nil || time.Since(current.CreatedAt) >= sessionTTL {
		return fmt.Errorf("no active pairing session or session expired")
	}

	current.Accepted = true
	current.DeviceName = deviceName
	return nil
}

func ConfirmSession() (token string, deviceID string, destinations map[string]string, err error) {
	mu.Lock()
	defer mu.Unlock()

	if current == nil || time.Since(current.CreatedAt) >= sessionTTL {
		return "", "", nil, fmt.Errorf("no active pairing session or session expired")
	}

	if !current.Accepted {
		return "", "", nil, fmt.Errorf("device has not yet connected — wait for the client to scan the QR code")
	}

	token, err = config.GenerateToken()
	if err != nil {
		return "", "", nil, fmt.Errorf("generating long-lived token: %w", err)
	}

	name := current.DeviceName
	if name == "" {
		name = "unknown"
	}

	destinations = current.Destinations

	dev, err := config.AddDevice(name, token)
	if err != nil {
		return "", "", nil, fmt.Errorf("adding device: %w", err)
	}

	// Store confirmation result briefly so the web UI and PC client can retrieve it
	lastConfirmedDeviceID = dev.ID
	lastConfirmedToken = token
	lastConfirmedDestinations = destinations
	lastConfirmedAt = time.Now()

	current = nil
	return token, dev.ID, destinations, nil
}

func DiscardSession() {
	mu.Lock()
	defer mu.Unlock()
	current = nil
}

func SetDestinations(destinations map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		current.Destinations = destinations
	}
}

func IsExpired() bool {
	mu.Lock()
	defer mu.Unlock()
	return current == nil || time.Since(current.CreatedAt) >= sessionTTL
}

// GetLastConfirmed returns the device ID, token, and destinations from the most recent
// confirmation, if it happened within the last 30 seconds. Returns empty
// values if no recent confirmation exists.
func GetLastConfirmed() (deviceID, token string, destinations map[string]string, ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if lastConfirmedToken == "" || time.Since(lastConfirmedAt) > 30*time.Second {
		return "", "", nil, false
	}
	return lastConfirmedDeviceID, lastConfirmedToken, lastConfirmedDestinations, true
}

func derivePIN(token string) string {
	h := sha256.Sum256([]byte(token))
	num := uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3])
	pin := num % 10000
	return fmt.Sprintf("%0*s", pinLength, strconv.FormatUint(uint64(pin), 10))
}
