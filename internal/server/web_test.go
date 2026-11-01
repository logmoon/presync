package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"presync/internal/config"
	"presync/internal/transfer"
)

type webSessionSummary struct {
	DeviceID  string `json:"device_id"`
	Profile   string `json:"profile"`
	Direction string `json:"direction"`
}

func TestHandleWebSessionsFiltersByDeviceAndIncludesPush(t *testing.T) {
	t.Setenv("PRESYNC_CONFIG_DIR", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	devA, err := config.AddDevice("phoneA", "tokenA")
	if err != nil {
		t.Fatalf("AddDevice A: %v", err)
	}
	devB, err := config.AddDevice("phoneB", "tokenB")
	if err != nil {
		t.Fatalf("AddDevice B: %v", err)
	}

	pullA := transfer.NewSessionWithDirection(devA.ID, "music", config.DirectionPull, []string{"a"})
	if err := pullA.Save(); err != nil {
		t.Fatalf("saving pull A: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	pushA := transfer.NewSessionWithDirection(devA.ID, "Photos", config.DirectionPush, []string{"p"})
	if err := pushA.Save(); err != nil {
		t.Fatalf("saving push A: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	pullB := transfer.NewSessionWithDirection(devB.ID, "music", config.DirectionPull, []string{"b"})
	if err := pullB.Save(); err != nil {
		t.Fatalf("saving pull B: %v", err)
	}

	get := func(token string) []webSessionSummary {
		req := httptest.NewRequest(http.MethodGet, "/web/sessions", nil)
		req.Header.Set("X-Presync-Token", token)
		w := httptest.NewRecorder()
		handleWebSessions()(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("sessions returned %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Sessions []webSessionSummary `json:"sessions"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decoding sessions: %v", err)
		}
		return resp.Sessions
	}

	deviceSessions := get("tokenA")
	if len(deviceSessions) != 2 {
		t.Fatalf("expected 2 sessions for device A (pull + push), got %+v", deviceSessions)
	}
	for _, s := range deviceSessions {
		if s.DeviceID != devA.ID {
			t.Errorf("device A view leaked another device's session: %+v", s)
		}
	}
	if deviceSessions[0].Profile != "Photos" || deviceSessions[0].Direction != config.DirectionPush {
		t.Errorf("expected newest (push) first, got %+v", deviceSessions)
	}
	if deviceSessions[1].Profile != "music" || deviceSessions[1].Direction != config.DirectionPull {
		t.Errorf("expected pull second, got %+v", deviceSessions)
	}

	localToken, err := config.GetLocalToken()
	if err != nil {
		t.Fatalf("GetLocalToken: %v", err)
	}
	localSessions := get(localToken)
	if len(localSessions) != 2 {
		t.Fatalf("expected 2 pull sessions for the local view, got %+v", localSessions)
	}
	for _, s := range localSessions {
		if s.Direction != config.DirectionPull {
			t.Errorf("local PC view must not include push sessions: %+v", s)
		}
	}
	if localSessions[0].DeviceID != devB.ID {
		t.Errorf("expected newest pull first, got %+v", localSessions)
	}
}
