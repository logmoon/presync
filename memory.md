# Memory

## Active Feature

Task 12 — Auto-sync on same network (next up). The manual device E2E and the Progress multi-session list UI are closed and accepted.

Branch: main

## Session Phase

closing

## Next Action

Start Task 12 with the Architect Gate: auto-sync on same network — client periodically attempts its stored server address, triggers a sync on success respecting a cooldown (no more than once per N minutes); Android background service, PC lightweight watcher; Settings toggle for global + per-profile override matching `config.toml`'s `auto_sync` (`*bool`). Meanwhile the `music` pull is still finishing on the phone in the background; let it complete, then do the deferred additive check and final zero-delta as follow-ups.

## Decisions & Problems Not Yet Elsewhere

- E2E fixtures: `~/presync-test/small/big.bin` (30MB, small source no longer has a.txt); phone push source `/sdcard/Download/presync-push-src` (a.txt, c.txt, sub/c.txt, big.bin); `~/presync-test/push-recv` holds all of those except b.txt (mirror-test delete).
- Push/small E2E passed end to end: 5-file push incl. 30MB big.bin, changed-file-only re-upload, Stop at 23,592,960/31,457,280 → Resume at that offset, zero-delta, mirror delete only on complete. Small pull: baseline, Stop at 11,967,711/31,457,280 → resume at offset, mirror delete a.txt, zero-delta. Isolated scripts all pass (transfer 48, connect 13, push 29, pairing 19).
- The music session (`8bc052af7b3ebe88-music-20261101T181657...`) has a 30-minute inactivity limit: if it goes stale before completion it is discarded and, with no baseline written yet, the next sync re-transfers the whole 7.26GB.
- The phone still runs the pre-fix `app.js` until the page is reloaded; the new Progress list (all sessions, per-card Stop/Resume) needs an on-device reload check.
- Throughput is per-file-overhead-bound, not WiFi-bound: a single 30MB file resumed at ~10MB/s, while ~5.4MB MP3s average ~3s each (SAF write/rename + native hash + confirm per file).

## Open Questions

- Does the full 6.8G music pull complete cleanly, and does the additive check behave (file added lands; deleted from source stays on phone)?
- Does the final music zero-delta sync report no transfers once the baseline exists?
- On-device rendering of the new Progress list (live + paused cards visible together) after reload.
