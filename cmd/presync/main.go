package main

import (
	"fmt"
	"net/http"
	"os"

	"presync/internal/client"
	"presync/internal/config"
	"presync/internal/server"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--daemon" {
		if err := server.RunDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) >= 2 && os.Args[1] == "--watch-daemon" {
		address := ""
		if len(os.Args) >= 3 {
			address = os.Args[2]
		}
		if err := client.RunWatcher(address); err != nil {
			fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error

	switch os.Args[1] {
	case "start":
		err = server.Start()
	case "stop":
		err = server.Stop()
	case "status":
		err = server.Status()
	case "logs":
		err = server.Logs()
	case "pair":
		name, dest := parsePairFlags()
		err = server.Pair(name, dest)
	case "unpair":
		err = server.Unpair()
	case "sync":
		err = runSync()
	case "connect":
		err = runConnect()
	case "watch":
		err = runWatch()
	case "forget":
		address := ""
		if len(os.Args) >= 3 {
			address = os.Args[2]
		}
		err = client.ForgetServer(address)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`presync — one-way file sync over LAN

Usage:
  presync <command>

Server commands:
  start                Start the daemon in the background
  stop                 Stop the running daemon
  status               Show daemon status
  logs                 Tail the daemon log file
  pair --name <name> [--dest <path>]
                       Pair a new device (--dest is optional)
  unpair               Remove a paired device
  sync [name]          Trigger a manual sync (all profiles, or one by name)
    --fresh            Force a new session, ignoring any resume state
    --to <dest>        Sync only to specific destination(s), repeatable
                       (e.g. --to laptop --to phone)

Client commands:
  connect [url]         Pair with a server (url with ?token=) or sync (no args).
                        An address without a token reconnects to that stored
                        server. --fresh forces a clean session.
    --watch            Start the auto-sync watcher after a successful connect
  watch [address]       Start the background auto-sync watcher
    --status           Show whether the watcher is running
    --stop             Stop the watcher
    --once             Run a single auto-sync check in the foreground
  forget [address]      Forget a paired server (first one if no address given)

Other:
  help                 Show this help message`)
}

func runSync() error {
	fresh, profileName, toDests := parseSyncFlags()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if len(cfg.Profiles) == 0 {
		return fmt.Errorf("no sync profiles configured in config.toml")
	}

	token, err := config.GetLocalToken()
	if err != nil {
		return fmt.Errorf("getting local token: %w", err)
	}

	serverAddr := fmt.Sprintf("http://localhost:%d", cfg.Daemon.Port)

	if !isDaemonRunning(serverAddr) {
		return fmt.Errorf("daemon is not running — start it with 'presync start'")
	}

	var profiles []config.Profile
	if profileName != "" {
		for _, p := range cfg.Profiles {
			if p.Name != profileName {
				continue
			}
			if p.Direction == config.DirectionPush {
				return fmt.Errorf("profile %q is a push profile — run it from the paired device", profileName)
			}
			profiles = append(profiles, p)
			break
		}
		if len(profiles) == 0 {
			return fmt.Errorf("profile %q not found in config.toml", profileName)
		}
	} else {
		for _, p := range cfg.Profiles {
			if p.Direction == config.DirectionPush {
				continue
			}
			profiles = append(profiles, p)
		}
		if len(profiles) == 0 {
			return fmt.Errorf("no pull profiles configured in config.toml")
		}
	}

	deviceID := "local"

	fmt.Printf("Syncing %d profile(s)...\n\n", len(profiles))

	var totalSynced, totalErrors int
	for _, p := range profiles {
		for destName, destPath := range p.Destinations {
			if len(toDests) > 0 && !contains(toDests, destName) {
				continue
			}

			fmt.Printf("[%s] %s → %s (%s, dest: %s)\n", p.Name, p.Source, destPath, p.Mode, destName)

			result, err := client.SyncProfile(serverAddr, token, p.Name, deviceID, destPath, fresh, func(path string, done bool, err error) {
				if done {
					fmt.Printf("  done: %s\n", path)
				} else if err != nil {
					fmt.Printf("  error: %s — %v\n", path, err)
				}
			})

			if err != nil {
				fmt.Fprintf(os.Stderr, "  sync failed: %v\n", err)
				totalErrors++
				continue
			}

			totalSynced += result.FilesSynced
			totalErrors += result.Errors

			if result.FilesSynced == 0 && result.Errors == 0 {
				fmt.Println("  already up to date")
			}
			fmt.Println()
		}
	}

	fmt.Printf("Sync complete. %d file(s) synced", totalSynced)
	if totalErrors > 0 {
		fmt.Printf(", %d error(s)", totalErrors)
	}
	fmt.Println()

	return nil
}

func parseSyncFlags() (fresh bool, profileName string, toDests []string) {
	args := os.Args[2:]
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--fresh" {
			fresh = true
		} else if arg == "--to" && i+1 < len(args) {
			toDests = append(toDests, args[i+1])
			skipNext = true
		} else if len(arg) > 5 && arg[:5] == "--to=" {
			toDests = append(toDests, arg[5:])
		} else if len(arg) > 0 && arg[0] != '-' {
			profileName = arg
		}
	}
	return fresh, profileName, toDests
}

func parsePairFlags() (name, dest string) {
	args := os.Args[2:]
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--name" && i+1 < len(args) {
			name = args[i+1]
			skipNext = true
		} else if arg == "--dest" && i+1 < len(args) {
			dest = args[i+1]
			skipNext = true
		} else if len(arg) > 7 && arg[:7] == "--name=" {
			name = arg[7:]
		} else if len(arg) > 7 && arg[:7] == "--dest=" {
			dest = arg[7:]
		}
	}
	return name, dest
}

func isDaemonRunning(serverAddr string) bool {
	resp, err := (&http.Client{Timeout: 2000000000}).Get(serverAddr + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func runConnect() error {
	fresh, pairingURL, destDir, watch := parseConnectFlags()

	progress := func(path string, done bool, err error) {
		if done {
			fmt.Printf("  done: %s\n", path)
		} else if err != nil {
			fmt.Printf("  error: %s — %v\n", path, err)
		}
	}

	address, err := client.Connect(pairingURL, destDir, fresh, progress)
	if err != nil {
		return err
	}

	if watch {
		if err := client.StartWatcher(address); err != nil {
			return fmt.Errorf("starting auto-sync watcher: %w", err)
		}
		return nil
	}

	fmt.Println("Run 'presync watch' to sync automatically when this server is reachable.")
	return nil
}

func runWatch() error {
	address, stop, status, once := parseWatchFlags()

	switch {
	case stop:
		return client.StopWatcher()
	case status:
		return client.WatcherStatus()
	case once:
		return client.RunWatcherOnce(address)
	default:
		return client.StartWatcher(address)
	}
}

func parseWatchFlags() (address string, stop, status, once bool) {
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--stop":
			stop = true
		case "--status":
			status = true
		case "--once":
			once = true
		default:
			if len(arg) > 0 && arg[0] != '-' {
				address = arg
			}
		}
	}
	return address, stop, status, once
}

func parseConnectFlags() (fresh bool, pairingURL, destDir string, watch bool) {
	args := os.Args[2:]
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--fresh" {
			fresh = true
		} else if arg == "--watch" {
			watch = true
		} else if arg == "--dest" {
			if i+1 < len(args) {
				destDir = args[i+1]
				skipNext = true
			}
		} else if len(arg) > 7 && arg[:7] == "--dest=" {
			destDir = arg[7:]
		} else if len(arg) > 0 && arg[0] != '-' {
			pairingURL = arg
		}
	}
	return fresh, pairingURL, destDir, watch
}
