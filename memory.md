# Memory

## Active Feature

Android pull restored + push profiles (client → server) built and ready for device testing.

Branch: main

## Session Phase

closing

## Next Action

Give the developer the manual end-to-end test tutorial: clean config, start daemon, pair the Android app, pull a small folder then `~/Music`, push a small folder, test resume by restarting the daemon mid-transfer, and test mirror deletion. Use `./presync` and `android/app/build/outputs/apk/debug/app-debug.apk`; fix issues found.

## Decisions & Problems Not Yet Elsewhere

- JS→Android bridge callbacks must be global names, not inline functions. `web/app.js` uses `bridgeCallback()` and passes the generated name; keep this for any new bridge method.
- Pull resume reconciles the session against the freshly computed delta: changed/missing `done` files are re-queued, new source files are added, removed source files are dropped.
- Push uses server-owned offsets and temp files; mirror deletions are persisted and applied only on successful `/push/complete`; an empty source manifest is refused when the destination has files.
- Complete handlers delete the resolved `sess.Path`, never the raw client-supplied session identifier.
- Manifest paths are normalized to forward slashes (`filepath.ToSlash`) so Windows/Android manifests compare correctly.
- Android SAF append/seek/rename are still device-unverified.

## Open Questions

- Does SAF `openOutputStream(uri, "wa")` and `DocumentFile.renameTo` behave correctly on the Samsung A06? The device test will tell.
- Real music is ~6.8G against ~12G free on the phone — full pull is feasible but tight.
- Still pending after this: Task 12 auto-sync, mDNS, sync history, cross-compile.
