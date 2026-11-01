#!/usr/bin/env bash
set -e

# Integration test for Task 12: Auto-sync watcher (PC)
# Uses temp config dirs — never touches real ~/.config/presync

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
export PRESYNC_CONFIG_DIR=$(mktemp -d)
PORT=17704
BASE="http://localhost:$PORT"
PASS=0
FAIL=0
PRESYNC="$PROJECT_DIR/presync-test"

SRC_DIR="$PRESYNC_CONFIG_DIR/source"
DST_DIR="$PRESYNC_CONFIG_DIR/dest"
CLIENT_CONFIG=""

cleanup() {
    if [ -n "$CLIENT_CONFIG" ]; then
        PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" $PRESYNC watch --stop 2>/dev/null || true
    fi
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

# Build
echo "=== Building ==="
(cd "$PROJECT_DIR" && go build -o presync-test ./cmd/presync)

# Source + destination
echo "=== Creating source files ==="
mkdir -p "$SRC_DIR"
echo "hello" > "$SRC_DIR/one.txt"
mkdir -p "$DST_DIR"

# Server config with auto-sync enabled
cat > "$PRESYNC_CONFIG_DIR/config.toml" <<EOF
[daemon]
port = $PORT
log_level = "info"
auto_sync = true
auto_sync_cooldown_minutes = 15

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

# --- Pair a client ---
echo ""
echo "=== Pairing client ==="
LOCAL_TOKEN=$(curl -s $BASE/web/token | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
PAIR_RESP=$(curl -s -X POST $BASE/pair)
TOKEN=$(echo "$PAIR_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

CLIENT_DIR="$PRESYNC_CONFIG_DIR/client-dest"
mkdir -p "$CLIENT_DIR"
curl -s -X POST $BASE/pair/destinations \
  -H "Content-Type: application/json" \
  -d "{\"token\": \"$TOKEN\", \"name\": \"test-client\", \"destinations\": {\"Music\": \"$CLIENT_DIR\"}}" > /dev/null

PAIR_URL="http://localhost:$PORT/?token=$TOKEN"
CLIENT_CONFIG="$PRESYNC_CONFIG_DIR/client-config"
mkdir -p "$CLIENT_CONFIG"

(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 30 $PRESYNC connect "$PAIR_URL" > /dev/null 2>&1) &
CONNECT_PID=$!
sleep 3
curl -s -X POST -H "X-Presync-Token: $LOCAL_TOKEN" $BASE/pair/confirm > /dev/null
wait $CONNECT_PID || true

DEVICE_TOKEN=$(python3 -c "import json; print(json.load(open('$CLIENT_CONFIG/servers.json'))['servers'][0]['token'])")

# --- Test 1: /auto-sync reports eligible profile ---
echo ""
echo "=== Test 1: /auto-sync eligibility ==="
AUTO_JSON=$(curl -s -H "X-Presync-Token: $DEVICE_TOKEN" $BASE/auto-sync)
AUTO_PROFILE=$(echo "$AUTO_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin)['profiles'][0])")
AUTO_COOLDOWN=$(echo "$AUTO_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin)['cooldown_minutes'])")
assert_eq "eligible profile" "Music" "$AUTO_PROFILE"
assert_eq "cooldown from config" "15" "$AUTO_COOLDOWN"

# --- Test 2: watch --once syncs a new file ---
echo ""
echo "=== Test 2: watch --once syncs a new file ==="
echo "auto content" > "$SRC_DIR/two.txt"
ONCE_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 60 $PRESYNC watch --once 2>&1) || true
assert_contains "once run triggered" "auto-sync complete" "$ONCE_OUTPUT"
assert_file_exists "new file synced by watcher" "$CLIENT_DIR/two.txt"

# --- Test 3: cooldown suppresses a second run ---
echo ""
echo "=== Test 3: cooldown suppresses a second run ==="
echo "third" > "$SRC_DIR/three.txt"
COOLDOWN_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 60 $PRESYNC watch --once 2>&1) || true
assert_contains "cooldown reported" "cooldown active" "$COOLDOWN_OUTPUT"
if [ -f "$CLIENT_DIR/three.txt" ]; then
    echo "  FAIL: file synced despite cooldown"
    FAIL=$((FAIL + 1))
else
    echo "  PASS: file not synced during cooldown"
    PASS=$((PASS + 1))
fi

# --- Test 4: detached watcher lifecycle ---
echo ""
echo "=== Test 4: detached watcher lifecycle ==="
START_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 10 $PRESYNC watch 2>&1) || true
assert_contains "watcher started" "watcher started" "$START_OUTPUT"

STATUS_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 5 $PRESYNC watch --status 2>&1) || true
assert_contains "watcher status running" "watcher running" "$STATUS_OUTPUT"

STOP_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 10 $PRESYNC watch --stop 2>&1) || true
assert_contains "watcher stopped" "watcher stopped" "$STOP_OUTPUT"

STOPPED_OUTPUT=$(PRESYNC_CONFIG_DIR="$CLIENT_CONFIG" timeout 5 $PRESYNC watch --status 2>&1) || true
assert_contains "watcher status stopped" "watcher stopped" "$STOPPED_OUTPUT"

# --- Summary ---
echo ""
echo "=============================="
echo "Results: $PASS passed, $FAIL failed"
echo "=============================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
