# Progress Tracker

## Current Status

**Phase:** Phase 2 — Polish
**Last completed:** 12 Auto-sync on same network — reviewed and closed (Android on-device verification deferred)
**Next:** 13 mDNS discovery

---

## In Progress

None. Android auto-sync still needs a real-device check (network join, notification action, cooldown across process restart); the code, Go tests, PC E2E, and APK build are complete.

## Up Next

- 13 mDNS discovery
- 14 Sync history / logs in UI
- 15 Cross-compile client binaries

## Blocked

None.

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
- [x] 11A Push profiles (client → server)
- [x] Manual end-to-end device test (pull + push)
- [x] 12 Auto-sync on same network (PC E2E verified; Android on-device check deferred)
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
- **Auth middleware split** (Phase 1): `withAuth` (device tokens, + local token over loopback only) on `/sync*`; `withWebAuth` (device or local) on `/web/*`, `/profiles/*`, `/status`; `withLocalAdmin` (loopback + local token) on `/pair/confirm|discard|status`, `/reload`. The unauthenticated `/web/token` bootstrap can never yield file-download capability remotely.
- **Pairing session-token binding** (Phase 1): `/pair/confirmed` and `/pair/destinations` require the QR session token; QR token is consumed on first `/pair/accept` (single-use, invariant 5 now enforced).
- **Local token atomicity** (Phase 1): `O_CREATE|O_EXCL` create + race-loser re-reads winner; truncated/corrupt files are healed; only 64-char hex tokens accepted.
- **Session identifiers confined** (Phase 1): `transfer.ResolveSession` maps client-supplied session values to basenames inside `SessionsDir`; `device_id`/`profile` with path separators rejected at `/sync` start.
- **Stuck-in_progress fix** (Phase 2): `handleSyncFile` validates source + hashes BEFORE marking `in_progress`; offset > size → 416; failures leave the file pending. New `POST /sync/error` lets clients mark a file errored after retries (state machine terminal). `PendingFiles()` counts `in_progress`.
- **In-run retry** (Phase 2): clients retry each file up to `MaxRetries` within one run; `error`-state files stay re-fetchable on later runs (per-run cap).
- **Server-side `--fresh`** (Phase 2): `fresh` field in `/sync` body; works for remote clients. `connect --fresh` allowed on reconnect.
- **SHA-256 ambiguity fallback** (Phase 2): fresh sessions include size+mtime-identical files with server-computed hashes; clients verify their local copy and confirm without transfer when it matches.
- **Mirror deletions executed** (Phase 2): resume recomputes the delta (deletions delivered on resume); PC client + Android bridge (`deleteFile`) apply `delta.to_delete` after the file phase.
- **Staleness 30 min** (Phase 2): `activeSessionTimeout` 60s → 30 min so interrupted syncs survive; `/web/sessions` delete-gate uses it.
- **`/web/sync` real deltas** (Phase 3): no pre-created full-profile sessions; the background `SyncProfile` computes the delta; guard moved before state creation (double-trigger → 409).
- **Destination resolution rules** (Phase 3/4): `"local"` only for `deviceID=="local"`; webDeviceID gets no override; registered devices resolve by device ID (legacy name-key fallback); unregistered devices keep their own path — the old "local" fallback that misdirected remote clients is gone.
- **Android chunked transfer** (Phase 3): 512KB ReadableStream chunks → `writeChunk` (append to `.presync-tmp`) → `finishFile` returns the native SHA-256 (real end-to-end verification, no more echoing the server header) → confirm → `renameFile` only on success; `abortFile` on failure; resume from `tempFileSize` + offset. `buildManifest` (recursive SAF walk) sends real destination manifests.
- **Android lifecycle** (Phase 3): transient errors show a Retry screen — never auto-wipe pairing data/SAF grant; persistable URI grant released on forget; `allowBackup=false`; `allowFileAccess=false`; cross-origin navigation blocked; bridge origin-gated.
- **`presync forget [address]`** (Phase 4): first production caller of `RemoveServer`; `connect <address>` (no token) reconnects to that stored server instead of always `servers[0]`.
- **Destinations keyed by device ID** (Phase 4): pairing writes `Destinations[deviceID]`; legacy name keys upgraded on confirm and honored in resolution.
- **Unpair cleanup** (Phase 4): removes destination entries (ID + name keys) and deletes the device's session files.
- **SaveConfig validates** (Phase 4): mode/port/log-level/empty/duplicate/path-unsafe profile names rejected before write; pair confirm reloads config from disk before mutating (no stale in-memory clobber).
- **Device IDs 8 bytes** (Phase 4): 16 hex chars, collision-checked on add.
- **Android pull restored**: SafBridge implements the full chunked contract (`buildManifest`, `hashFile`, `tempFileSize`, `writeChunk`, `finishFile`, `renameFile`, `abortFile`, `deleteFile`). Done files are skipped via a new `state` field instead of re-hashed; ambiguous files are verified natively.
- **Push profiles** (Phase 5): `direction = "pull" | "push"`, push `destination`; separate `/push*` API; server-owned offsets and temp files; client-hash + server-hash verification before atomic rename; additive/mirror parity. Mirror deletes are persisted in the session and applied only on successful complete; empty source is refused when the destination has files.
- **Push resume merges**: a resumed push session accepts newly added source files, drops files deleted from the source, and refreshes expected sizes.
- **Push profile binding**: push profiles are excluded from `/profiles` (pull discovery), `presync pair --dest`, and `presync sync`; a pull profile can never be pushed and vice versa.
- **Bridge callbacks are names**: `web/app.js` registers global callbacks via `bridgeCallback()` and passes the name; Android receives a string, never an inline function.
- **Resume reconciliation** (both directions): the session is reconciled against the fresh delta on start — changed/missing done files re-queue, new files are added, removed source files are dropped.
- **Completion deletes the resolved session path**: handlers use `sess.Path`, never the raw client-supplied identifier.
- **Manifest paths are slash-normalized**: `diff.BuildManifest` uses `filepath.ToSlash`, keeping Windows/Android/push manifests comparable.
- **Deletion ordering**: pull clients and Android skip mirror deletions and completion when any file failed; the session stays resumable. Push mirror deletes only after every upload is verified and complete succeeds.
- **Push path safety**: containment is checked lexically and against resolved symlinks; symlinked temp files are rejected.
- **Last-synced baseline** (Phase 5 fix): recipient mtimes are never preserved (Android SAF writes a new one), so the old delta classified every synced file as changed and resume/repeat syncs re-transferred everything. The server now keeps `state/<device-id>-<profile>.json` with the source metadata of what the device actually got, computes deltas against it (`diff.ComputeDeltaWithBase`), and reconciles resume state against source metadata instead of destination mtimes. Pull and push both use it; web-initiated syncs key the baseline by a per-destination device ID.
- **Progress state** (Phase 5 fix): one Progress card renderer drives live, paused, server-driven, and completed states; counts initialize from session states (no restart at zero), the current filename is shown, Stop leaves a paused card with Resume, and `/web/sessions` is device-filtered with push sessions included.
- **Manual E2E results** (Phase 5): push verified end to end (5 files including a 30MB `big.bin`, changed-file-only re-upload, Stop at 75% → Resume continued at the server offset, zero-delta repeat, mirror delete applied only after complete). Small pull verified the same way (baseline, Stop at 38% → resume at offset, mirror delete, zero-delta). All four isolated E2E scripts pass (transfer 48, connect 13, push 29, pairing 19).
- **Auto-sync split** (Task 12): the server's `config.toml` owns eligibility (`[daemon] auto_sync`, `auto_sync_cooldown_minutes`, per-profile `auto_sync` override; push never eligible); each client owns the master switch. PC uses a detached `presync watch` process (30s health poll, persisted cooldown in `servers.json`); Android uses a `dataSync` foreground service with a hidden WebView loading `?autosync=1`, reusing the normal JS pull path. The service and visible WebView share an owner-scoped manual-sync lock. Last trigger time is recorded before syncing, so failures also respect the cooldown.
- **Progress shows every session** (Phase 5 fix): `renderProgressView` lists the live local card, the locally paused session, and every non-complete `/web/sessions` entry, deduped by profile+direction. The poll now runs during a local transfer too, per-card Stop/Resume replace the single-card ids, Resume is disabled while a sync runs, and the nav pulse clears when nothing is active. Verified headlessly with two sessions rendered newest-first.

---

## Notes

_Add notes here as the build progresses._
