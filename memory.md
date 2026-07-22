# Memory — Android Pairing & Sync Fixes Complete

Last updated: 2026-06-21

## What was built

Fixed 7 root causes preventing Android app from pairing and syncing. All 152 tests pass.

**Fixes applied:**
1. **Pairing URL path** (`pair_cli.go:70`): `/pair?token=` → `/?token=` — QR code now points to root so web UI loads
2. **SPA fallback 405** (`web.go`): Rewrote `staticFileHandler` to serve `index.html` via `http.ServeContent` instead of delegating to `http.FileServer`
3. **QR scanner filter** (`PairingActivity.kt:222`): `/pair?token=` → `?token=` — recognizes new URL format
4. **Pairing race condition** (`web/app.js`, `pair_handler.go`, `pairing.go`): Removed `POST /pair/confirm` from web UI; added `GET /pair/confirmed` polling endpoint; web UI polls after showing PIN until CLI confirms
5. **Missing destination dirs** (`pair_handler.go`): Added `os.MkdirAll` during pairing confirm
6. **Hashchange listener not registered** (`web/app.js`): Added `window.addEventListener("hashchange", onHashChange)` in pairing confirmation handler
7. **Android sync progress overwritten** (`web/app.js`): Added `androidSyncActive` flag — `renderProgress` early-returns during Android sync loop

**New endpoint:** `GET /pair/confirmed` — returns `{device_id, token}` from last confirmed pairing in last 30s

## Decisions made

- **Web UI doesn't call `/pair/confirm`** — only CLI (server-side) confirms. Web UI polls `GET /pair/confirmed`.
- **`/pair/confirmed` returns token from last 30s** — `pairing.go` stores `lastConfirmedDeviceID/Token` after confirm.
- **`androidSyncActive` flag** — prevents `renderProgress` from polling server sessions during Android client-driven sync.
- **SPA fallback uses `http.ServeContent`** not `http.FileServer` — serves `index.html` for any path not matching a real file.

## Problems solved

- Android pairing loop (QR scanned → 405 → stuck) — fixed by URL path + SPA fallback changes
- Android sync stuck at "0 of 1 files" — fixed by removing `navigate("progress")` from Android sync flow
- Nav tabs not working after pairing — fixed by adding hashchange listener registration

## Current state

- Phase 1 fully complete (Tasks 01-09)
- Phase 2 Tasks 10-11 also done
- Android pairing + sync fixes complete
- Binary built: `presync` (11MB)
- APK built: `android/app/build/outputs/apk/debug/app-debug.apk`
- Go SDK: 1.24.4, Android SDK: ~/android-sdk/, JDK: openjdk 17.0.19
- 152 tests all pass (76 Go unit + 18 pairing + 45 transfer + 13 connect)

## Next session starts with

User needs to re-test Android pairing + sync end-to-end. If sync still fails, check server logs and Android WebView console. Consider making `PendingFiles()` also count `in_progress` files as pending for session resume.

## Open questions

- `PendingFiles()` only counts `pending` state, not `in_progress` — server logs show `pending=0` even when file is stuck `in_progress`. Consider fixing.
- Auto-sync toggle in Settings is UI-only — no backend wiring yet.
- "Forget this server" button in Settings is not yet implemented.
- `SaveConfig` has no unit tests.
- Android: no background service for keeping WebView/transfer alive during sync yet.
- Android: `localIP()` fallback to `localhost` if no internet route — won't work from phone.
