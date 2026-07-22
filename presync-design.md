# presync — Design Document

> One-way, profile-based file sync from any device to any device over local WiFi.

---

## Overview

`presync` is a local network file sync tool. One binary, two modes: **server** (the master, hosts the files and the web UI) and **client** (any recipient device that wants to sync). The same binary runs on Linux, Mac, and Windows — invoke it differently depending on which role the machine plays. On Android, a minimal APK fills the client role since you can't run arbitrary binaries there.

No cloud. No accounts. No cables. Doesn't care what's on the other end.

---

## Goals

- Simple to set up, invisible when it's working
- Generic enough to sync any folder, not just music
- Device-agnostic — recipient can be Android, Linux, Mac, or Windows
- Secure on a LAN without being annoying
- One binary that acts as both server and client, minimal APK for Android
- Learnable codebase — good project for picking up a new language

---

## Non-Goals

- Bidirectional sync (server is always master, clients are always mirrors)
- Cloud relay or sync over the internet
- iOS support

---

## Architecture

```
                         ┌─────────────────────────────┐
                         │        presync (server)      │
                         │                              │
                         │  ├── HTTP server             │
                         │  ├── File diff engine        │
                         │  ├── QR generator            │
                         │  ├── Web UI                  │
                         │  └── Device trust store      │
                         └──────────────┬───────────────┘
                                        │ WiFi LAN
                   ┌────────────────────┼─────────────────────┐
                   │                    │                      │
      ┌────────────▼───────────┐  ┌─────▼──────────┐  ┌──────▼──────────────┐
      │   presync (client)     │  │  presync (client)│  │   presync APK       │
      │   Linux / Mac / Windows│  │  Linux / Mac /  │  │   Android           │
      │                        │  │  Windows        │  │                     │
      │  ├── CLI client mode   │  │                 │  │  ├── WebView        │
      │  └── Native file I/O   │  │                 │  │  ├── SAF bridge     │
      └────────────────────────┘  └─────────────────┘  │  └── Token store   │
                                                        └─────────────────────┘
```

One binary, two modes:

- **Server mode** (`presync start`): hosts the files, serves the web UI, manages profiles and trust, runs as a background daemon
- **Client mode** (`presync connect`): connects to a server on the network, pulls the sync manifest, writes files to the local filesystem

The same binary handles both roles on Linux, Mac, and Windows. On Android, the APK fills the client role — it's a WebView shell that loads the server's web UI, with a native SAF bridge for filesystem access underneath.

Cross-compilation is trivial if using Go: one build step produces binaries for all platforms.

---

## Server Mode

### Responsibilities

- Serve the web UI over HTTP on the local network
- Manage sync profiles (config file)
- Handle device pairing and trust
- Compute file diffs and serve file transfer requests
- Run in the background, detached from the terminal

### CLI Interface

```
presync start              Start the daemon in the background (server mode)
presync stop               Stop the daemon
presync status             Show daemon status, paired devices, last sync per profile
presync sync               Trigger a manual sync from the terminal (no client interaction)
presync sync --fresh       Force a new session, ignoring any existing resume state
presync pair               Show QR code in terminal to pair a new device
presync unpair             Remove a paired device
presync logs               Tail daemon logs
```

---

## Client Mode

### Responsibilities

- Connect to a presync server on the local network
- Receive and apply the sync manifest
- Write files to the local filesystem
- Report manifest of destination back to server
- Handle resume state and checksum verification locally

### CLI Interface

```
presync connect            Scan or enter server address, pair if needed, sync
presync connect <address>  Connect directly to a known server (e.g. presync.local:7700)
presync status             Show last sync info and paired servers
presync forget             Unpair from a server
```

On first run, `presync connect` shows a QR scanner (terminal-based) or prompts for the server URL. On subsequent runs it reconnects automatically using the stored token.

### File Structure (Client)

```
~/.config/presync/
├── servers.json         # trusted servers + tokens
└── sessions/            # resume state for in-progress syncs
```

---

## Server Mode (continued)

### Config File

Location: `~/.config/presync/config.toml`

```toml
[daemon]
port = 7700
auto_sync = false        # global default, overridable per profile

[[profiles]]
name = "Music"
source = "~/Music"
destination = "~/Music"          # path on the recipient device
mode = "mirror"                  # mirror | additive
auto_sync = true                 # overrides global

[[profiles]]
name = "Podcasts"
source = "~/Podcasts"
destination = "~/Podcasts"
mode = "additive"
```

`destination` is interpreted by the client — on Android the SAF bridge maps it to the appropriate storage location, on a PC it's a plain filesystem path. The server doesn't care what's on the other end.

### Sync Modes

| Mode | Behavior |
|---|---|
| `mirror` | Recipient matches server exactly. Files deleted on server are deleted on client. |
| `additive` | Files are only ever added to recipient. Deletions on server are ignored. |

### File Diff Engine

- Builds a manifest of the source folder: relative paths + file sizes + modification times + SHA-256 hash per file
- Sends manifest to client
- Client responds with its own manifest of the destination (same format)
- Server computes the delta: files to add, files to update, files to delete (if mirror mode)
- Files are considered identical if size + mtime match — hash is only computed when those differ, to avoid hashing the entire library on every sync

---

## Transfer Engine

### Checksums

Every file transfer is verified end-to-end with SHA-256:

1. Server computes SHA-256 of the source file before sending
2. File is transferred in chunks over HTTP
3. Once all chunks are received, the client computes SHA-256 of the assembled temp file
4. Client reports the hash back to the server
5. Server compares hashes — match means the file is good and gets renamed to its final name; mismatch means the file is silently retransferred (up to 3 attempts before marking as error)

This catches corruption from flaky WiFi, partial writes, or anything else that might silently mangle a file in transit.

### Resumable Transfers

Each sync session is tracked by the server in a session state file:

```
~/.config/presync/sessions/<device-id>-<profile>-<timestamp>.json
```

The session file records every file in the delta with one of three states: `pending`, `in_progress`, or `done`.

On the client side, in-progress files are written to a temp filename during transfer:

```
Wish You Were Here.flac.presync-tmp
```

Only when the checksum passes does the client atomically rename it to the real filename. This means there are never half-written real files on the recipient — either the file is complete and verified, or it's a `.presync-tmp` that gets cleaned up or resumed.

**On reconnect after a dropped connection:**

1. Client reconnects to server with its token
2. Server checks for an existing session for this device + profile
3. If found, resumes from where it left off — skipping all `done` files
4. For the `in_progress` file, server checks how many bytes the temp file already has and resumes from that byte offset (HTTP range requests)
5. `pending` files proceed normally

**Session cleanup:**

- Session file is deleted when all files reach `done` state
- Stale sessions older than 7 days are pruned on server start
- `presync sync --fresh` forces a new session, ignoring any existing one

---

## Pairing & Authentication

### First Connection

1. User runs `presync start` — server starts, listens on port 7700
2. User runs `presync pair` (or it's prompted automatically on first start)
3. Terminal displays a QR code encoding `http://<local-ip>:7700/pair?token=<session-token>`
4. **On Android**: user opens presync app, taps "Scan to pair", scans QR — **On another PC**: user runs `presync connect`, scans QR with terminal QR reader or pastes the URL
5. Both sides display the same 4-digit confirmation pin
6. User confirms on server terminal (`[y/n]`) — if confirmed, device is trusted
7. Server stores a device identity + long-lived token in `~/.config/presync/devices.json`

### Subsequent Connections

- Client sends its stored long-lived token on every request
- Server validates token against trust store
- No QR, no pin — just works

### Revoking

- `presync unpair` lists paired devices and lets you remove one
- Removing a device invalidates its token immediately

### Security Notes

- All traffic is over plain HTTP on the LAN — acceptable for a personal local tool, but worth noting
- HTTPS support can be added later (self-signed cert) if needed
- The session token in the QR is single-use and short-lived (60 seconds) — sniffing it after pairing is useless
- The long-lived token is a random 256-bit value stored on both sides

---

## Android Client (APK)

### What It Is

Android can't run the presync binary directly, so the APK fills the client role. It's minimal by design:

- A WebView that loads the server's web UI
- A `JavascriptInterface` (the "SAF bridge") that exposes file operations to the web UI
- A token store (SharedPreferences) for the long-lived auth token
- A background service to keep the WebView alive during transfers

Everything else — sync logic, progress tracking, UI — lives in the server's web UI, loaded into the WebView. Updating the server updates the Android UI too, no APK update needed.

### SAF Bridge

The web UI calls `window.AndroidBridge.writeFile(path, base64data)` etc. The bridge handles:

- Requesting Storage Access Framework permissions (first time only — user picks destination folders once, SAF grants persistent URI access)
- Writing files to the correct Android locations
- Returning manifests of destination folders back to the web UI
- Persisting folder URI permissions across app restarts

### Permissions

- No `MANAGE_EXTERNAL_STORAGE` (too broad, Play Store dislikes it)
- Uses `ACTION_OPEN_DOCUMENT_TREE` — user picks each destination folder once, SAF grants persistent access
- No other permissions needed

### APK Distribution

- Sideloaded — no Play Store needed
- Server can optionally serve the APK at `http://<ip>:7700/app.apk` for easy install
- Initial QR on the server can point to the APK download URL before the app is installed

---

## Web UI

Served by the server, rendered inside the Android WebView. Also accessible from any browser on the LAN for PC clients or debugging — though on PC the native client binary is preferred for actual syncing.

### Screens

**Home / Dashboard**
- List of sync profiles with last-synced timestamp
- Sync status (idle / syncing / error)
- Manual "Sync Now" button per profile or "Sync All"

**Sync Progress**
- Currently syncing profile name
- File count: X of Y
- Current file name
- Overall progress bar
- Cancel button

**Profiles** (read-only on client, editable on server via config file)
- List of profiles with source, destination, mode
- Per-profile sync history

**Settings**
- Auto-sync toggle
- Paired server info
- "Forget this server" (unpairs)

### Design Principles

- Clean, minimal, purposeful — no clutter
- Dark mode by default (or follows system)
- Vanilla JS + CSS — no frameworks, keeps it fast in a WebView
- No loading spinners for things that should be instant

---

## Auto-Sync (Optional, Phase 2)

When enabled (globally or per profile):

- Client detects it's on the same network as the paired server (by attempting a connection to the stored address)
- Triggers a sync automatically in the background
- Notifies when done or on error
- Respects a cooldown (don't sync more than once every N minutes)

On Android this runs as a background service. On PC it runs as a lightweight watcher process spawned by the client.

IP change problem is handled by:
- mDNS: server advertises itself as `presync.local` via mDNS (avahi on Linux, built-in on Mac, bonjour on Windows)
- Client tries `presync.local:7700` first, falls back to stored IP
- On successful connection, updates stored IP

---

## File Structure

**Server:**
```
~/.config/presync/
├── config.toml          # profiles and settings
├── devices.json         # paired device trust store
├── sessions/            # resume state per device per profile
│   └── <device-id>-<profile>-<timestamp>.json
└── logs/
    └── presync.log
```

**Client (PC):** see Client Mode section above.

---

## Phases

### Phase 1 — Core (Weekend)
- [ ] Server: HTTP server, daemon, background process
- [ ] CLI: start, stop, status, pair, unpair (server mode)
- [ ] CLI: connect (client mode, PC)
- [ ] QR + pin pairing flow (both sides)
- [ ] One sync profile (Music)
- [ ] File diff + transfer engine
- [ ] SHA-256 checksums on every file transfer
- [ ] Resumable transfers with session tracking + temp files
- [ ] Android APK: WebView shell + SAF bridge
- [ ] Web UI: home, progress, basic settings

### Phase 2 — Polish
- [ ] Multiple profiles
- [ ] Per-profile sync modes (mirror / additive)
- [ ] Auto-sync on same network (Android + PC client)
- [ ] mDNS discovery (`presync.local`)
- [ ] Sync history / logs in UI
- [ ] Cross-compile client binaries for Linux, Mac, Windows

### Phase 3 — Nice to Have
- [ ] HTTPS / self-signed cert
- [ ] Delta transfers (only changed bytes, not whole files)
- [ ] Server serves APK + client binaries for easy install
- [ ] Desktop tray icon (server mode)

---

## Open Questions

- **PC language**: Go is the recommended choice (static binary, great stdlib for HTTP + concurrency, learnable), but left open. C is viable if you want low-level practice. Rust works but has a steeper curve for marginal benefit here.
- **Web UI framework**: Vanilla JS/CSS preferred for WebView performance. Reconsider if UI complexity grows.
- **mDNS library**: platform-dependent. On Linux, avahi. Worth abstracting early.
