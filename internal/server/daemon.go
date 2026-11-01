package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"presync/internal/config"
	"presync/internal/transfer"
)

type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelError
)

var (
	currentLevel logLevel = levelInfo
	logger       *log.Logger

	cfgMu     sync.RWMutex
	activeCfg *config.Config
)

func parseLevel(s string) logLevel {
	switch s {
	case "debug":
		return levelDebug
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

func logDebug(format string, args ...interface{}) {
	if currentLevel <= levelDebug {
		logger.Printf("[DEBUG] "+format, args...)
	}
}

func logInfo(format string, args ...interface{}) {
	if currentLevel <= levelInfo {
		logger.Printf("[INFO] "+format, args...)
	}
}

func logError(format string, args ...interface{}) {
	if currentLevel <= levelError {
		logger.Printf("[ERROR] "+format, args...)
	}
}

func getConfig() config.Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return *activeCfg
}

func Start() error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	if pid, err := readPIDFile(); err == nil {
		if processAlive(pid) {
			return fmt.Errorf("daemon already running (PID %d)", pid)
		}
		os.Remove(config.PIDPath())
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	cmd := exec.Command(binary, "--daemon")
	configureDaemonCmd(cmd)

	logPath := config.LogPath()
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening log file for daemon output: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launching daemon: %w", err)
	}

	// Wait for PID file to appear, confirming daemon started successfully
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pid, err := readPIDFile(); err == nil && pid == cmd.Process.Pid {
			fmt.Printf("daemon started (PID %d, port %d)\n", cmd.Process.Pid, cfg.Daemon.Port)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Check if process is still alive
	if !processAlive(cmd.Process.Pid) {
		return fmt.Errorf("daemon failed to start — check logs at %s", logPath)
	}

	// Process is alive but PID file not yet written — report success with caveat
	fmt.Printf("daemon started (PID %d, port %d)\n", cmd.Process.Pid, cfg.Daemon.Port)
	return nil
}

func RunDaemon() error {
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Daemon.Port))
	if err != nil {
		return fmt.Errorf("binding port %d: %w", cfg.Daemon.Port, err)
	}

	pid := os.Getpid()
	if err := writePIDFile(pid); err != nil {
		listener.Close()
		return fmt.Errorf("writing PID file: %w", err)
	}

	setupLogger(cfg.Daemon.LogLevel)

	logInfo("presync daemon starting on port %d (PID %d)", cfg.Daemon.Port, pid)

	transfer.PruneStaleSessions()

	pruneCtx, pruneCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-pruneCtx.Done():
				return
			case <-ticker.C:
				transfer.PruneStaleSessions()
			}
		}
	}()

	srv := newServer(&cfg)

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			logError("HTTP server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	logInfo("shutting down daemon...")
	pruneCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logError("forced shutdown: %v", err)
	}

	os.Remove(config.PIDPath())
	logInfo("daemon stopped")
	return nil
}

func Stop() error {
	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("daemon not running: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	// Stale PID file: process is already gone — clean it up and report stopped.
	if !processAlive(pid) {
		os.Remove(config.PIDPath())
		fmt.Printf("daemon stopped (PID %d)\n", pid)
		return nil
	}

	if err := sendStopSignal(process); err != nil {
		return fmt.Errorf("sending stop signal to PID %d: %w", pid, err)
	}

	for i := 0; i < 50; i++ {
		if !processAlive(pid) {
			os.Remove(config.PIDPath())
			fmt.Printf("daemon stopped (PID %d)\n", pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The daemon is still alive after 5s. Keep the PID file so status reports
	// it correctly — removing it here would let `presync start` spawn a second
	// daemon that fails to bind while the original keeps running.
	return fmt.Errorf("daemon (PID %d) did not stop within 5s — check the logs", pid)
}

func Status() error {
	pid, err := readPIDFile()
	if err != nil {
		fmt.Println("stopped")
		return nil
	}

	if !processAlive(pid) {
		os.Remove(config.PIDPath())
		fmt.Println("stopped")
		return nil
	}

	uptime, err := pidUptime(pid)
	if err != nil {
		fmt.Printf("running (PID %d, uptime unknown)\n", pid)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		cfg = config.Config{}
		cfg.Daemon.Port = 7700
	}

	fmt.Printf("running (PID %d, port %d, uptime %s)\n", pid, cfg.Daemon.Port, formatDuration(uptime))
	return nil
}

func Logs() error {
	logPath := config.LogPath()
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return fmt.Errorf("no log file found at %s — has the daemon been started?", logPath)
	}

	return tailFile(logPath)
}

func newServer(cfg *config.Config) *http.Server {
	cfgMu.Lock()
	activeCfg = cfg
	cfgMu.Unlock()

	mux := http.NewServeMux()

	// Unauthenticated routes
	mux.HandleFunc("/health", handleHealth())
	mux.HandleFunc("/pair", handlePairStart())
	mux.HandleFunc("/pair/accept", handlePairAccept())
	mux.HandleFunc("/pair/destinations", handlePairDestinations())
	mux.HandleFunc("/pair/confirmed", handlePairConfirmed())

	// Web bootstrap: hands out the scoped local token. The token is only valid
	// on web-UI routes (withWebAuth), never on /sync* file-download routes.
	mux.HandleFunc("/web/token", handleWebToken())

	// Local-admin routes: CLI on the server machine only (loopback + local token)
	mux.HandleFunc("/pair/confirm", withLocalAdmin(handlePairConfirm()))
	mux.HandleFunc("/pair/discard", withLocalAdmin(handlePairDiscard()))
	mux.HandleFunc("/pair/status", withLocalAdmin(handlePairStatus()))
	mux.HandleFunc("/reload", withLocalAdmin(handleReload()))

	// Web-UI routes: device token or local token
	mux.HandleFunc("/web/device-id", withWebAuth(handleWebDeviceID()))
	mux.HandleFunc("/web/sync", withWebAuth(handleWebSync()))
	mux.HandleFunc("/web/sessions", withWebAuth(handleWebSessions()))
	mux.HandleFunc("/profiles", withWebAuth(handleProfiles()))
	mux.HandleFunc("/profiles/detail", withWebAuth(handleProfilesDetail()))
	mux.HandleFunc("/auto-sync", withWebAuth(handleAutoSync()))
	mux.HandleFunc("/status", withWebAuth(handleStatus()))

	// Device-token-only routes: raw file transfer and sync state mutation
	mux.HandleFunc("/sync", withAuth(handleSyncStart()))
	mux.HandleFunc("/sync/file", withAuth(handleSyncFile()))
	mux.HandleFunc("/sync/confirm", withAuth(handleSyncConfirm()))
	mux.HandleFunc("/sync/error", withAuth(handleSyncError()))
	mux.HandleFunc("/sync/status", withAuth(handleSyncStatus()))
	mux.HandleFunc("/sync/complete", withAuth(handleSyncComplete()))

	// Push (client -> server) routes
	mux.HandleFunc("/push", withAuth(handlePushStart()))
	mux.HandleFunc("/push/file", withAuth(handlePushFile()))
	mux.HandleFunc("/push/confirm", withAuth(handlePushConfirm()))
	mux.HandleFunc("/push/error", withAuth(handlePushError()))
	mux.HandleFunc("/push/status", withAuth(handlePushStatus()))
	mux.HandleFunc("/push/complete", withAuth(handlePushComplete()))

	// Static file serving (must be last — catch-all route)
	mux.Handle("/", staticFileHandler("web"))

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Daemon.Port),
		Handler: mux,
	}
}

func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := getConfig()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","port":%d}`, cfg.Daemon.Port)
	}
}

func handleReload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		cfg, err := config.Load()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to reload config: %v", err)})
			return
		}

		cfgMu.Lock()
		activeCfg = &cfg
		cfgMu.Unlock()

		logInfo("config reloaded")
		writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
	}
}

func readPIDFile() (int, error) {
	data, err := os.ReadFile(config.PIDPath())
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID file contents: %w", err)
	}

	return pid, nil
}

func writePIDFile(pid int) error {
	return os.WriteFile(config.PIDPath(), []byte(strconv.Itoa(pid)), 0o644)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func setupLogger(level string) {
	currentLevel = parseLevel(level)
	logPath := config.LogPath()
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("opening log file: %v", err)
	}
	logger = log.New(f, "", log.LstdFlags|log.Lshortfile)
}
