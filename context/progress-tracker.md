# Progress Tracker

## Current Status

**Phase:** Phase 2 — Polish
**Last completed:** 11 Per-profile sync modes (mirror / additive)
**Next:** 12 Auto-sync on same network

---

## Progress

### Phase 1 — Core (Weekend)

- [x] 01 Server daemon
- [x] 02 File diff engine
- [x] 03 Single sync profile config
- [x] 04 QR + PIN pairing + trust store
- [x] 05 Transfer engine + checksums + sessions
- [x] 06 Server CLI commands
- [x] 07 Client connect (PC)
- [x] 08 Web UI — home, progress, settings
- [x] 09 Android APK — WebView shell + SAF bridge

### Phase 2 — Polish

- [x] 10 Multiple profiles
- [x] 11 Per-profile sync modes (mirror / additive)
- [ ] 12 Auto-sync on same network
- [ ] 13 mDNS discovery
- [ ] 14 Sync history / logs in UI
- [ ] 15 Cross-compile client binaries

### Phase 3 — Nice to Have

- [ ] 16 HTTPS / self-signed cert
- [ ] 17 Delta transfers (byte-level)
- [ ] 18 Server-hosted APK + binary downloads
- [ ] 19 Desktop tray icon (server mode)

---

## Decisions Made During Build

_Add decisions here as they are made during implementation._

- PC/server language: **Go** (chosen over Rust/C for cross-compilation simplicity and learnability).
- UI accent direction: dark-neutral base with a single warm terracotta accent (`#D97757`), used sparingly — not a literal copy of any existing product's palette, just the same general warm-dark feel.
- TOML library and QR library left as "or equivalent" in library-docs.md — confirm exact package choice on first use and update that file.
- mDNS on Windows needs verification once Phase 2 starts (see open note in library-docs.md under grandcat/zeroconf).
- **Cross-platform from the start:** all Go packages ship with `_unix.go` and `_windows.go` build-tagged files from the first task. No Unix-only-first.
- **Build order reordered:** pure logic (diff engine) first, then config, then pairing/trust, then transfer/sessions, then CLI wiring, then client, then UI, then Android. Original plan had CLI commands before diff engine — changed because CLI would have been all stubs.
- **QR library:** `skip2/go-qrcode` — confirmed working, added to go.mod.
- **Pairing-via-HTTP:** CLI talks to the running daemon over HTTP (`POST /pair`, `/pair/accept`, `/pair/confirm`). Token generation and `devices.json` persistence stay server-side. CLI is a thin QR renderer + confirmation prompt. Keeps the daemon as the single source of truth for all state.
- **Transfer chunk size:** 256KB — balances HTTP overhead (256 requests per 64MB) and resume granularity.
- **Session files:** `~/.config/presync/sessions/<device-id>-<profile>-<timestamp>.json`. `internal/config` provides `SessionsDir()`; `internal/transfer` owns all session CRUD.
- **Server-driven sync:** server builds source manifest, receives client's destination manifest, computes delta, serves files. Client is thin — fetches, writes, confirms.
- **SHA-256 verification:** server precomputes hash before streaming, sets `X-Presync-SHA256` header before writing body. Client also computes independently; server recomputes on confirm for double-check.
- **Session pruning:** managed by `RunDaemon` with a ticker goroutine that stops on SIGTERM, not an unmanaged `init()` goroutine.
- **Local auth token:** `presync sync` authenticates via a local token stored in `~/.config/presync/local-token`, checked by `withAuth` middleware alongside `devices.json`. Keeps daemon as single source of truth, avoids polluting the paired-device trust store.
- **`--fresh` flag is client-side:** Deletes existing sessions before calling `/sync`. Works for server self-sync (Task 06) and PC client (Task 07) since both have filesystem access. Remote clients without filesystem access will need a server-side `fresh` parameter in a future task.
- **Test split:** unit tests via `go test ./...`, E2E integration via standalone bash scripts in `scripts/`. No Go wrappers around bash — two separate test systems, each doing what it's good at.
- **Pairing via text URL:** user copies pairing URL from server terminal, pastes into `presync connect <url>`. No terminal QR scanning.
- **Profile discovery:** `GET /profiles` endpoint on server returns profile names. Client queries on each connect and syncs all returned profiles.
- **`ensureHTTP` helper:** address stored in `servers.json` as `host:port` (no scheme). `ensureHTTP` adds `http://` prefix when building URLs for HTTP requests.
- **Unified destinations model:** `Profile.Destination string` → `Profile.Destinations map[string]string`. Server is single source of truth for where files go on all recipients. Destinations written to config.toml during pairing confirm, keyed by actual device name.
- **`--to` for sync filter:** `presync sync` sends to all, `presync sync --to laptop --to phone` filters. Multiple `--to` flags supported. Web UI shows clickable destination chips with multi-select.
- **Web UI doesn't call `/pair/confirm`** — only CLI (server-side) confirms. Web UI polls `GET /pair/confirmed`.
- **`/pair/confirmed` returns token from last 30s** — `pairing.go` stores `lastConfirmedDeviceID/Token` after confirm.
- **`androidSyncActive` flag** — prevents `renderProgress` from polling server sessions during Android client-driven sync.
- **SPA fallback uses `http.ServeContent`** not `http.FileServer` — serves `index.html` for any path not matching a real file.

---

## Notes

_Add notes here as the build progresses._
