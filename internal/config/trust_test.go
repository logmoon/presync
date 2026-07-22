package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemoveDevice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	dev, err := AddDevice("test-device", "abc123")
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	if dev.Name != "test-device" {
		t.Errorf("expected name 'test-device', got %q", dev.Name)
	}
	if dev.Token != "abc123" {
		t.Errorf("expected token 'abc123', got %q", dev.Token)
	}
	if dev.ID == "" {
		t.Error("expected non-empty device ID")
	}

	df, err := LoadDevices()
	if err != nil {
		t.Fatalf("LoadDevices: %v", err)
	}
	if len(df.Devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(df.Devices))
	}

	if err := RemoveDevice(dev.ID); err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}

	df, err = LoadDevices()
	if err != nil {
		t.Fatalf("LoadDevices after remove: %v", err)
	}
	if len(df.Devices) != 0 {
		t.Fatalf("expected 0 devices after remove, got %d", len(df.Devices))
	}
}

func TestValidateToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	_, err := AddDevice("my-phone", "token-abc")
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	dev, ok := ValidateToken("token-abc")
	if !ok {
		t.Fatal("expected ValidateToken to succeed")
	}
	if dev.Name != "my-phone" {
		t.Errorf("expected name 'my-phone', got %q", dev.Name)
	}

	_, ok = ValidateToken("wrong-token")
	if ok {
		t.Error("expected ValidateToken to fail for wrong token")
	}
}

func TestRemoveDeviceNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	err := RemoveDevice("nonexistent")
	if err == nil {
		t.Error("expected error when removing nonexistent device")
	}
}

func TestLoadDevicesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	df, err := LoadDevices()
	if err != nil {
		t.Fatalf("LoadDevices: %v", err)
	}
	if len(df.Devices) != 0 {
		t.Fatalf("expected 0 devices from empty file, got %d", len(df.Devices))
	}
}

func TestDevicesFilePersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESYNC_CONFIG_DIR", dir)

	AddDevice("device-a", "token-a")
	AddDevice("device-b", "token-b")

	data, err := os.ReadFile(filepath.Join(dir, devicesFileName))
	if err != nil {
		t.Fatalf("reading devices.json: %v", err)
	}

	if len(data) == 0 {
		t.Error("devices.json is empty")
	}

	df, err := LoadDevices()
	if err != nil {
		t.Fatalf("LoadDevices: %v", err)
	}
	if len(df.Devices) != 2 {
		t.Errorf("expected 2 devices, got %d", len(df.Devices))
	}
}
