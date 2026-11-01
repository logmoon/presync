# Build Plan — presync

## Core Principle

Pure logic first, then integration layers, then surface. The diff engine and config system exist and are testable before anything touches the network. Pairing and trust are built before the CLI wires into them. Transfer and session management come after diff output types are settled. The web UI and Android APK come last — they depend on all backend APIs being real, not stubbed. Nothing in Phase 1 may depend on mDNS, multiple profiles, or auto-sync — those are explicitly deferred so the foundation stays small enough to verify by hand.

## Conventions

**Cross-platform from the start.** Every Go package that needs platform-specific behavior ships with both `_unix.go` and `_windows.go` build-tagged files from the first task. No Unix-only-then-add-Windows later.

## Phase 1 — Core

### 01 Server daemon
Background HTTP server process that can be started, stopped, and queried.

**UI:**
- N/A — this is a backend process; verify via `presync status` text output

**Logic:**
- `presync start` launches `internal/server/daemon.go`, detaches from terminal, writes a PID file
- `presync stop` reads the PID file and terminates the process cleanly
- `presync status` reports running/stopped, listening port, uptime
- `presync logs` tails `~/.config/presync/logs/presync.log`

**Exit criteria:** `presync start` launches the daemon, closing the terminal doesn't kill it, `presync status` reports it running, `presync stop` cleanly shuts it down.

**Status:** DONE

### 02 File diff engine
Builds manifests and computes the sync delta. Pure computation — the foundation that transfer and sync logic call into.

**UI:**
- N/A — pure backend computation

**Logic:**
- `internal/diff/diff.go`: types (`FileEntry`, `Manifest`, `FileAction`, `Delta`), `BuildManifest`, `ComputeHash`, `ComputeDelta`
- `BuildManifest(root)` walks a folder, records relative path, size, mtime for each file; SHA-256 left empty for caller to fill selectively
- `ComputeHash(path)` computes SHA-256 of a single file — called only when size+mtime match but confirmation is needed
- `ComputeDelta(src, dst, mode)` compares two manifests; `mode = "mirror"` populates deletes, `mode = "additive"` always leaves deletes empty
- Unit tests covering all delta scenarios (add, update, delete mirror, delete additive, identical files, empty manifests)

**Exit criteria:** all test cases pass, `go vet ./internal/diff/...` clean, `gofmt` clean.

### 03 Single sync profile config
One profile end to end, proving the config format is loaded and exposed.

**UI:**
- N/A — config-file driven (`config.toml`)

**Logic:**
- `internal/server/profiles.go` reads `[[profiles]]` entries from `config.toml`
- Profile is exposed to the diff engine and transfer engine by name

**Exit criteria:** editing `config.toml`'s profile `source`/`destination` and restarting the daemon changes what gets synced.

### 04 QR + PIN pairing + trust store
The full handshake between server and client establishing trust, plus the device trust store.

**UI:**
- Server: terminal-rendered QR code encoding `http://<local-ip>:7700/pair?token=<session-token>`
- Both sides: 4-digit confirmation PIN displayed
- Server: `[y/n]` confirmation prompt

**Logic:**
- `internal/pairing/pairing.go`: generates 60s single-use session token, derives matching PIN on both ends
- `internal/server/trust.go`: validates tokens, issues 256-bit long-lived tokens, persists to `devices.json`
- Client persists the long-lived token to its local store

**Exit criteria:** a brand-new client can scan/paste the QR, confirm the PIN, and end up with a stored long-lived token that authenticates all future requests with no further prompts.

### 05 Transfer engine + checksums + sessions
Moves files over HTTP with end-to-end verification and survives dropped connections.

**UI:**
- N/A — backend; surfaced later via Sync Progress UI (08)

**Logic:**
- `internal/transfer/transfer.go`: chunked HTTP send/receive
- `internal/transfer/session.go`: session state file tracking each file as `pending`/`in_progress`/`done`
- Server computes SHA-256 before sending; client computes SHA-256 of the assembled temp file after receipt
- Hash mismatch triggers retransfer, up to 3 attempts, then marks the file as error
- Client writes to `<filename>.presync-tmp` during transfer, atomic rename to real filename only after checksum match
- On reconnect: server checks for existing session, skips `done` files, resumes the `in_progress` file via HTTP range request
- Session file deleted when all files reach `done`; stale sessions older than 7 days pruned on server start

**Exit criteria:** start a sync, kill the connection mid-transfer, reconnect — the sync completes without re-downloading completed files, and the previously in-progress file resumes from where it left off.

### 06 Server CLI commands
The remaining server-mode CLI surface besides start/stop/status. Wires into the real packages built in 02–05.

**UI:**
- N/A — CLI text output for each command

**Logic:**
- `presync pair` triggers the pairing flow (uses 04)
- `presync unpair` lists and removes a device from `devices.json` (uses 04)
- `presync sync` triggers a manual sync from the terminal (uses 02, 03, 05)
- `presync sync --fresh` forces a new session ignoring existing resume state (uses 05)

**Exit criteria:** every command listed runs against a started daemon and produces correct, documented output.

### 07 Client connect (PC)
The CLI client mode that pairs with and pulls from a server.

**UI:**
- N/A — terminal prompts/output (QR scan or paste URL, progress text)

**Logic:**
- `presync connect` with no args prompts for a server URL or QR input on first run
- `presync connect <address>` connects directly to a known server
- Stores trusted server + token in `~/.config/presync/servers.json`
- On subsequent runs, reconnects automatically using the stored token

**Exit criteria:** running `presync connect <address>` against a started, paired server begins a sync without re-pairing.

### 08 Web UI — home, progress, settings
The actual UI loaded into the WebView and reachable from any LAN browser.

**UI:**
- Home/Dashboard: profile list with last-synced time, sync status, Sync Now / Sync All
- Sync Progress: profile name, file count X of Y, current filename, progress bar, cancel
- Settings: auto-sync toggle (UI only — logic in Phase 2), paired server info, "Forget this server"
- Built with mock data first, then wired to the real `/status` and `/sync` endpoints

**Logic:**
- Vanilla JS fetch calls to the server's HTTP API for status, profile list, and triggering sync
- Progress updates via polling or a simple event stream from the server during an active sync

**Exit criteria:** opening the web UI in a browser on the LAN shows real profile state, and clicking "Sync Now" triggers and visibly progresses a real sync using everything built in 01–07.

### 09 Android APK — WebView shell + SAF bridge
The minimal Android client.

**UI:**
- WebView loading the server's web UI by URL (mock URL acceptable until 08 exists)
- First-run folder picker (SAF `ACTION_OPEN_DOCUMENT_TREE`)

**Logic:**
- `MainActivity.kt`: hosts the WebView, loads the paired server's address
- `SafBridge.kt`: `JavascriptInterface` exposing `writeFile(path, base64data)`, manifest-of-destination reads, persisting folder URI grants across restarts
- Token store via SharedPreferences
- Background service keeps the WebView/transfer alive during sync

**Exit criteria:** the APK installs via sideload, a user picks a destination folder once, and a file written via the bridge from a test web page lands correctly in that folder after an app restart.

## Phase 2 — Polish

### 10 Multiple profiles
Extend config parsing beyond the single profile.

**UI:**
- Profiles screen lists all configured profiles, not just one

**Logic:**
- `internal/server/profiles.go` iterates all `[[profiles]]` entries in `config.toml`
- Each profile syncs independently with its own session state

**Exit criteria:** adding a second `[[profiles]]` block (e.g. Podcasts) and restarting the daemon makes it appear and sync correctly alongside the first, without affecting its state.

### 11 Per-profile sync modes (mirror / additive)
Make the mode field in config actually change delete behavior.

**UI:**
- Profiles screen shows each profile's mode

**Logic:**
- `internal/diff/diff.go` respects `mode = "mirror"` (includes deletes in the delta) vs `mode = "additive"` (deletes always omitted from the delta)

**Exit criteria:** deleting a file from an `additive` profile's source and syncing leaves it present on the recipient; doing the same on a `mirror` profile removes it from the recipient.

**Status:** DONE

### 11A Push profiles (client → server)
One-way upload direction, so a phone can send files it owns to the PC.

**UI:**
- Android profile card: per-profile source folder picker + "Send to PC"
- PC Profiles screen: receive folder, direction badge, no sync button

**Logic:**
- `direction = "push"` profile with a server-side `destination`
- `POST /push` computes the delta from the client manifest; uploads append to `.presync-tmp` with server-owned offsets
- SHA-256 verified before atomic rename; mirror deletions persisted in the session and applied only on `/push/complete`
- Empty source manifest refused for mirror when the destination has files

**Exit criteria:** a phone uploads only changed files, resume works after a dropped connection, additive never deletes, and mirror deletes only after a successful complete.

**Status:** DONE

### 12 Auto-sync on same network
Background sync without manual triggering.

**UI:**
- Settings: auto-sync toggle (global default + per-profile override, matching `config.toml`)
- Notification on completion or error (Android) / equivalent on PC

**Logic:**
- Client periodically attempts a connection to its stored server address; on success, triggers a sync respecting a cooldown (no more than once per N minutes)
- Android: background service; PC: lightweight watcher process spawned by the client

**Exit criteria:** with auto-sync enabled and the client device joining the same WiFi network as a paired server, a sync starts without any user action, and a second join within the cooldown window does not trigger another sync.

**Status:** CODE COMPLETE — PC watcher E2E verified; Android on-device check pending.

### 13 mDNS discovery
Survive server IP changes.

**UI:**
- N/A — transparent to the user

**Logic:**
- `internal/mdns/mdns.go`: server advertises as `presync.local` via `grandcat/zeroconf`
- Client tries `presync.local:7700` first, falls back to last known stored IP, updates stored IP on successful connection

**Exit criteria:** changing the server's IP address (e.g. reconnecting to a different WiFi network) and then running `presync connect` with no arguments still finds and syncs with the server.

### 14 Sync history / logs in UI
Surface what's already being logged.

**UI:**
- Profiles screen: per-profile sync history (timestamp, result, file counts)

**Logic:**
- Server exposes recent sync session outcomes via an HTTP endpoint, read from completed/archived session data

**Exit criteria:** after several syncs, the Profiles screen shows an accurate, ordered history for each profile.

### 15 Cross-compile client binaries
Make distribution actually trivial as designed.

**UI:**
- N/A

**Logic:**
- Build script producing Linux/Mac/Windows binaries via `GOOS`/`GOARCH` in one step

**Exit criteria:** one build command produces working binaries for all three target platforms, each verified to run `connect` successfully against a live server.

## Phase 3 — Nice to Have

### 16 HTTPS / self-signed cert
**UI:** N/A
**Logic:** Server optionally serves over HTTPS with a self-signed cert; client accepts/pins it on pairing.
**Exit criteria:** with HTTPS enabled, pairing and sync both succeed and plain-HTTP fallback still works when disabled.

### 17 Delta transfers (byte-level)
**UI:** N/A
**Logic:** Only changed byte ranges within a file are transferred instead of the whole file on update.
**Exit criteria:** updating a large file with a small change transfers measurably less data than the full file size.

### 18 Server-hosted APK + binary downloads
**UI:** Initial pairing QR can point to an APK/binary download URL before the app is installed.
**Logic:** Server serves `/app.apk` and platform binaries from a known path.
**Exit criteria:** scanning the initial QR on a device with nothing installed downloads a working APK directly.

### 19 Desktop tray icon (server mode)
**UI:** Tray icon with status and quick actions (sync now, open web UI, quit).
**Logic:** Wraps the existing daemon with a tray-icon front end.
**Exit criteria:** starting the server in tray mode shows status at a glance and triggers a sync without using the terminal.
