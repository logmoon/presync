package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupServersTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadServers_NoFile(t *testing.T) {
	setupServersTest(t)
	sf, err := LoadServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Servers) != 0 {
		t.Fatalf("expected empty servers list, got %d", len(sf.Servers))
	}
}

func TestSaveAndGetServer(t *testing.T) {
	setupServersTest(t)

	entry := ServerEntry{
		Address:  "192.168.1.5:7700",
		Token:    "abc123",
		DeviceID: "deadbeef",
		Profiles: []ServerProfile{
			{Name: "Music", Destination: "/home/user/Music"},
		},
	}

	if err := SaveServer(entry); err != nil {
		t.Fatal(err)
	}

	got, err := GetServer("192.168.1.5:7700")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected server, got nil")
	}
	if got.Token != "abc123" {
		t.Errorf("expected token abc123, got %s", got.Token)
	}
	if got.DeviceID != "deadbeef" {
		t.Errorf("expected device ID deadbeef, got %s", got.DeviceID)
	}
	if len(got.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(got.Profiles))
	}
	if got.Profiles[0].Name != "Music" {
		t.Errorf("expected profile Music, got %s", got.Profiles[0].Name)
	}
}

func TestGetServer_NotFound(t *testing.T) {
	setupServersTest(t)
	got, err := GetServer("10.0.0.1:7700")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestSaveServer_Upsert(t *testing.T) {
	setupServersTest(t)

	entry := ServerEntry{
		Address:  "192.168.1.5:7700",
		Token:    "token1",
		DeviceID: "id1",
	}
	if err := SaveServer(entry); err != nil {
		t.Fatal(err)
	}

	entry.Token = "token2"
	entry.Profiles = []ServerProfile{{Name: "Podcasts", Destination: "/home/user/Podcasts"}}
	if err := SaveServer(entry); err != nil {
		t.Fatal(err)
	}

	got, err := GetServer("192.168.1.5:7700")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "token2" {
		t.Errorf("expected upserted token token2, got %s", got.Token)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].Name != "Podcasts" {
		t.Errorf("expected upserted profile, got %+v", got.Profiles)
	}

	sf, err := LoadServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Servers) != 1 {
		t.Fatalf("expected 1 server after upsert, got %d", len(sf.Servers))
	}
}

func TestSaveServer_Multiple(t *testing.T) {
	setupServersTest(t)

	SaveServer(ServerEntry{Address: "10.0.0.1:7700", Token: "t1", DeviceID: "d1"})
	SaveServer(ServerEntry{Address: "10.0.0.2:7700", Token: "t2", DeviceID: "d2"})

	sf, err := LoadServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(sf.Servers))
	}
}

func TestRemoveServer(t *testing.T) {
	setupServersTest(t)

	SaveServer(ServerEntry{Address: "10.0.0.1:7700", Token: "t1", DeviceID: "d1"})
	SaveServer(ServerEntry{Address: "10.0.0.2:7700", Token: "t2", DeviceID: "d2"})

	if err := RemoveServer("10.0.0.1:7700"); err != nil {
		t.Fatal(err)
	}

	sf, err := LoadServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Servers) != 1 {
		t.Fatalf("expected 1 server after remove, got %d", len(sf.Servers))
	}
	if sf.Servers[0].Address != "10.0.0.2:7700" {
		t.Errorf("expected remaining server 10.0.0.2:7700, got %s", sf.Servers[0].Address)
	}
}

func TestRemoveServer_NotFound(t *testing.T) {
	setupServersTest(t)
	err := RemoveServer("nonexistent:7700")
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}

func TestServersFileAtomicWrite(t *testing.T) {
	setupServersTest(t)

	SaveServer(ServerEntry{Address: "10.0.0.1:7700", Token: "t1", DeviceID: "d1"})

	tmpFile := ServersPath() + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Fatal("temp file should not exist after successful write")
	}

	data, err := os.ReadFile(ServersPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("servers.json should not be empty")
	}
}

func TestServersPath(t *testing.T) {
	setupServersTest(t)
	expected := filepath.Join(ConfigDir(), "servers.json")
	got := ServersPath()
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestTouchAutoSync(t *testing.T) {
	setupServersTest(t)

	entry := ServerEntry{
		Address:  "192.168.1.5:7700",
		Token:    "abc123",
		DeviceID: "deadbeef",
	}
	if err := SaveServer(entry); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := TouchAutoSync(entry.Address, at); err != nil {
		t.Fatal(err)
	}

	got, err := GetServer(entry.Address)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastAutoSyncAt.Equal(at) {
		t.Errorf("last auto sync: got %v, want %v", got.LastAutoSyncAt, at)
	}

	if err := TouchAutoSync("10.0.0.99:7700", at); err == nil {
		t.Fatal("expected error for unknown server")
	}
}
