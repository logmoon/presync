package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"presync/internal/config"
)

func localIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "localhost"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}
	return "localhost"
}

func Pair(name, dest string) error {
	if name == "" {
		return fmt.Errorf("--name is required (e.g. presync pair --name laptop)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build destinations map: for each profile, dest/profile-name
	// Only if --dest was provided
	var destinations map[string]string
	if dest != "" {
		destinations = make(map[string]string)
		for _, p := range cfg.Profiles {
			destinations[p.Name] = filepath.Join(dest, p.Name)
		}
	}

	// Start pairing session FIRST (before sending destinations)
	startResp, err := pairStart(cfg.Daemon.Port)
	if err != nil {
		return err
	}

	// Send destinations if provided; nil destinations = client picks its own
	if len(destinations) > 0 {
		if err := sendDestinations(cfg.Daemon.Port, name, destinations); err != nil {
			return fmt.Errorf("registering destinations: %w", err)
		}
	}

	pairURL := fmt.Sprintf("http://%s:%d/?token=%s", localIP(), cfg.Daemon.Port, startResp.Token)
	qr, err := qrcode.New(pairURL, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("generating QR code: %w", err)
	}

	fmt.Println()
	fmt.Println("Scan this QR code with your device:")
	fmt.Println()
	fmt.Println(qr.ToSmallString(false))
	fmt.Println()
	fmt.Println("Pairing URL (paste this on the client):")
	fmt.Printf("  %s\n", pairURL)
	fmt.Println()
	fmt.Printf("PIN: %s\n", startResp.PIN)
	fmt.Println()
	fmt.Println("Waiting for device to scan...")

	deviceName, err := waitForDevice(cfg.Daemon.Port, startResp.Token)
	if err != nil {
		return err
	}

	fmt.Printf("Device detected: %s\n", deviceName)
	fmt.Print("Confirm pairing? [y/n] ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input != "y" && input != "yes" {
		pairDiscard(cfg.Daemon.Port)
		fmt.Println("Pairing cancelled.")
		return nil
	}

	result, err := pairConfirm(cfg.Daemon.Port)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("Device paired successfully!\n")
	fmt.Printf("  Device ID: %s\n", result.DeviceID)
	if len(destinations) > 0 {
		fmt.Printf("  Destinations:\n")
		for profile, path := range destinations {
			fmt.Printf("    %s → %s\n", profile, path)
		}
	} else {
		fmt.Printf("  No destinations set — client will choose where to store files\n")
	}
	fmt.Printf("  Token stored in devices.json\n")

	return nil
}

func Unpair() error {
	df, err := config.LoadDevices()
	if err != nil {
		return fmt.Errorf("loading devices: %w", err)
	}

	if len(df.Devices) == 0 {
		fmt.Println("No paired devices.")
		return nil
	}

	fmt.Println("Paired devices:")
	for i, d := range df.Devices {
		fmt.Printf("  %d. %s (ID: %s, paired: %s)\n", i+1, d.Name, d.ID, d.PairedAt)
	}

	fmt.Print("\nEnter device number to unpair (or 0 to cancel): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	var choice int
	if _, err := fmt.Sscanf(input, "%d", &choice); err != nil || choice < 0 || choice > len(df.Devices) {
		fmt.Println("Invalid selection.")
		return nil
	}

	if choice == 0 {
		fmt.Println("Cancelled.")
		return nil
	}

	dev := df.Devices[choice-1]
	if err := config.RemoveDevice(dev.ID); err != nil {
		return fmt.Errorf("removing device: %w", err)
	}

	fmt.Printf("Device %q (ID: %s) removed.\n", dev.Name, dev.ID)
	return nil
}

type pairStartResult struct {
	Token string `json:"token"`
	PIN   string `json:"pin"`
}

type pairConfirmResult struct {
	DeviceID       string `json:"device_id"`
	LongLivedToken string `json:"token"`
}

func pairStart(port int) (*pairStartResult, error) {
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/pair", port), "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon — is it running? (presync start): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pair start failed: %s", strings.TrimSpace(string(body)))
	}

	var result pairStartResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding pair response: %w", err)
	}

	return &result, nil
}

func waitForDevice(port int, sessionToken string) (string, error) {
	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)

		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/pair/status", port))
		if err != nil {
			continue
		}

		var status struct {
			Active     bool   `json:"active"`
			Accepted   bool   `json:"accepted"`
			DeviceName string `json:"device_name"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		if status.Accepted {
			return status.DeviceName, nil
		}
	}

	return "", fmt.Errorf("timed out waiting for device (60s)")
}

func pairConfirm(port int) (*pairConfirmResult, error) {
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/pair/confirm", port), "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("confirming pair: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pair confirm failed: %s", strings.TrimSpace(string(body)))
	}

	var result pairConfirmResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding confirm response: %w", err)
	}

	return &result, nil
}

func pairDiscard(port int) {
	http.Post(fmt.Sprintf("http://localhost:%d/pair/discard", port), "application/json", bytes.NewReader(nil))
}

func sendDestinations(port int, name string, destinations map[string]string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"name":         name,
		"destinations": destinations,
	})

	resp, err := http.Post(
		fmt.Sprintf("http://localhost:%d/pair/destinations", port),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registering destinations failed (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
