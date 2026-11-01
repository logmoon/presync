package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"presync/internal/config"
)

const (
	watchInterval      = 30 * time.Second
	watchHealthTimeout = 3 * time.Second
)

var (
	errServerUnreachable = errors.New("server unreachable")
	watchLog             = log.New(os.Stdout, "", log.LstdFlags)
)

// watchCycleResult describes what one watcher poll did.
type watchCycleResult struct {
	Server            string
	Profiles          []string
	FilesSynced       int
	FilesErrored      int
	Triggered         bool
	CooldownRemaining time.Duration
}

// StartWatcher launches the auto-sync watcher as a detached process. It is
// idempotent: a running watcher is reported and left alone.
func StartWatcher(address string) error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	if err := checkWatchTarget(address); err != nil {
		return err
	}

	if pid, err := readWatchPID(); err == nil {
		if watcherProcessAlive(pid) {
			fmt.Printf("watcher already running (PID %d)\n", pid)
			return nil
		}
		os.Remove(config.WatchPIDPath())
	}

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	args := []string{"--watch-daemon"}
	if address != "" {
		args = append(args, normalizeAddr(address))
	}

	cmd := exec.Command(binary, args...)
	configureWatcherCmd(cmd)

	logPath := config.WatchLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening watcher log: %w", err)
	}
	defer logFile.Close()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launching watcher: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pid, err := readWatchPID(); err == nil && pid == cmd.Process.Pid {
			fmt.Printf("watcher started (PID %d, log %s)\n", cmd.Process.Pid, logPath)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !watcherProcessAlive(cmd.Process.Pid) {
		return fmt.Errorf("watcher failed to start — check logs at %s", logPath)
	}

	fmt.Printf("watcher started (PID %d, log %s)\n", cmd.Process.Pid, logPath)
	return nil
}

// StopWatcher terminates a running watcher. A stale PID file is cleaned up.
func StopWatcher() error {
	pid, err := readWatchPID()
	if err != nil {
		return fmt.Errorf("watcher not running: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding watcher process %d: %w", pid, err)
	}

	if !watcherProcessAlive(pid) {
		os.Remove(config.WatchPIDPath())
		fmt.Printf("watcher stopped (PID %d)\n", pid)
		return nil
	}

	if err := sendWatcherStop(process); err != nil {
		return fmt.Errorf("sending stop signal to PID %d: %w", pid, err)
	}

	for i := 0; i < 50; i++ {
		if !watcherProcessAlive(pid) {
			os.Remove(config.WatchPIDPath())
			fmt.Printf("watcher stopped (PID %d)\n", pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("watcher (PID %d) did not stop within 5s — check %s", pid, config.WatchLogPath())
}

// WatcherStatus reports whether the watcher process is running.
func WatcherStatus() error {
	pid, err := readWatchPID()
	if err != nil {
		fmt.Println("watcher stopped")
		return nil
	}

	if !watcherProcessAlive(pid) {
		os.Remove(config.WatchPIDPath())
		fmt.Println("watcher stopped")
		return nil
	}

	fmt.Printf("watcher running (PID %d, log %s)\n", pid, config.WatchLogPath())
	return nil
}

// RunWatcher is the watcher process body: poll, trigger when allowed, repeat
// until SIGTERM/SIGINT.
func RunWatcher(address string) error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	pid := os.Getpid()
	if err := writeWatchPID(pid); err != nil {
		return fmt.Errorf("writing watcher PID file: %w", err)
	}
	defer os.Remove(config.WatchPIDPath())

	watchLog = log.New(os.Stdout, "", log.LstdFlags)
	watchLog.Printf("watcher starting (PID %d)", pid)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		res, err := runWatchCycle(address)
		switch {
		case errors.Is(err, errServerUnreachable):
			// Normal while the client is off the server's network.
		case err != nil:
			watchLog.Printf("auto-sync: %v", err)
		case res.Triggered:
			watchLog.Printf("auto-sync complete for %s: %d file(s) synced, %d error(s)", res.Server, res.FilesSynced, res.FilesErrored)
		}

		select {
		case <-ctx.Done():
			watchLog.Printf("watcher stopped")
			return nil
		case <-ticker.C:
		}
	}
}

// RunWatcherOnce performs a single foreground check and sync, for debugging
// and tests.
func RunWatcherOnce(address string) error {
	res, err := runWatchCycle(address)
	if errors.Is(err, errServerUnreachable) {
		return fmt.Errorf("server %s is not reachable", res.Server)
	}
	if err != nil {
		return err
	}

	switch {
	case res.CooldownRemaining > 0:
		fmt.Printf("cooldown active — %s remaining\n", res.CooldownRemaining.Round(time.Second))
	case res.Triggered:
		fmt.Printf("auto-sync complete: %d file(s) synced, %d error(s)\n", res.FilesSynced, res.FilesErrored)
	case len(res.Profiles) == 0:
		fmt.Println("no auto-sync profiles eligible")
	default:
		fmt.Println("nothing to sync")
	}
	return nil
}

func runWatchCycle(address string) (watchCycleResult, error) {
	var res watchCycleResult

	sf, err := config.LoadServers()
	if err != nil {
		return res, fmt.Errorf("loading servers: %w", err)
	}
	if len(sf.Servers) == 0 {
		return res, fmt.Errorf("no paired server — run 'presync connect <pairing-url>' first")
	}

	entry := &sf.Servers[0]
	if address != "" {
		addr := normalizeAddr(address)
		entry = nil
		for i := range sf.Servers {
			if sf.Servers[i].Address == addr {
				entry = &sf.Servers[i]
				break
			}
		}
		if entry == nil {
			return res, fmt.Errorf("no stored pairing for server %q", addr)
		}
	}
	res.Server = entry.Address

	if !serverReachable(entry.Address) {
		return res, errServerUnreachable
	}

	autoCfg, err := FetchAutoSyncConfig(entry.Address, entry.Token)
	if err != nil {
		return res, fmt.Errorf("fetching auto-sync config: %w", err)
	}
	if len(autoCfg.Profiles) == 0 {
		return res, nil
	}

	type target struct {
		name string
		dest string
	}
	var targets []target
	for _, name := range autoCfg.Profiles {
		dest := destinationForProfile(entry, name)
		if dest == "" {
			continue
		}
		targets = append(targets, target{name: name, dest: dest})
	}
	if len(targets) == 0 {
		return res, nil
	}
	for _, t := range targets {
		res.Profiles = append(res.Profiles, t.name)
	}

	if cooldown := time.Duration(autoCfg.CooldownMinutes) * time.Minute; cooldown > 0 && !entry.LastAutoSyncAt.IsZero() {
		if remaining := cooldown - time.Since(entry.LastAutoSyncAt); remaining > 0 {
			res.CooldownRemaining = remaining
			return res, nil
		}
	}

	if err := config.TouchAutoSync(entry.Address, time.Now()); err != nil {
		return res, fmt.Errorf("recording auto-sync time: %w", err)
	}
	res.Triggered = true

	for _, t := range targets {
		watchLog.Printf("auto-sync [%s] → %s", t.name, t.dest)
		result, err := SyncProfile(ensureHTTP(entry.Address), entry.Token, t.name, entry.DeviceID, t.dest, false, nil)
		if err != nil {
			watchLog.Printf("auto-sync [%s] failed: %v", t.name, err)
			res.FilesErrored++
			continue
		}
		res.FilesSynced += result.FilesSynced
		res.FilesErrored += result.Errors
		if result.FilesSynced == 0 && result.Errors == 0 {
			watchLog.Printf("auto-sync [%s] already up to date", t.name)
		}
	}

	return res, nil
}

func checkWatchTarget(address string) error {
	sf, err := config.LoadServers()
	if err != nil {
		return fmt.Errorf("loading servers: %w", err)
	}
	if len(sf.Servers) == 0 {
		return fmt.Errorf("no paired server — run 'presync connect <pairing-url>' first")
	}
	if address == "" {
		return nil
	}

	addr := normalizeAddr(address)
	for _, s := range sf.Servers {
		if s.Address == addr {
			return nil
		}
	}
	return fmt.Errorf("no stored pairing for server %q", addr)
}

func destinationForProfile(entry *config.ServerEntry, name string) string {
	for _, p := range entry.Profiles {
		if p.Name == name {
			return p.Destination
		}
	}
	return ""
}

func serverReachable(addr string) bool {
	c := &http.Client{Timeout: watchHealthTimeout}
	resp, err := c.Get(ensureHTTP(addr) + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func readWatchPID() (int, error) {
	data, err := os.ReadFile(config.WatchPIDPath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parsing watcher PID file: %w", err)
	}
	return pid, nil
}

func writeWatchPID(pid int) error {
	return os.WriteFile(config.WatchPIDPath(), []byte(strconv.Itoa(pid)), 0o644)
}
