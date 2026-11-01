# Library Docs — presync

## net/http (stdlib)

### Setup / Initialisation
```go
mux := http.NewServeMux()
mux.HandleFunc("/manifest", withAuth(handleManifest))
mux.HandleFunc("/pair", handlePair) // unauthenticated — pairing endpoint only
mux.Handle("/", http.FileServer(http.Dir("web")))

srv := &http.Server{
    Addr:    fmt.Sprintf(":%d", cfg.Daemon.Port),
    Handler: mux,
}
```

### Common Patterns
- Auth middleware wraps every handler except `/pair` and the static APK download:
```go
func withAuth(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        token := r.Header.Get("X-Presync-Token")
        if !trust.Validate(token) {
            http.Error(w, `{"error":"invalid or revoked token"}`, http.StatusUnauthorized)
            return
        }
        next(w, r)
    }
}
```
- Range requests for resumable transfers: read `Range: bytes=<offset>-` from the client and serve from `io.SeekStart` at that offset using `http.ServeContent`, or implement manually for the chunked transfer endpoint since file content is generated/streamed rather than a static file.

### Rules
- Every authenticated route goes through `withAuth` — no handler checks the token itself.
- Handlers never write business logic inline — they parse the request, call into `internal/diff`, `internal/transfer`, etc., and serialize the result.
- Binary file chunk endpoints must set `Content-Type: application/octet-stream` explicitly; JSON endpoints set `Content-Type: application/json`.

---

## crypto/sha256 (stdlib)

### Setup / Initialisation
```go
h := sha256.New()
if _, err := io.Copy(h, file); err != nil {
    return "", fmt.Errorf("hashing file: %w", err)
}
sum := hex.EncodeToString(h.Sum(nil))
```

### Common Patterns
- Manifest building only hashes a file when size+mtime differ from the comparison manifest (see architecture.md invariant — never hash the whole library on every sync).
- Client hashes the assembled `.presync-tmp` file only after all chunks are received, before reporting back to the server.

### Rules
- Never compare files by hash alone without first checking size+mtime as a cheap pre-filter — hashing is the expensive path, reserved for genuine ambiguity.
- Hash comparisons are always done as hex strings, never raw bytes, to keep JSON serialization in session/manifest files simple.

---

## encoding/json (stdlib)

### Setup / Initialisation
Used for: API request/response bodies, `devices.json`, session state files.

### Common Patterns
```go
type Manifest struct {
    Files []FileEntry `json:"files"`
}

type FileEntry struct {
    Path    string `json:"path"`
    Size    int64  `json:"size"`
    ModTime int64  `json:"mod_time"` // unix seconds
    SHA256  string `json:"sha256,omitempty"`
}
```

### Rules
- All JSON field names are `snake_case` in the wire/file format (`json:"mod_time"`), even though Go field names stay `PascalCase`.
- `omitempty` is used for `SHA256` since it's only populated when actually computed.
- Session and trust-store files are written via a write-to-temp-then-rename pattern to avoid corrupting them on a crash mid-write — the same atomic-rename discipline used for synced files applies to presync's own state.

---

## BurntSushi/toml (config.toml parsing)

### Setup / Initialisation
```go
var cfg Config
if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
    return fmt.Errorf("parsing config.toml: %w", err)
}
```

### Common Patterns
```go
type Config struct {
    Daemon   DaemonConfig `toml:"daemon"`
    Profiles []Profile    `toml:"profiles"`
}

type Profile struct {
    Name         string            `toml:"name"`
    Source       string            `toml:"source"`
    Direction    string            `toml:"direction"`   // "pull" (default) | "push"
    Destination  string            `toml:"destination"` // push only: server-side receive folder
    Destinations map[string]string `toml:"destinations"` // pull only, keyed by device ID
    Mode         string            `toml:"mode"`        // "mirror" | "additive"
    AutoSync     *bool             `toml:"auto_sync"`   // pointer so "unset" is distinguishable from "false", to allow per-profile override of the daemon-level default
}
```

### Rules
- `AutoSync` must be a pointer (`*bool`) specifically so an unset value in a profile correctly falls back to `daemon.auto_sync` rather than defaulting to `false`.
- Config is read once at daemon start; a config change requires a restart in Phase 1 (hot-reload is not in scope).
- `Mode` is validated against the literal set `{"mirror", "additive"}` at load time — an unrecognized value is a startup error, not a silent default.
- `Direction` defaults to `pull` when omitted. Push profiles require `destination` and reject `destinations`; pull profiles reject `destination`. These cross-rules are enforced in `validate`, not silently normalized.
- Push profiles accept both `mirror` and `additive`; the mirror safety guard (empty source) lives in the push handler, not in config.

---

## go-qrcode (QR generation)

### Setup / Initialisation
```go
qr, err := qrcode.New(pairingURL, qrcode.Medium)
if err != nil {
    return fmt.Errorf("generating QR: %w", err)
}
fmt.Println(qr.ToSmallString(false))
```

### Common Patterns
- `pairingURL` is always `http://<local-ip>:<port>/pair?token=<session-token>`, generated fresh per `presync pair` invocation.
- Terminal rendering only (`ToSmallString`) — no image file output needed for Phase 1.

### Rules
- The encoded token must be the 60-second single-use session token, never the long-lived device token.
- Regenerate the QR (new token) on every `presync pair` call — never reuse a token across invocations.

---

## grandcat/zeroconf (mDNS — Phase 2)

### Setup / Initialisation
```go
// Server: advertise
server, err := zeroconf.Register("presync", "_presync._tcp", "local.", port, nil, nil)
defer server.Shutdown()

// Client: discover
resolver, _ := zeroconf.NewResolver(nil)
entries := make(chan *zeroconf.ServiceEntry)
go resolver.Browse(ctx, "_presync._tcp", "local.", entries)
```

### Common Patterns
- Client always tries `presync.local:<port>` directly first (fast path); falls back to active mDNS browse only if the direct attempt fails, then falls back further to the last stored IP in `servers.json`.
- On any successful connection via any method, the resolved IP is written back to `servers.json` so the direct-path attempt has a better chance next time.

### Rules
- mDNS browse has a timeout (a few seconds) — never block indefinitely waiting for a response; fall through to the stored-IP path.
- This package is platform-dependent under the hood (relies on system mDNS support) — isolate all calls inside `internal/mdns` so a platform-specific swap doesn't ripple elsewhere. Pending after first use: confirm Windows behavior; may need a different backend or a bundled mDNS responder.

---

## Android: WebView + JavascriptInterface

### Setup / Initialisation
```kotlin
webView.settings.javaScriptEnabled = true
webView.addJavascriptInterface(SafBridge(this), "AndroidBridge")
webView.loadUrl(pairedServerUrl)
```

### Common Patterns
- Pull contract (paths are `<profile>/<relative path>` under the picked destination tree): `buildManifest`, `hashFile`, `tempFileSize`, `writeChunk`, `finishFile`, `renameFile`, `abortFile`, `deleteFile`.
- Push contract (paths are relative to the per-profile picked source tree): `pushBuildManifest`, `pushHashFile`, `pushReadChunk`, plus `getPushSourceDisplayPath` / `pickPushSourceFolder`.
- `@JavascriptInterface` methods run on the WebView bridge thread; file I/O is synchronous there and the result is posted back with `evaluateJavascript`. JS calls are serialized per transfer, so ordering is preserved.
- Callback shape is always `{success: bool, data: string}`; JSON payloads are encoded into `data` (manifests, hashes, chunk bytes).

### Rules
- `MANAGE_EXTERNAL_STORAGE` is never requested — only `ACTION_OPEN_DOCUMENT_TREE` per folder, once.
- Folder URI permissions must be persisted (`takePersistableUriPermission`) so they survive app restarts — re-prompting the user on every launch is a bug.
- All file writes through the bridge follow the same temp-file-then-rename discipline as the PC client, implemented via SAF's `DocumentFile` API.
- SAF append is done with `ContentResolver.openOutputStream(uri, "wa")`; the temp file is created once with `DocumentFile.createFile` and must keep the requested display name. `renameFile` deletes an existing destination document before `DocumentFile.renameTo`.
- Every bridge path is validated segment-by-segment (no absolute paths, no `.`/`..`, no backslashes) and resolved inside the picked tree, even though the page is origin-gated.

---

## Android: SharedPreferences (token store)

### Setup / Initialisation
```kotlin
val prefs = getSharedPreferences("presync_auth", Context.MODE_PRIVATE)
prefs.edit().putString("token", longLivedToken).apply()
```

### Rules
- Token store holds exactly one server's token at a time in Phase 1 (single paired server per device) — multi-server support is not in scope.
- Never log the token value, even at debug log level.
