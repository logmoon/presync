package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const serversFileName = "servers.json"

type ServerProfile struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
}

type ServerEntry struct {
	Address  string          `json:"address"`
	Token    string          `json:"token"`
	DeviceID string          `json:"device_id"`
	Name     string          `json:"name"`
	Profiles []ServerProfile `json:"profiles"`
}

type ServersFile struct {
	Servers []ServerEntry `json:"servers"`
}

var (
	serversMu sync.Mutex
)

func ServersPath() string {
	return filepath.Join(ConfigDir(), serversFileName)
}

func LoadServers() (ServersFile, error) {
	serversMu.Lock()
	defer serversMu.Unlock()
	return loadServersLocked()
}

func SaveServers(sf ServersFile) error {
	serversMu.Lock()
	defer serversMu.Unlock()
	return saveServersLocked(sf)
}

func loadServersLocked() (ServersFile, error) {
	var sf ServersFile

	path := ServersPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sf, nil
		}
		return ServersFile{}, fmt.Errorf("reading servers.json: %w", err)
	}

	if err := json.Unmarshal(data, &sf); err != nil {
		return ServersFile{}, fmt.Errorf("parsing servers.json: %w", err)
	}

	return sf, nil
}

func saveServersLocked(sf ServersFile) error {
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling servers.json: %w", err)
	}

	path := ServersPath()
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing servers.json temp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming servers.json temp: %w", err)
	}

	return nil
}

func GetServer(address string) (*ServerEntry, error) {
	serversMu.Lock()
	defer serversMu.Unlock()

	sf, err := loadServersLocked()
	if err != nil {
		return nil, err
	}

	for i := range sf.Servers {
		if sf.Servers[i].Address == address {
			return &sf.Servers[i], nil
		}
	}

	return nil, nil
}

func SaveServer(entry ServerEntry) error {
	serversMu.Lock()
	defer serversMu.Unlock()

	sf, err := loadServersLocked()
	if err != nil {
		return err
	}

	for i := range sf.Servers {
		if sf.Servers[i].Address == entry.Address {
			sf.Servers[i] = entry
			return saveServersLocked(sf)
		}
	}

	sf.Servers = append(sf.Servers, entry)
	return saveServersLocked(sf)
}

func RemoveServer(address string) error {
	serversMu.Lock()
	defer serversMu.Unlock()

	sf, err := loadServersLocked()
	if err != nil {
		return err
	}

	for i, s := range sf.Servers {
		if s.Address == address {
			sf.Servers = append(sf.Servers[:i], sf.Servers[i+1:]...)
			return saveServersLocked(sf)
		}
	}

	return fmt.Errorf("server %q not found", address)
}
