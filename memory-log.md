# Memory Log

_Compact history of past sessions, appended by `/remember save`. Not auto-injected at session
start — read this on request, or via `/remember restore` when the auto-restore summary isn't
enough. Capped at the last 20 entries; oldest entries drop off as new ones are added._

---

### 2026-11-01 — Auto-sync on same network

Built server-owned auto-sync eligibility (`[daemon] auto_sync` + cooldown + per-profile override, `/auto-sync`), a detached PC `presync watch` (status/stop/once, persisted cooldown), and an Android `dataSync` foreground service that reuses the web sync engine through a hidden WebView (`?autosync=1`). Added the real Settings toggle (disabled when the server disallows it) and an owner-scoped single-sync lock. Review round 1 found two Android lifecycle races plus a local-token race; all fixed and round 2 shipped clean. Go tests, all five E2E scripts, and the APK build pass; on-device auto-sync verification remains.

### 2026-11-01 — manual E2E + Progress session list

Push and small pull verified end to end on the phone (baseline, changed-file-only upload, stop/resume at exact offsets, mirror delete only after complete, zero-delta repeats); all isolated E2E scripts pass. Fixed the Progress screen to list every session (live, paused, server-side) with per-card Stop/Resume; review found one pulse-dot leak, fixed. Music pull accepted with completion/additive checks deferred.

### 2026-11-01 — Pairing bridge, baseline resume, progress UI

Fixed the Android pairing hang (WebView method called on the JavaBridge thread). Added a per-device last-synced baseline so resume/repeat syncs no longer re-transfer everything, fixed resume reconciliation against source metadata, and unified the progress UI: Stop leaves a paused card with Resume, Home shows Syncing/Sending disabled, counts resume from session states, and the current file is shown. All Go tests and the four E2E scripts pass; all four review findings were fixed. Device testing is mid-flight — the music pull is running/resumable; push and the small-profile resume/mirror tests remain.


### 2026-11-01 — Android pull restored + push profiles

Rebuilt Android pull (SAF chunked bridge, state-aware resume, zero-byte files, retries, mirror deletion) and added push profiles (direction/destination, `/push` API, server-owned offsets, hash verification, additive/mirror parity, empty-source guard). Two review rounds fixed the bridge callback contract, resume reconciliation, mismatch offsets, symlink ordering, completion deletion, and deletion-on-error ordering. Go tests, all four E2E scripts, and the APK build pass. Device testing is still pending.

### 2026-08-30 — Security hardening + sync engine overhaul

Completed phases 1–5: auth/pairing hardening, sync-engine correctness (resume, retries, mirror deletions), real deltas for web/Android with chunked verified transfer, CLI/config cleanup, and expanded unit/E2E coverage. All Go tests, the three E2E scripts, and the Android build passed. Next planned task: 12 auto-sync on same network.
