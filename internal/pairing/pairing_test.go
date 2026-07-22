package pairing

import (
	"testing"
	"time"

	"presync/internal/config"
)

func TestNewSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	session, err := NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if len(session.Token) != 64 {
		t.Errorf("expected 64-char token, got %d chars", len(session.Token))
	}

	if len(session.PIN) != 4 {
		t.Errorf("expected 4-digit PIN, got %q", session.PIN)
	}

	if session.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}

	if session.Accepted {
		t.Error("expected Accepted to be false")
	}
}

func TestGetSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	DiscardSession()

	if GetSession() != nil {
		t.Error("expected nil session before creation")
	}

	NewSession()

	session := GetSession()
	if session == nil {
		t.Fatal("expected session after creation")
	}

	DiscardSession()
}

func TestSessionExpiry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	NewSession()

	// Manually expire the session
	mu.Lock()
	current.CreatedAt = time.Now().Add(-sessionTTL - time.Second)
	mu.Unlock()

	if GetSession() != nil {
		t.Error("expected nil session after expiry")
	}
}

func TestConcurrentSessionRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	_, err := NewSession()
	if err != nil {
		t.Fatalf("first NewSession: %v", err)
	}

	_, err = NewSession()
	if err == nil {
		t.Error("expected error for second concurrent session")
	}
}

func TestAcceptSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	NewSession()

	if err := AcceptSession("my-device"); err != nil {
		t.Fatalf("AcceptSession: %v", err)
	}

	session := GetSession()
	if !session.Accepted {
		t.Error("expected Accepted to be true")
	}
	if session.DeviceName != "my-device" {
		t.Errorf("expected device name 'my-device', got %q", session.DeviceName)
	}
}

func TestConfirmSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	DiscardSession()
	NewSession()

	// Confirm without accept should fail
	_, _, _, err := ConfirmSession()
	if err == nil {
		t.Error("expected error when confirming without accept")
	}

	AcceptSession("test-phone")

	token, deviceID, _, err := ConfirmSession()
	if err != nil {
		t.Fatalf("ConfirmSession: %v", err)
	}

	if len(token) != 64 {
		t.Errorf("expected 64-char long-lived token, got %d chars", len(token))
	}

	if len(deviceID) == 0 {
		t.Error("expected non-empty device ID")
	}

	// Session should be cleared
	if GetSession() != nil {
		t.Error("expected nil session after confirm")
	}

	// Device should be persisted
	dev, ok := config.ValidateToken(token)
	if !ok {
		t.Fatal("expected device to be persisted")
	}
	if dev.Name != "test-phone" {
		t.Errorf("expected device name 'test-phone', got %q", dev.Name)
	}
}

func TestDiscardSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	NewSession()
	DiscardSession()

	if GetSession() != nil {
		t.Error("expected nil session after discard")
	}
}

func TestDerivePINConsistent(t *testing.T) {
	token := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	pin1 := derivePIN(token)
	pin2 := derivePIN(token)

	if pin1 != pin2 {
		t.Errorf("PIN not consistent: %q != %q", pin1, pin2)
	}

	if len(pin1) != 4 {
		t.Errorf("expected 4-digit PIN, got %q", pin1)
	}
}
