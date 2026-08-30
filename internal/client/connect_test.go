package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"presync/internal/config"
)

func TestParsePairingURL_Valid(t *testing.T) {
	addr, token, err := parsePairingURL("http://192.168.1.5:7700/pair?token=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "192.168.1.5:7700" {
		t.Errorf("expected 192.168.1.5:7700, got %s", addr)
	}
	if token != "abc123" {
		t.Errorf("expected abc123, got %s", token)
	}
}

func TestParsePairingURL_NoPort(t *testing.T) {
	addr, token, err := parsePairingURL("http://192.168.1.5/pair?token=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "192.168.1.5:7700" {
		t.Errorf("expected 192.168.1.5:7700, got %s", addr)
	}
	if token != "abc123" {
		t.Errorf("expected abc123, got %s", token)
	}
}

func TestParsePairingURL_NoToken(t *testing.T) {
	_, _, err := parsePairingURL("http://192.168.1.5:7700/pair")
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParsePairingURL_InvalidURL(t *testing.T) {
	_, _, err := parsePairingURL("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestParsePairingURL_EmptyString(t *testing.T) {
	_, _, err := parsePairingURL("")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestPairAccept_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pair/accept" {
			t.Errorf("expected /pair/accept, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req pairAcceptRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Token != "session123" {
			t.Errorf("expected token session123, got %s", req.Token)
		}
		if req.Name != "test-host" {
			t.Errorf("expected name test-host, got %s", req.Name)
		}

		json.NewEncoder(w).Encode(pairAcceptResponse{
			Status: "accepted",
			PIN:    "1234",
		})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	pin, err := pairAccept(addr, "session123", "test-host")
	if err != nil {
		t.Fatal(err)
	}
	if pin != "1234" {
		t.Errorf("expected PIN 1234, got %s", pin)
	}
}

func TestPairAccept_Rejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"token mismatch"}`))
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	_, err := pairAccept(addr, "bad-token", "test")
	if err == nil {
		t.Fatal("expected error for rejected pairing")
	}
}

func TestFetchProfiles_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/profiles" {
			t.Errorf("expected /profiles, got %s", r.URL.Path)
		}
		token := r.Header.Get("X-Presync-Token")
		if token != "mytoken" {
			t.Errorf("expected token mytoken, got %s", token)
		}

		json.NewEncoder(w).Encode(profilesResponse{
			Profiles: []string{"Music", "Podcasts"},
		})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	profiles, err := fetchProfiles(addr, "mytoken")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	if profiles[0] != "Music" || profiles[1] != "Podcasts" {
		t.Errorf("expected [Music Podcasts], got %v", profiles)
	}
}

func TestFetchProfiles_AuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	_, err := fetchProfiles(addr, "bad-token")
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
}

func TestFetchProfiles_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(profilesResponse{Profiles: []string{}})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	profiles, err := fetchProfiles(addr, "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected empty profiles, got %v", profiles)
	}
}

func TestPollPairConfirmed_Success(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pair/confirmed" {
			t.Errorf("expected /pair/confirmed, got %s", r.URL.Path)
		}
		callCount++
		if callCount < 3 {
			json.NewEncoder(w).Encode(pairConfirmedResponse{Confirmed: false})
			return
		}
		json.NewEncoder(w).Encode(pairConfirmedResponse{
			Confirmed:    true,
			DeviceID:     "deadbeef",
			Token:        "token123",
			Destinations: map[string]string{"Music": "/tmp/Music"},
		})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	deviceID, token, dests, err := pollPairConfirmed(addr, "sess-token", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if deviceID != "deadbeef" {
		t.Errorf("expected device ID deadbeef, got %s", deviceID)
	}
	if token != "token123" {
		t.Errorf("expected token token123, got %s", token)
	}
	if dests["Music"] != "/tmp/Music" {
		t.Errorf("expected destination /tmp/Music, got %v", dests)
	}
	if callCount < 3 {
		t.Errorf("expected at least 3 polls, got %d", callCount)
	}
}

func TestPollPairConfirmed_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(pairConfirmedResponse{Confirmed: false})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	_, _, _, err := pollPairConfirmed(addr, "sess-token", 1*time.Second)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestReconnectAndSync_NoServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	err := reconnectAndSync(false, nil, "")
	if err == nil {
		t.Fatal("expected error for no servers")
	}
}

func TestReconnectAndSync_ServerDown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	config.SaveServer(config.ServerEntry{
		Address:  "127.0.0.1:19999",
		Token:    "token",
		DeviceID: "device1",
		Profiles: []config.ServerProfile{{Name: "Music", Destination: "/tmp/music"}},
	})

	err := reconnectAndSync(false, nil, "")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestReconnectAndSync_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/profiles":
			json.NewEncoder(w).Encode(profilesResponse{Profiles: []string{"Music"}})
		case "/sync":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"session": "test-session",
				"files":   []interface{}{},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	config.SaveServer(config.ServerEntry{
		Address:  addr,
		Token:    "testtoken",
		DeviceID: "device1",
		Profiles: []config.ServerProfile{{Name: "Music", Destination: t.TempDir()}},
	})

	err := reconnectAndSync(false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
}
