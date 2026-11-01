# Memory

## Active Feature

Manual end-to-end verification of the Android pull/push client after the pairing-bridge, resumable-baseline, and progress/Resume UI fixes.

Branch: main

## Session Phase

closing

## Next Action

Resume the `music` pull on the phone (it is running/paused around 151+/1235; session `8bc052af7b3ebe88-music-20261101T181657...`). Then finish the manual tests: push (pick a phone source folder via SAF, send, repeat-send, stop/resume, mirror delete), `small` pull (baseline, drop/resume with a big file, mirror delete), additive check with `~/Music/zz-presync-additive.txt`, and a second Sync Now per profile asserting zero transfers.

## Decisions & Problems Not Yet Elsewhere

- The old music session went stale (>30 min) and was discarded; the current session started fresh, so ~83 files already on the phone re-transfer once. Expected.
- Phone destination is `Sync/Music`; pull paths are `Sync/Music/<profile>/...`. Push destination on the PC is `~/presync-test/push-recv`.
- Home now shows `Syncing…`/`Sending…` disabled while a local sync runs, `Resume sync`/`Resume upload` for paused sessions, and disables all sync/push actions during a run.
- Pairing bridge fix: never read `webView.url` off the UI thread; SafBridge uses a `@Volatile currentUrl` kept in sync by MainActivity.
- Review findings fixed: legacy sessions hash-verify before seeding metadata; empty push sources reconcile the baseline; completed/stale sessions are reaped with a grace window; server-driven cards say “in progress”.
- Verified on-device: pairing, music pull progress, keep-screen-awake, Stop → paused card with Resume, Home disabled/Syncing state, Resumed twice successfully.

## Open Questions

- Does the full 6.8G music pull complete cleanly with the new baseline?
- Push SAF folder picking, stop/resume of a large upload, and mirror delete are still untested on-device.
- The keep-screen-on flag after a WebView reload mid-sync (native lifecycle does not clear it) is unverified; PairingActivity’s one-shot guard is fine as long as proceedWithUrl always navigates.
