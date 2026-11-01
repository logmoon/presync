# Architecture — presync

## Stack

| Layer | Tool | Purpose |
|---|---|---|
| Language (server + PC client) | Go | Single cross-compiled binary, stdlib HTTP + concurrency |
| Web framework | None — `net/http` stdlib | No router/framework needed for this surface area |
| Mobile client | Kotlin (Android) | Thin WebView shell + SAF bridge only |
| Web UI | Vanilla JS + CSS | Served by the server, loaded in WebView and any LAN browser |
| Styling | Vanilla CSS (no framework) | Keeps it fast inside a WebView |
| Auth | Custom token pairing (QR + PIN handshake) | No accounts; per-device long-lived 256-bit tokens |
| Database | None | Flat files: `config.toml`, `devices.json`, session JSON |
| Storage (PC) | Local filesystem | Plain paths from `config.toml` |
| Storage (Android) | Storage Access Framework (SAF) | Persistent URI grants per destination folder |
| QR generation | `go-qrcode` (or equivalent) | Terminal-rendered QR for pairing |
| mDNS | `grandcat/zeroconf` (Go, cross-platform) | `presync.local` advertise/discover — Phase 2 |
| Deployment (PC) | Native binaries, `GOOS`/`GOARCH` cross-compile | One build step → Linux/Mac/Windows binaries |
| Deployment (Android) | Sideloaded APK, optionally served by the server at `/app.apk` | No Play Store |

## Folder Structure

```
presync/
├── cmd/
│   └── presync/
│       └── main.go          # CLI entrypoint: parses args, dispatches to server or client mode
├── internal/
│   ├── server/               # Server-mode only: daemon lifecycle, HTTP handlers, profile manager
│   │   ├── daemon.go         # start/stop/status, background process management
│   │   ├── sync_handler.go   # pull endpoints: manifest exchange, file download, confirm/resume
│   │   ├── push_handler.go   # push endpoints: source manifest, chunk upload, verify, mirror deletes
│   │   ├── pair_handler.go   # pairing HTTP surface
│   │   ├── web.go            # web-UI data endpoints and server-initiated sync
│   │   └── trust_middleware.go # token scope + loopback admin gates
│   ├── client/                # Client-mode only: connect, pull manifest, write files
│   │   ├── connect.go        # establishes connection, sends stored token, drives a sync
│   │   └── apply.go          # writes received files to local filesystem (PC) via temp+rename
│   ├── diff/                  # Pure computation — no I/O
│   │   └── diff.go           # builds manifests, computes delta (add/update/delete)
│   ├── transfer/              # Chunked transfer + checksum + resume, on both sides
│   │   ├── transfer.go       # chunked send/receive over HTTP, range-request resume
│   │   └── session.go        # session state file read/write (pending/in_progress/done)
│   ├── pairing/                # QR + PIN + token issuance, used only during pairing
│   │   └── pairing.go
│   ├── mdns/                   # mDNS advertise (server) / discover (client) — Phase 2
│   │   └── mdns.go
│   └── config/                  # Owns all on-disk config/state file formats
│       └── config.go         # config.toml, devices.json, sessions/*.json — nothing else touches these directly
├── web/                       # Static web UI, served as-is by internal/server/http.go
│   ├── index.html
│   ├── app.js
│   └── style.css
├── android/                   # Separate Kotlin/Gradle module — the APK
│   └── app/src/main/
│       ├── MainActivity.kt    # WebView host, loads server's web UI by URL
│       ├── SafBridge.kt       # JavascriptInterface: exposes file ops to the loaded web UI
│       └── res/
├── go.mod
└── go.sum
```

## System Boundaries

| Folder / Layer | Owns | Must Never |
|---|---|---|
| `cmd/presync` | CLI argument parsing and dispatch | Contain business logic — it only calls into `internal/server` or `internal/client` |
| `internal/server` | Daemon lifecycle, HTTP routes, profile + trust management | Know anything about the client's actual filesystem beyond what's in a reported manifest |
| `internal/client` | Connecting to the server, writing files locally | Decide mirror-vs-additive logic itself — the server's delta dictates what happens |
| `internal/diff` | Manifest building, delta computation | Perform any network or disk I/O — pure computation over two manifests in, a delta out |
| `internal/transfer` | Chunked transfer, checksum verification, resume | Decide *which* files to transfer — that's `diff`'s output, `transfer` only executes it |
| `internal/pairing` | QR rendering, PIN confirmation, token issuance | Touch sync/transfer logic in any way |
| `internal/config` | Reading/writing `config.toml`, `devices.json`, `servers.json`, `local-token` | Be bypassed — no other package reads or writes these files directly. Session file CRUD is owned by `internal/transfer` (see invariant 11) |
| `web/` | UI rendering only | Call into Go directly — it only ever talks to the server via HTTP endpoints |
| `android/` | WebView shell, SAF file operations exposed via JS bridge | Contain sync logic — that lives entirely in the loaded web UI / server |

## Data Flow

### Flow: Device Pairing
```
User (server) → presync pair → pairing.go generates session token (60s TTL) + QR
User (client) → scans QR / runs connect → client sends session token to server's /pair endpoint
Server → displays 4-digit PIN, client UI displays same PIN (derived from session token)
User confirms [y/n] on server terminal
Server → trust.go issues 256-bit long-lived token → writes to devices.json
Client → stores token in servers.json (PC) / SharedPreferences (Android)
```

### Flow: Sync Execution (mirror profile)
```
Client → connect.go sends stored token to server
Server → trust.go validates token
Server → profiles.go loads profile config → diff.go builds source manifest
Client → apply.go builds destination manifest, sends to server
Server → diff.go computes delta (add / update / delete)
Server → transfer.go streams each file in chunks, computing SHA-256 as it sends
Client → apply.go writes to <file>.presync-tmp, computes SHA-256 on completion
Client → reports hash back to server
Server → compares hashes:
  match    → instructs client to atomically rename temp → real filename, marks file 'done' in session.go
  mismatch → retransfer (up to 3 attempts), else mark 'error'
Server → (mirror only) instructs client to delete files absent from source manifest
Session file deleted once every file reaches 'done'
```

### Flow: Resume After Dropped Connection
```
Client → reconnects, sends stored token
Server → trust.go validates token
Server → session.go checks for existing session file for this device + profile
  found → reconcile the session with the freshly computed delta: done files that
          the destination no longer matches are re-queued, new source files are
          added, and files removed from the source are dropped
        → for the 'in_progress'/re-queued files, the client resumes from its temp
          file's byte count
        → remaining 'pending' files proceed normally
  not found → behaves as a fresh sync
`presync sync --fresh` forces a new session regardless of existing state
```

### Flow: Push Execution (client → server, additive or mirror)
```
Client → builds a manifest of its locally chosen source folder
Client → POST /push {device_id, profile, source_manifest}
Server → diff.go computes the delta against the profile's destination folder
Server → creates a push session; session JSON stores the upload list, per-file
         expected sizes, and (mirror only) the planned deletion list
Client → reads each pending file in chunks natively and POSTs each chunk with
         the server-reported offset
Server → appends to <destination>/<file>.presync-tmp, rejecting offset drift
Client → hashes the source file natively, POSTs /push/confirm
Server → hashes the temp file; on match atomically renames into place and marks
         the file done; on mismatch deletes the temp and asks for a retry
Client → POST /push/complete once every file is done
Server → (mirror only) applies the persisted deletion list, then deletes the
         session. An empty source manifest is refused when the destination has
         files, so a lost SAF grant can never wipe the server copy.
```

## Database Schema

None. State is stored as flat files, owned exclusively by `internal/config`:

- `config.toml` — profiles and daemon settings (see Config below)
- `devices.json` — paired device identities + long-lived tokens
- `sessions/<device-id>-<profile>-<timestamp>.json` — per-file sync state (`pending` / `in_progress` / `done`), the profile `direction`, the expected file `size` for push uploads, and (mirror push only) the planned `to_delete` list

## Authentication

- **Provider:** none external — presync issues and validates its own tokens.
- **Pairing handshake:** QR-encoded single-use session token (60s TTL) + a 4-digit PIN both sides display and the user confirms manually. This is the only point requiring human interaction. The session token is consumed on first `/pair/accept` — a second device scanning the same QR is rejected.
- **Steady state:** client sends its stored 256-bit long-lived token on every request; server validates against `devices.json`. No QR, no PIN, no further confirmation.
- **Protected routes:** every endpoint except `/pair`, `/pair/accept`, `/pair/confirmed` (session-token bound), `/pair/destinations` (session-token bound), `/web/token` (scoped bootstrap), and static files requires a valid token.
- **Token scoping:** the local token served by `/web/token` is only accepted on web-UI routes (`/web/*`, `/profiles/*`, `/status`) and — over loopback only — on `/sync*` and `/push*` (CLI/self-hosted tests). A remote caller can never use it to download or upload files. Pairing admin actions (`/pair/confirm`, `/pair/discard`, `/pair/status`, `/reload`) require the local token **and** a loopback connection.
- **Revocation:** `presync unpair` removes the device from `devices.json`; its token is rejected on the very next request — no grace period. Unpair also removes the device's destination entries from `config.toml` and deletes its session files.
- **Transport:** plain HTTP on the LAN for Phase 1 (documented, accepted risk for a personal local tool). HTTPS with a self-signed cert is a Phase 3 addition — no Phase 1/2 code should assume or hardcode `https://`.

## Invariants

1. Each profile has exactly one writer. Pull profiles are written by the server; push profiles are written by the paired client that owns the source. No profile accepts writes from both directions, so conflicts cannot occur.
2. A file is never marked synced until the client-reported SHA-256 hash matches the server's hash for that file — a mismatch always triggers a retry (max 3), never silent acceptance.
3. In-progress transfers always write to `<filename>.presync-tmp`. The real filename only ever appears via an atomic rename, and only after checksum verification passes.
4. `mirror` mode deletions happen only on the receiving side, only after the full delta is known, and only after all transfers for the session have succeeded: a pull client applies them after the file phase, and the server applies a push session's persisted delete list only on successful `/push/complete`. `additive` profiles never delete a recipient file under any circumstance.
5. The QR pairing token is single-use (consumed on first `/pair/accept`) and expires after 60 seconds; the long-lived token is a 256-bit random value, generated once per device at confirmed pairing.
6. Unpairing a device invalidates its token immediately on the server — no cached or delayed validity anywhere in the request path. Unpair also removes the device's destination entries and session files.
7. The server never assumes anything about the destination filesystem's shape — `destination` path interpretation belongs entirely to the client (plain path on PC, SAF URI on Android). The server's `Destinations` map is keyed by device ID; resolution falls back to the legacy name key.
8. `internal/diff` performs zero network or disk I/O — it is pure computation over two manifests in, a delta out, and must stay unit-testable without mocks.
9. A sync must be resumable purely by reading the session JSON plus checking the in-progress temp file's byte count — no other hidden in-memory state is allowed to be required for resume correctness.
10. No Phase 1 or Phase 2 code path hardcodes or assumes an `https://` scheme; transport is plain HTTP by design until Phase 3.
11. `internal/config` is the only package permitted to read or write `config.toml`, `devices.json`, `servers.json`, and `local-token` directly; `internal/transfer` owns all session-file CRUD (via `config.SessionsDir()`). All other packages go through these two.
12. Stale session files older than 7 days are pruned on server start, and `presync sync --fresh` must always be honored as "ignore any existing session, start clean" regardless of session age. The `fresh` flag is also honored server-side for remote clients. Incomplete sessions are considered stale (and resumable state discarded) after 30 minutes of inactivity.
13. A mirror push never deletes anything when the client reports an empty source manifest while the destination contains files, and never before every upload in that session has been hash-verified and the session completed.
