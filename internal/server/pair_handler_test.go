package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"presync/internal/config"
	"presync/internal/pairing"
)

func TestHandlePairStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	handler := handlePairStart()
	req := httptest.NewRequest(http.MethodPost, "/pair", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp pairResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if len(resp.Token) != 64 {
		t.Errorf("expected 64-char token, got %d chars", len(resp.Token))
	}
	if len(resp.PIN) != 4 {
		t.Errorf("expected 4-digit PIN, got %q", resp.PIN)
	}

	pairing.DiscardSession()
}

func TestHandlePairStartMethodNotAllowed(t *testing.T) {
	handler := handlePairStart()
	req := httptest.NewRequest(http.MethodGet, "/pair", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandlePairAccept(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	session, _ := pairing.NewSession()

	handler := handlePairAccept()
	body, _ := json.Marshal(pairAcceptRequest{
		Token: session.Token,
		Name:  "test-device",
	})
	req := httptest.NewRequest(http.MethodPost, "/pair/accept", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	pairing.DiscardSession()
}

func TestHandlePairAcceptTokenMismatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	pairing.NewSession()

	handler := handlePairAccept()
	body, _ := json.Marshal(pairAcceptRequest{
		Token: "wrong-token",
		Name:  "test-device",
	})
	req := httptest.NewRequest(http.MethodPost, "/pair/accept", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}

	pairing.DiscardSession()
}

func TestHandlePairConfirm(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	pairing.NewSession()
	pairing.AcceptSession("confirm-device")

	handler := handlePairConfirm()
	req := httptest.NewRequest(http.MethodPost, "/pair/confirm", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp pairConfirmResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if resp.DeviceID == "" {
		t.Error("expected non-empty device ID")
	}
	if len(resp.LongLivedToken) != 64 {
		t.Errorf("expected 64-char token, got %d chars", len(resp.LongLivedToken))
	}
}

func TestHandlePairConfirmWithoutAccept(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	pairing.NewSession()

	handler := handlePairConfirm()
	req := httptest.NewRequest(http.MethodPost, "/pair/confirm", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	pairing.DiscardSession()
}

func TestHandlePairStatusNoSession(t *testing.T) {
	handler := handlePairStatus()
	req := httptest.NewRequest(http.MethodGet, "/pair/status", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["active"] != false {
		t.Error("expected active=false")
	}
}

func TestHandlePairStatusActiveSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	pairing.NewSession()

	handler := handlePairStatus()
	req := httptest.NewRequest(http.MethodGet, "/pair/status", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["active"] != true {
		t.Error("expected active=true")
	}

	pairing.DiscardSession()
}

func TestWithAuthMissingToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestWithAuthInvalidToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("X-Presync-Token", "invalid-token")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestWithAuthValidToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	config.AddDevice("auth-test", "valid-token-123")

	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("X-Presync-Token", "valid-token-123")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
