#!/usr/bin/env bash
set -e

# Integration test for Task 07: Client Connect (PC)
# Uses a temp config dir — never touches real ~/.config/presync

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
export PRESYNC_CONFIG_DIR=$(mktemp -d)
PORT=17701
BASE="http://localhost:$PORT"
PASS=0
FAIL=0
PRESYNC="$PROJECT_DIR/presync-test"

SRC_DIR="$PRESYNC_CONFIG_DIR/source"
DST_DIR="$PRESYNC_CONFIG_DIR/dest"
CLIENT_DIR="$PRESYNC_CONFIG_DIR/client"

cleanup() {
    $PRESYNC stop 2>/dev/null || true
    rm -rf "$PRESYNC_CONFIG_DIR" "$PRESYNC"
}
trap cleanup EXIT

assert_eq() {
    local label="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        echo "  PASS: $label"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $label (expected: $expected, got: $actual)"
        FAIL=$((FAIL + 1))
    fi
}

assert_contains() {
    local label="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -q "$needle"; then
        echo "  PASS: $label"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $label (expected to contain: $needle)"
        FAIL=$((FAIL + 1))
    fi
}

assert_file_exists() {
    local label="$1" path="$2"
    if [ -f "$path" ]; then
        echo "  PASS: $label"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $label (file not found: $path)"
        FAIL=$((FAIL + 1))
    fi
}

assert_files_equal() {
    local label="$1" src="$2" dst="$3"
    if diff -q "$src" "$dst" > /dev/null 2>&1; then
        echo "  PASS: $label"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $label (files differ)"
        FAIL=$((FAIL + 1))
    fi
}

# Build
echo "=== Building ==="
(cd "$PROJECT_DIR" && go build -o presync-test ./cmd/presync)

# Create source files
echo "=== Creating source files ==="
mkdir -p "$SRC_DIR/subdir"
echo "hello world" > "$SRC_DIR/file1.txt"
echo "nested content" > "$SRC_DIR/subdir/nested.txt"

# Create client dest
mkdir -p "$CLIENT_DIR"

# Write config for the server
cat > "$PRESYNC_CONFIG_DIR/config.toml" <<EOF
[daemon]
port = $PORT
log_level = "info"

[[profiles]]
name = "Music"
source = "$SRC_DIR"
mode = "mirror"

[profiles.destinations]
local = "$DST_DIR"
EOF

echo ""
echo "=== Starting server daemon ==="
$PRESYNC start
sleep 1

STATUS=$($PRESYNC status 2>&1)
assert_contains "daemon is running" "running" "$STATUS"

# --- Test 1: Pair via presync connect ---
echo ""
echo "=== Test 1: Pair via presync connect ==="

# Start pairing on the server to get a session token
LOCAL_TOKEN=$(curl -s $BASE/web/token | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
PAIR_RESP=$(curl -s -X POST $BASE/pair)
TOKEN=$(echo "$PAIR_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
PIN=$(echo "$PAIR_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['pin'])")

# Register destinations for the client on the server (must be after pair start)
curl -s -X POST $BASE/pair/destinations \
  -H "Content-Type: application/json" \
  -d "{\"token\": \"$TOKEN\", \"name\": \"test-client\", \"destinations\": {\"Music\": \"$CLIENT_DIR\"}}" > /dev/null

PAIR_URL="http://localhost:$PORT/?token=$TOKEN"

# Use a separate config dir for the client side
CLIENT_CONFIG="$PRESYNC_CONFIG_DIR/client-config"
mkdir -p "$CLIENT_CONFIG"

# Run connect in background; confirm pairing on the server side (as the CLI
# user would after verifying the PIN)
CONNECT_LOG="$CLIENT_CONFIG/connect.log"
(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 30 $PRESYNC connect "$PAIR_URL" > "$CONNECT_LOG" 2>&1) &
CONNECT_PID=$!
sleep 3
curl -s -X POST -H "X-Presync-Token: $LOCAL_TOKEN" $BASE/pair/confirm > /dev/null
wait $CONNECT_PID || true
CONNECT_OUTPUT=$(cat "$CONNECT_LOG")

assert_contains "pairing succeeded" "Paired successfully" "$CONNECT_OUTPUT"
assert_contains "sync started" "Syncing" "$CONNECT_OUTPUT"

# Check that files were synced
assert_file_exists "file1.txt synced" "$CLIENT_DIR/file1.txt"
assert_files_equal "file1.txt content matches" "$SRC_DIR/file1.txt" "$CLIENT_DIR/file1.txt"
assert_file_exists "nested.txt synced" "$CLIENT_DIR/subdir/nested.txt"
assert_files_equal "nested.txt content matches" "$SRC_DIR/subdir/nested.txt" "$CLIENT_DIR/subdir/nested.txt"

# --- Test 2: Reconnect via presync connect (no args) ---
echo ""
echo "=== Test 2: Reconnect via presync connect (no args) ==="

# Add a new file on the server
echo "new file" > "$SRC_DIR/file2.txt"

# Reconnect using stored server info
RECONNECT_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 10 $PRESYNC connect 2>&1) || true

assert_contains "reconnect succeeded" "Connected to" "$RECONNECT_OUTPUT"
assert_contains "sync happened" "Syncing" "$RECONNECT_OUTPUT"

# Check that the new file was synced
assert_file_exists "file2.txt synced on reconnect" "$CLIENT_DIR/file2.txt"
assert_files_equal "file2.txt content matches" "$SRC_DIR/file2.txt" "$CLIENT_DIR/file2.txt"

# --- Test 3: profiles endpoint ---
echo ""
echo "=== Test 3: GET /profiles endpoint ==="

PROFILES_RESP=$(curl -s -H "X-Presync-Token: $(cat "$CLIENT_CONFIG/servers.json" | python3 -c "import sys,json; print(json.load(sys.stdin)['servers'][0]['token'])")" $BASE/profiles)
PROFILES=$(echo "$PROFILES_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['profiles'][0])")

assert_eq "profiles endpoint returns Music" "Music" "$PROFILES"

# --- Test 4: connect with no paired server errors ---
echo ""
echo "=== Test 4: connect with no paired server ==="

EMPTY_CONFIG="$PRESYNC_CONFIG_DIR/empty-config"
mkdir -p "$EMPTY_CONFIG"

EMPTY_OUTPUT=$(PRESYNC_CONFIG_DIR="$EMPTY_CONFIG" timeout 5 $PRESYNC connect 2>&1) || true
assert_contains "no server error" "no paired server" "$EMPTY_OUTPUT"

# --- Summary ---
echo ""
echo "=============================="
echo "Results: $PASS passed, $FAIL failed"
echo "=============================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
