# Memory Log

_Compact history of past sessions, appended by `/remember save`. Not auto-injected at session
start — read this on request, or via `/remember restore` when the auto-restore summary isn't
enough. Capped at the last 20 entries; oldest entries drop off as new ones are added._

---

### 2026-11-01 — Android pull restored + push profiles

Rebuilt Android pull (SAF chunked bridge, state-aware resume, zero-byte files, retries, mirror deletion) and added push profiles (direction/destination, `/push` API, server-owned offsets, hash verification, additive/mirror parity, empty-source guard). Two review rounds fixed the bridge callback contract, resume reconciliation, mismatch offsets, symlink ordering, completion deletion, and deletion-on-error ordering. Go tests, all four E2E scripts, and the APK build pass. Device testing is still pending.

### 2026-08-30 — Security hardening + sync engine overhaul

Completed phases 1–5: auth/pairing hardening, sync-engine correctness (resume, retries, mirror deletions), real deltas for web/Android with chunked verified transfer, CLI/config cleanup, and expanded unit/E2E coverage. All Go tests, the three E2E scripts, and the Android build passed. Next planned task: 12 auto-sync on same network.
