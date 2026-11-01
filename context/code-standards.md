# Code Standards — presync

## Engineering Mindset

1. Prefer the simple, obvious solution over the clever one — this codebase is a learning project as much as a tool.
2. Don't add abstraction for a single use case. Wait for a second real use before extracting an interface.
3. Be skeptical of "while I'm in here" scope creep — one feature, one PR-sized change, one exit criterion at a time.
4. Make illegal states unrepresentable where Go makes that cheap (typed enums via constants, not raw strings, for session/file states).
5. If something fails, fail loudly and specifically — no silent fallbacks that mask a real bug.
6. Treat `internal/diff` as the project's core logic and protect its purity (no I/O) above all other conveniences — it's the easiest place for an agent to accidentally introduce coupling.

## Language (Go)

- Go 1.22+ across server, client, and all `internal/` packages.
- Standard `gofmt`/`goimports` formatting — never hand-format.
- No naked returns except in very short (<5 line) functions.
- Errors are values: always check and handle, never `_ = err` outside of genuinely ignorable cases (and comment why when you do).
- No panics for expected/recoverable failure conditions (network errors, missing files, bad config) — those return `error`. `panic` is reserved for programmer errors (nil pointer that should be impossible, invariant violations) and is acceptable to crash the daemon for.
- Prefer named return values only when they materially clarify a function's contract (e.g. `(manifest Manifest, err error)`), not as a default style.
- Use context.Context for anything that does network I/O or could need cancellation (transfers, connects); pass it explicitly as the first parameter, never store it in a struct.
- Concurrency: goroutines for per-file transfer within a sync session are fine; always pair with a `sync.WaitGroup` or errgroup and a bounded worker pool — never spawn unbounded goroutines per file in a large library.

## Framework Conventions

There is no web framework — `net/http` stdlib only.

- Routes are registered explicitly in `internal/server/http.go`; no auto-discovery or reflection-based routing.
- Each handler does request parsing → call into the relevant `internal/` package (diff, transfer, pairing, trust) → write response. Handlers themselves contain no business logic.
- JSON is the wire format for all API responses except the raw file chunk endpoints (which are binary streams).
- Long-lived token is read from a custom header (e.g. `X-Presync-Token`) on every authenticated request; validated once via middleware before reaching any handler.

## File and Folder Naming

- Go packages: lowercase, single word, no underscores (`diff`, `transfer`, `pairing`) — matches `internal/` folder names exactly.
- Go files: `snake_case.go` only when a file name is naturally multi-word (e.g. `session_test.go`); otherwise short lowercase names matching their primary type/function.
- Exported identifiers: `PascalCase`. Unexported: `camelCase`. No stuttering (`diff.Diff` → `diff.Compute`, not `diff.DiffCompute`).
- Web UI files: kebab-case for any new file beyond the three already named (`index.html`, `app.js`, `style.css`).
- Kotlin (Android): standard Kotlin conventions — `PascalCase` for classes (`MainActivity.kt`, `SafBridge.kt`), `camelCase` for functions/properties.

## Module / File Structure

Go file order, top to bottom:
1. Package declaration
2. Imports — stdlib first, blank line, then third-party, blank line, then internal (`presync/internal/...`)
3. Constants and package-level vars
4. Types
5. Constructor/`New*` functions
6. Methods, grouped by receiver
7. Free functions
8. No code below the last function — no trailing init blocks unless genuinely required for package setup

```go
package transfer

import (
    "context"
    "crypto/sha256"
    "fmt"

    "github.com/some/thirdparty"

    "presync/internal/config"
)

const maxRetries = 3

type Session struct {
    // ...
}

func NewSession(profile string) (*Session, error) {
    // ...
}

func (s *Session) Resume(ctx context.Context) error {
    // ...
}
```

## Error Handling

- Standard return shape: every fallible function returns `(T, error)`; errors are wrapped with context using `fmt.Errorf("doing X: %w", err)` so the chain stays inspectable with `errors.Is`/`errors.As`.
- HTTP handlers translate internal errors into a consistent JSON error shape: `{"error": "human-readable message"}` with an appropriate status code — never leak raw Go error strings containing file paths to the client.
- Daemon-level unexpected errors are logged to `~/.config/presync/logs/presync.log` with timestamp and the failing operation; the daemon does not crash on a single failed sync — it marks that session as errored and stays running.
- Transfer-layer checksum mismatches are not treated as fatal errors — they trigger the documented retry-then-mark-error flow (see architecture.md invariant 2), not an exception path.
- The user-facing surface (CLI output, web UI) always shows a specific, actionable message — never a bare "an error occurred."

## Testing

- Unit tests use Go's standard `testing` package, table-driven, in `*_test.go` files beside the code. Run `go test ./...` for every change.
- E2E integration lives in `scripts/test-pairing.sh`, `scripts/test-transfer.sh`, `scripts/test-connect.sh`, and `scripts/test-push.sh`; each runs against a live daemon with a temp `PRESYNC_CONFIG_DIR`. Run the script covering the area you changed.
- Every auth, sync-session, config-validation, or transfer change gets a unit test. Pairing, connect, push, and cross-device changes extend the matching E2E script.
- Android bridge changes must build with `android/gradlew assembleDebug` before review.

## Environment Variables

presync is config-file driven, not environment-variable driven, by design (matches the "no cloud, no accounts, just a config file" philosophy). The only variables used:

| Variable | Used by | Purpose |
|---|---|---|
| `PRESYNC_CONFIG_DIR` | `internal/config` | Override `~/.config/presync` for testing/development. Optional. |
| `PRESYNC_LOG_LEVEL` | `internal/server/daemon.go` | `debug` / `info` / `error`. Defaults to `info`. Optional. |

No other environment variables should be introduced without updating this table.

## Dependencies (Approved List)

Nothing gets added to `go.mod` without updating this list first.

| Package | Purpose |
|---|---|
| stdlib `net/http` | HTTP server and client |
| stdlib `crypto/sha256` | Checksums |
| stdlib `encoding/json` | All JSON wire format and state files |
| `github.com/BurntSushi/toml` (or `pelletier/go-toml`) | `config.toml` parsing |
| `github.com/skip2/go-qrcode` (or equivalent) | Terminal QR rendering for pairing |
| `github.com/grandcat/zeroconf` | mDNS advertise/discover (Phase 2) |

Android module dependencies (Gradle) are tracked separately in `android/app/build.gradle` and are not subject to this table, but any new dependency there should be similarly minimal and justified (the APK is meant to stay a thin shell).
