package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const devicesFileName = "devices.json"

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	PairedAt string `json:"paired_at"`
}

type DevicesFile struct {
	Devices []Device `json:"devices"`
}

var (
	devicesMu sync.Mutex
)

func DevicesPath() string {
	return filepath.Join(ConfigDir(), devicesFileName)
}

func LoadDevices() (DevicesFile, error) {
	devicesMu.Lock()
	defer devicesMu.Unlock()
	return loadDevicesLocked()
}

func SaveDevices(df DevicesFile) error {
	devicesMu.Lock()
	defer devicesMu.Unlock()
	return saveDevicesLocked(df)
}

func loadDevicesLocked() (DevicesFile, error) {
	var df DevicesFile

	path := DevicesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return df, nil
		}
		return DevicesFile{}, fmt.Errorf("reading devices.json: %w", err)
	}

	if err := json.Unmarshal(data, &df); err != nil {
		return DevicesFile{}, fmt.Errorf("parsing devices.json: %w", err)
	}

	return df, nil
}

func saveDevicesLocked(df DevicesFile) error {
	data, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling devices.json: %w", err)
	}

	path := DevicesPath()
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing devices.json temp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming devices.json temp: %w", err)
	}

	return nil
}

func AddDevice(name, token string) (Device, error) {
	devicesMu.Lock()
	defer devicesMu.Unlock()

	df, err := loadDevicesLocked()
	if err != nil {
		return Device{}, err
	}

	id, err := generateID(df.Devices)
	if err != nil {
		return Device{}, fmt.Errorf("generating device ID: %w", err)
	}

	dev := Device{
		ID:       id,
		Name:     name,
		Token:    token,
		PairedAt: time.Now().UTC().Format(time.RFC3339),
	}

	df.Devices = append(df.Devices, dev)

	if err := saveDevicesLocked(df); err != nil {
		return Device{}, err
	}

	return dev, nil
}

func RemoveDevice(id string) error {
	devicesMu.Lock()
	defer devicesMu.Unlock()

	df, err := loadDevicesLocked()
	if err != nil {
		return err
	}

	for i, d := range df.Devices {
		if d.ID == id {
			df.Devices = append(df.Devices[:i], df.Devices[i+1:]...)
			return saveDevicesLocked(df)
		}
	}

	return fmt.Errorf("device %q not found", id)
}

func ValidateToken(token string) (Device, bool) {
	devicesMu.Lock()
	defer devicesMu.Unlock()

	df, err := loadDevicesLocked()
	if err != nil {
		return Device{}, false
	}

	for _, d := range df.Devices {
		if d.Token == token {
			return d, true
		}
	}

	return Device{}, false
}

func generateID(existing []Device) (string, error) {
	seen := make(map[string]bool, len(existing))
	for _, d := range existing {
		seen[d.ID] = true
	}

	for attempt := 0; attempt < 10; attempt++ {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		id := hex.EncodeToString(b)
		if !seen[id] {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique device ID")
}

func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
