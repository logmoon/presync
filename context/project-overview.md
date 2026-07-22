# Project Overview — presync

## About the Project

`presync` is a one-way, profile-based file sync tool for local WiFi networks. One Go binary plays two roles — server (the master copy, hosting files and a web UI) and client (any recipient device) — selected by which command you run. The server holds the source of truth for one or more "profiles" (a source folder + destination + sync mode); clients pair with the server once via QR code and PIN, then pull deltas on demand or automatically whenever they're on the same network. A minimal Android APK fills the client role on mobile, using a WebView to load the server's own web UI rather than shipping a separate native app to maintain. There's no cloud, no account system, and no internet dependency — everything happens over the LAN.

## The Problem It Solves

Moving a folder (a music library, podcast backlog, document set) between your own devices today means either a cloud sync service you don't want to pay for or trust with the data, a USB cable and manual drag-and-drop, or an ad-hoc script that breaks the moment a transfer gets interrupted. `presync` removes the manual step entirely: pair once, then every sync after that is "did anything change, send only that" with verified, resumable transfers — with zero cloud dependency and zero accounts.

## Pages / Screens

**Web UI** (served by the server, loaded in the Android WebView, also reachable from any LAN browser):
```
Home / Dashboard      — profile list, last-synced time, sync status, Sync Now / Sync All
Sync Progress         — active profile, X of Y files, current filename, progress bar, cancel
Profiles              — read-only list (source, destination, mode) + per-profile history
Settings              — auto-sync toggle, paired server info, "Forget this server"
```

**CLI** (PC, server mode):
```
presync start | stop | status | sync | sync --fresh | pair | unpair | logs
```

**CLI** (PC, client mode):
```
presync connect | connect <address> | status | forget
```

## Core User Flow

**Pairing (one-time, per device)**
1. User runs `presync start` on the machine that will hold the master files.
2. User runs `presync pair` (or is prompted on first start) — a QR code renders in the terminal.
3. On the recipient: Android scans via the app's "Scan to pair"; PC runs `presync connect` and scans/pastes the URL.
4. Both sides show the same 4-digit PIN; user confirms `[y/n]` on the server terminal.
5. Server issues a long-lived 256-bit token, stored on both sides. No further pairing needed for that device.

**Sync (every time after)**
1. User triggers sync manually (`presync sync`, "Sync Now" in the UI) or auto-sync fires.
2. Client connects with its stored token; server validates it.
3. Server builds a manifest of the profile's source folder; client reports its destination manifest.
4. Server computes the delta (add / update / delete-if-mirror) and the transfer begins, file by file, with checksum verification and resumability.
5. Client and UI report completion; session state is cleared once every file reaches `done`.

## Features In Scope

- Single Go binary acting as server and client on Linux, Mac, Windows
- Minimal Android APK (WebView + SAF bridge) as the mobile client
- QR + PIN device pairing with long-lived token trust store, and device unpairing
- Multiple named sync profiles, each with its own source, destination, and mode
- `mirror` mode (recipient matches server exactly, deletions propagate) and `additive` mode (recipient only ever gains files)
- File diff engine using size + mtime, falling back to SHA-256 only when those differ
- Chunked file transfer over HTTP with end-to-end SHA-256 verification and automatic retry (up to 3 attempts) on mismatch
- Resumable sync sessions: per-file state tracking (`pending` / `in_progress` / `done`), byte-offset resume via HTTP range requests, `.presync-tmp` staging files with atomic rename on verified completion
- Stale session pruning (7 days) and `--fresh` flag to force a clean session
- Background daemon mode for the server (`start` / `stop` / `status` / `logs`)
- Web UI: dashboard, sync progress, profiles (read-only on client), settings
- Auto-sync on same network (Phase 2), with cooldown
- mDNS discovery (`presync.local`) so clients survive IP changes (Phase 2)
- Cross-compiled client binaries for Linux/Mac/Windows (Phase 2)
- HTTPS / self-signed cert, byte-level delta transfers, server-hosted APK + binary downloads, desktop tray icon (Phase 3 — stretch)

## Features Out of Scope

- **Bidirectional sync** — the server is always master; clients never push changes back, only manifests of what they currently have. There is no conflict resolution because conflicts can't occur.
- **Cloud relay or sync over the internet** — presync only ever operates on the local network. There is no remote server, no NAT traversal, no off-LAN access.
- **iOS support** — Android is the only mobile target; no iOS app is planned.
- **Real-time / continuous file watching** — sync is triggered manually, on a schedule, or on network-detect (Phase 2 auto-sync), not via filesystem watchers reacting to every change.
- **User accounts or multi-tenant access** — devices are trusted individually via token; there's no login system or per-user permissions model.
- **Encryption at rest** — files are stored as plain files on both ends; presync secures the transfer, not the storage.

## Target User

A single technical user managing sync between their own personal devices — for example, mirroring a music or podcast library from a home server/desktop to a phone and a laptop — who wants this to work without a subscription, without trusting a third party with the data, and without manually copying files over a cable. Doubles as a personal project for learning Go.

## Success Criteria

- A profile syncs from server to a clean client with zero pre-existing files, and every resulting file's SHA-256 matches the source.
- Killing the connection mid-transfer and reconnecting resumes correctly: completed files are not re-sent, the in-progress file resumes from its existing byte count, and no corrupted or half-written file is ever left at its real filename.
- Pairing a new device requires the QR + PIN confirmation exactly once; every sync after that requires no manual confirmation.
- `mirror` mode deletes recipient files that were removed at the source; `additive` mode never deletes anything on the recipient, even when the source file is gone.
- The same binary, built once per OS, runs correctly as both `presync start` (server) and `presync connect` (client) on Linux, Mac, and Windows.
- The Android APK installs via sideload (no Play Store) and can write into a user-selected SAF folder without requesting broad storage permissions.
- `presync unpair` immediately invalidates a device's token — a subsequent sync attempt from that device is rejected.
- The server daemon survives terminal closure and can be stopped cleanly with `presync stop`.
