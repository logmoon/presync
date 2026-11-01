# Memory

## Active Feature

None in progress. Task 12 — Auto-sync on same network — is built, reviewed, and closed. Next up is Task 13 — mDNS discovery.

Branch: main

## Session Phase

closing

## Next Action

Start Task 13 (mDNS discovery) with the Architect Gate: server advertises `presync.local` via `grandcat/zeroconf`, client tries `presync.local:<port>` first, falls back to the last stored IP, and writes the resolved IP back to `servers.json` on success. Before or alongside that, finish the two deferred device checks: install the new APK and verify auto-sync on the phone, and check whether the 6.8G `music` pull completed so its additive/zero-delta follow-ups can run.

## Decisions & Problems Not Yet Elsewhere

- Auto-sync is committed on `main`: `e61ff85 feat: add auto-sync on same network`; `a56082e fix: serialize local-token creation across processes`. Server config owns eligibility (`[daemon] auto_sync`, `auto_sync_cooldown_minutes`, per-profile `auto_sync` pointer; push never eligible); `/auto-sync` returns `{cooldown_minutes, profiles}`. PC uses detached `presync watch` (`--status`, `--stop`, `--once`; cooldown persisted in `servers.json`). Android uses a `dataSync` foreground service with a hidden WebView loading `?autosync=1`; the notification has a "Turn off auto-sync" action. The service and visible WebView share an owner-scoped manual-sync lock.
- Review round 1: SHIP WITH FIXES (two IMPORTANT Android lifecycle races + the local-token race). All fixed; round 2: SHIP, no regressions. `scripts/test-watch.sh` adds 10 E2E checks and all five scripts pass.
- Auto-sync on-device verification is still pending. Test: rebuild/install `android/app/build/outputs/apk/debug/app-debug.apk`; set `[daemon] auto_sync = true` (optionally per-profile overrides) and restart the server; open the app, Settings → enable Auto-sync; background the app; toggle WiFi or wait ≤60s; verify sync + notification, tap "Turn off auto-sync", and confirm a second trigger inside the cooldown does nothing (including after an app restart).
- The previous session's music-pull follow-ups still stand: check whether the full 6.8G pull completed; then run the additive check and a final zero-delta sync. The 30-minute inactivity rule can discard a stalled session and force a full re-transfer because no baseline was written yet.
- Pre-existing uncommitted tooling changes (`.opencode/**`, `AGENTS.md`, `opencode.json`) are not part of the auto-sync commits and are still in the worktree; do not include them accidentally.

## Open Questions

- Does auto-sync work end to end on the real phone (network callback, notification action, cooldown across process restarts)?
- Did the music pull complete, and do the additive check + final zero-delta behave?
- Does the Progress list render correctly on-device (live + paused cards together) after reloading the new `app.js`?
