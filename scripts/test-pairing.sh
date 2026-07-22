#!/usr/bin/env bash
set -e

# Integration test for Task 04: Pairing + Trust Store
# Uses a temp config dir — never touches real ~/.config/presync

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
export PRESYNC_CONFIG_DIR=$(mktemp -d)
PORT=17700
BASE="http://localhost:$PORT"
PASS=0
FAIL=0
PRESYNC="$PROJECT_DIR/presync-test"

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

# Build
echo "=== Building ==="
(cd "$PROJECT_DIR" && go build -o presync-test ./cmd/presync)

# Write a test config
cat > "$PRESYNC_CONFIG_DIR/config.toml" <<EOF
[daemon]
port = $PORT
log_level = "info"

[[profiles]]
name = "Music"
source = "/tmp/fake-src"
mode = "mirror"

[profiles.destinations]
EOF

echo "=== Starting daemon ==="
$PRESYNC start
sleep 1

# Check daemon is running
STATUS=$($PRESYNC status 2>&1)
assert_contains "daemon is running" "running" "$STATUS"

echo ""
echo "=== Test 1: Auth rejection ==="
BODY=$(curl -s $BASE/status)
assert_contains "no token gets 401" "missing authentication token" "$BODY"

BODY=$(curl -s -H "X-Presync-Token: bad-token" $BASE/status)
assert_contains "wrong token gets 401" "invalid or revoked token" "$BODY"

echo ""
echo "=== Test 2: Pairing flow ==="
PAIR_RESP=$(curl -s -X POST $BASE/pair)
TOKEN=$(echo "$PAIR_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
PIN=$(echo "$PAIR_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['pin'])")

assert_eq "token is 64 chars" "64" "${#TOKEN}"
assert_eq "PIN is 4 chars" "4" "${#PIN}"

STATUS_RESP=$(curl -s $BASE/pair/status)
ACTIVE=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['active'])")
assert_eq "session is active" "True" "$ACTIVE"

ACCEPT_RESP=$(curl -s -X POST $BASE/pair/accept \
  -H "Content-Type: application/json" \
  -d "{\"token\": \"$TOKEN\", \"name\": \"test-phone\"}")
ACCEPT_STATUS=$(echo "$ACCEPT_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
assert_eq "accept returns accepted" "accepted" "$ACCEPT_STATUS"

STATUS_RESP=$(curl -s $BASE/pair/status)
ACCEPTED=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['accepted'])")
DEVNAME=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['device_name'])")
assert_eq "session accepted" "True" "$ACCEPTED"
assert_eq "device name correct" "test-phone" "$DEVNAME"

CONFIRM_RESP=$(curl -s -X POST $BASE/pair/confirm)
DEVICE_ID=$(echo "$CONFIRM_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['device_id'])")
LONG_TOKEN=$(echo "$CONFIRM_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

assert_eq "device ID is 8 chars" "8" "${#DEVICE_ID}"
assert_eq "long-lived token is 64 chars" "64" "${#LONG_TOKEN}"

DEVICES=$(cat "$PRESYNC_CONFIG_DIR/devices.json")
assert_contains "device in devices.json" "test-phone" "$DEVICES"
assert_contains "device ID in devices.json" "$DEVICE_ID" "$DEVICES"

echo ""
echo "=== Test 3: Auth with real token ==="
BODY=$(curl -s -H "X-Presync-Token: $LONG_TOKEN" $BASE/status)
assert_contains "valid token gets 200" '"status":"ok"' "$BODY"

echo ""
echo "=== Test 4: Pairing conflict ==="
curl -s -X POST $BASE/pair > /dev/null
BODY=$(curl -s -X POST $BASE/pair)
assert_contains "second session rejected" "already active" "$BODY"
curl -s -X POST $BASE/pair/discard > /dev/null

echo ""
echo "=== Test 5: Unpair ==="
DEVICES_LIST=$($PRESYNC unpair <<< "1" 2>&1)
assert_contains "unpair shows device" "test-phone" "$DEVICES_LIST"

DEVICES=$(cat "$PRESYNC_CONFIG_DIR/devices.json")
assert_eq "devices.json empty after unpair" "[]" "$(echo "$DEVICES" | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin)['devices']))")"

echo ""
echo "=== Test 6: Auth rejection after unpair ==="
BODY=$(curl -s -H "X-Presync-Token: $LONG_TOKEN" $BASE/status)
assert_contains "unpaired token rejected" "invalid or revoked token" "$BODY"

echo ""
echo "================================"
echo "Results: $PASS passed, $FAIL failed"
if [ $FAIL -eq 0 ]; then
    echo "ALL TESTS PASSED"
else
    echo "SOME TESTS FAILED"
    exit 1
fi
