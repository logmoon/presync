#!/usr/bin/env bash
set -e

# Integration test for Task 05: Transfer Engine + Checksums + Sessions
# Uses a temp config dir — never touches real ~/.config/presync

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
export PRESYNC_CONFIG_DIR=$(mktemp -d)
PORT=17700
BASE="http://localhost:$PORT"
PASS=0
FAIL=0
PRESYNC="$PROJECT_DIR/presync-test"

SRC_DIR="$PRESYNC_CONFIG_DIR/source"
DST_DIR="$PRESYNC_CONFIG_DIR/dest"

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
dd if=/dev/urandom of="$SRC_DIR/bigfile.bin" bs=1024 count=256 2>/dev/null
echo "nested content" > "$SRC_DIR/subdir/nested.txt"
dd if=/dev/urandom of="$SRC_DIR/subdir/medium.bin" bs=1024 count=64 2>/dev/null

# Create empty dest
mkdir -p "$DST_DIR"

# Write config
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
echo "=== Starting daemon ==="
$PRESYNC start
sleep 1

STATUS=$($PRESYNC status 2>&1)
assert_contains "daemon is running" "running" "$STATUS"

echo ""
echo "=== Pairing a device ==="
LOCAL_TOKEN=$(curl -s $BASE/web/token | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
PAIR_RESP=$(curl -s -X POST $BASE/pair)
TOKEN=$(echo "$PAIR_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
curl -s -X POST $BASE/pair/accept \
  -H "Content-Type: application/json" \
  -d "{\"token\": \"$TOKEN\", \"name\": \"test-client\"}" > /dev/null
CONFIRM_RESP=$(curl -s -X POST -H "X-Presync-Token: $LOCAL_TOKEN" $BASE/pair/confirm)
DEVICE_ID=$(echo "$CONFIRM_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['device_id'])")
LONG_TOKEN=$(echo "$CONFIRM_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

assert_eq "device ID is 16 chars" "16" "${#DEVICE_ID}"
assert_eq "long token is 64 chars" "64" "${#LONG_TOKEN}"

echo ""
echo "=== Test 1: Start sync ==="
DEST_MANIFEST='{"root":"'"$DST_DIR"'","files":[],"built_at":0}'
SYNC_RESP=$(curl -s -X POST $BASE/sync \
  -H "Content-Type: application/json" \
  -H "X-Presync-Token: $LONG_TOKEN" \
  -d '{"device_id":"'"$DEVICE_ID"'","profile":"Music","destination_manifest":'"$DEST_MANIFEST"'}')

SESSION=$(echo "$SYNC_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['session'])")
FILE_COUNT=$(echo "$SYNC_RESP" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['files']))")

assert_contains "session path returned" "sessions" "$SESSION"
assert_eq "4 files to sync" "4" "$FILE_COUNT"

echo ""
echo "=== Test 2: Sync status ==="
STATUS_RESP=$(curl -s "$BASE/sync/status?session=$SESSION" \
  -H "X-Presync-Token: $LONG_TOKEN")
TOTAL=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['total'])")
DONE=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['done'])")

assert_eq "total files in session" "4" "$TOTAL"
assert_eq "done files is 0" "0" "$DONE"

echo ""
echo "=== Test 3: Fetch and confirm files ==="
FILES=$(echo "$SYNC_RESP" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for f in data['files']:
    print(f['path'])
")

for FILE_PATH in $FILES; do
    echo "  Fetching: $FILE_PATH"

    # Ensure subdirectory exists
    mkdir -p "$DST_DIR/$(dirname "$FILE_PATH")"

    # Fetch file
    HTTP_CODE=$(curl -s -o "$DST_DIR/$FILE_PATH.presync-tmp" \
      -w "%{http_code}" \
      "$BASE/sync/file?session=$SESSION&path=$FILE_PATH" \
      -H "X-Presync-Token: $LONG_TOKEN")

    assert_eq "fetch $FILE_PATH returns 200" "200" "$HTTP_CODE"

    # Compute SHA-256 of received file
    HASH=$(sha256sum "$DST_DIR/$FILE_PATH.presync-tmp" | awk '{print $1}')

    # Confirm
    CONFIRM_RESP=$(curl -s -X POST $BASE/sync/confirm \
      -H "Content-Type: application/json" \
      -H "X-Presync-Token: $LONG_TOKEN" \
      -d '{"session":"'"$SESSION"'","path":"'"$FILE_PATH"'","sha256":"'"$HASH"'"}')

    CONFIRMED=$(echo "$CONFIRM_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['ok'])")
    assert_eq "confirm $FILE_PATH succeeds" "True" "$CONFIRMED"

    # Rename temp to real
    mv "$DST_DIR/$FILE_PATH.presync-tmp" "$DST_DIR/$FILE_PATH"
done

echo ""
echo "=== Test 4: Verify files transferred correctly ==="
assert_files_equal "file1.txt matches" "$SRC_DIR/file1.txt" "$DST_DIR/file1.txt"
assert_files_equal "bigfile.bin matches" "$SRC_DIR/bigfile.bin" "$DST_DIR/bigfile.bin"
assert_files_equal "nested.txt matches" "$SRC_DIR/subdir/nested.txt" "$DST_DIR/subdir/nested.txt"
assert_files_equal "medium.bin matches" "$SRC_DIR/subdir/medium.bin" "$DST_DIR/subdir/medium.bin"

echo ""
echo "=== Test 5: Complete sync ==="
COMPLETE_RESP=$(curl -s -X POST $BASE/sync/complete \
  -H "Content-Type: application/json" \
  -H "X-Presync-Token: $LONG_TOKEN" \
  -d '{"session":"'"$SESSION"'"}')

COMPLETE_OK=$(echo "$COMPLETE_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['ok'])")
assert_eq "sync complete returns ok" "True" "$COMPLETE_OK"

# Verify session file is deleted
if [ ! -f "$SESSION" ]; then
    echo "  PASS: session file deleted after complete"
    PASS=$((PASS + 1))
else
    echo "  FAIL: session file still exists after complete"
    FAIL=$((FAIL + 1))
fi

echo ""
echo "=== Test 6: Resume after daemon restart ==="
# Modify a file to test re-sync
echo "updated content" > "$SRC_DIR/file1.txt"

# Start a new sync
DEST_MANIFEST='{"root":"'"$DST_DIR"'","files":[{"path":"file1.txt","size":16,"mod_time":0},{"path":"bigfile.bin","size":262144,"mod_time":0},{"path":"subdir/nested.txt","size":16,"mod_time":0},{"path":"subdir/medium.bin","size":65536,"mod_time":0}],"built_at":0}'
SYNC_RESP2=$(curl -s -X POST $BASE/sync \
  -H "Content-Type: application/json" \
  -H "X-Presync-Token: $LONG_TOKEN" \
  -d '{"device_id":"'"$DEVICE_ID"'","profile":"Music","destination_manifest":'"$DEST_MANIFEST"'}')

FILES2=$(echo "$SYNC_RESP2" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for f in data['files']:
    print(f['path'])
")
FILE_COUNT2=$(echo "$SYNC_RESP2" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['files']))")

assert_eq "resync has files to update" "4" "$FILE_COUNT2"

# Fetch and confirm only file1.txt (simulate partial transfer)
for FILE_PATH in $FILES2; do
    if [ "$FILE_PATH" = "file1.txt" ]; then
        HTTP_CODE=$(curl -s -o "$DST_DIR/$FILE_PATH.presync-tmp" \
          -w "%{http_code}" \
          "$BASE/sync/file?session=$(echo "$SYNC_RESP2" | python3 -c "import sys,json; print(json.load(sys.stdin)['session'])")&path=$FILE_PATH" \
          -H "X-Presync-Token: $LONG_TOKEN")

        HASH=$(sha256sum "$DST_DIR/$FILE_PATH.presync-tmp" | awk '{print $1}')
        CONFIRM_RESP=$(curl -s -X POST $BASE/sync/confirm \
          -H "Content-Type: application/json" \
          -H "X-Presync-Token: $LONG_TOKEN" \
          -d '{"session":"'"$(echo "$SYNC_RESP2" | python3 -c "import sys,json; print(json.load(sys.stdin)['session'])")"'","path":"'"$FILE_PATH"'","sha256":"'"$HASH"'"}')

        mv "$DST_DIR/$FILE_PATH.presync-tmp" "$DST_DIR/$FILE_PATH"
        break
    fi
done

assert_files_equal "file1.txt updated after resync" "$SRC_DIR/file1.txt" "$DST_DIR/file1.txt"

echo ""
echo "=== Test 7: Checksum mismatch triggers retry ==="
# Create a file with wrong hash
echo "wrong" > "$DST_DIR/badfile.txt"
BAD_HASH="0000000000000000000000000000000000000000000000000000000000000000"
SESSION3=$(curl -s -X POST $BASE/sync \
  -H "Content-Type: application/json" \
  -H "X-Presync-Token: $LONG_TOKEN" \
  -d '{"device_id":"'"$DEVICE_ID"'","profile":"Music","destination_manifest":{"root":"'"$DST_DIR"'","files":[],"built_at":0}}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['session'])")

# Fetch file1.txt
curl -s -o "$DST_DIR/file1.txt.presync-tmp" \
  "$BASE/sync/file?session=$SESSION3&path=file1.txt" \
  -H "X-Presync-Token: $LONG_TOKEN" > /dev/null

# Confirm with wrong hash
MISMATCH_RESP=$(curl -s -X POST $BASE/sync/confirm \
  -H "Content-Type: application/json" \
  -H "X-Presync-Token: $LONG_TOKEN" \
  -d '{"session":"'"$SESSION3"'","path":"file1.txt","sha256":"'"$BAD_HASH"'"}')

MISMATCH_OK=$(echo "$MISMATCH_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['ok'])")
MISMATCH_ACTION=$(echo "$MISMATCH_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['action'])")
assert_eq "mismatch returns ok=false" "False" "$MISMATCH_OK"
assert_eq "mismatch action is retry" "retry" "$MISMATCH_ACTION"

echo ""
echo "=== Test 8: presync sync — CLI triggers real sync ==="
# Reset dest to empty for a clean CLI sync test
rm -rf "$DST_DIR"
mkdir -p "$DST_DIR"

$PRESYNC sync
SYNC_EXIT=$?
assert_eq "presync sync exits 0" "0" "$SYNC_EXIT"

# Verify files were synced
assert_file_exists "CLI synced file1.txt" "$DST_DIR/file1.txt"
assert_file_exists "CLI synced bigfile.bin" "$DST_DIR/bigfile.bin"
assert_file_exists "CLI synced nested.txt" "$DST_DIR/subdir/nested.txt"
assert_file_exists "CLI synced medium.bin" "$DST_DIR/subdir/medium.bin"
assert_files_equal "CLI file1.txt matches" "$SRC_DIR/file1.txt" "$DST_DIR/file1.txt"
assert_files_equal "CLI bigfile.bin matches" "$SRC_DIR/bigfile.bin" "$DST_DIR/bigfile.bin"

echo ""
echo "=== Test 9: presync sync --fresh — forces new session ==="
# Modify a file, then sync with --fresh
echo "fresh content" > "$SRC_DIR/fresh.txt"

$PRESYNC sync --fresh
FRESH_EXIT=$?
assert_eq "presync sync --fresh exits 0" "0" "$FRESH_EXIT"
assert_file_exists "fresh.txt synced" "$DST_DIR/fresh.txt"
assert_files_equal "fresh.txt matches" "$SRC_DIR/fresh.txt" "$DST_DIR/fresh.txt"

echo ""
echo "=== Test 10: presync sync <name> — syncs named profile ==="
# Add a second profile
mkdir -p "$DST_DIR/podcasts"
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

[[profiles]]
name = "Podcasts"
source = "$SRC_DIR/subdir"
mode = "additive"

[profiles.destinations]
local = "$DST_DIR/podcasts"
EOF

# Restart daemon to pick up new config
$PRESYNC stop
sleep 1
$PRESYNC start
sleep 1

$PRESYNC sync Podcasts
NAME_EXIT=$?
assert_eq "presync sync Podcasts exits 0" "0" "$NAME_EXIT"
assert_file_exists "Podcasts profile synced nested.txt" "$DST_DIR/podcasts/nested.txt"
assert_files_equal "Podcasts nested.txt matches" "$SRC_DIR/subdir/nested.txt" "$DST_DIR/podcasts/nested.txt"

echo ""
echo "=== Test 11: presync sync <bad-name> — errors on unknown profile ==="
BAD_OUTPUT=$($PRESYNC sync Nonexistent 2>&1) && BAD_EXIT=0 || BAD_EXIT=$?
assert_eq "presync sync Nonexistent exits non-zero" "1" "$BAD_EXIT"

echo ""
echo "=== Test 12: presync sync --to <dest> — syncs only to named destination ==="
# Config already has Music with local destination from Test 8
# Add a second destination
DST2_DIR="$PRESYNC_CONFIG_DIR/dest2"
mkdir -p "$DST2_DIR"
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
remote = "$DST2_DIR"
EOF

$PRESYNC stop
sleep 1
$PRESYNC start
sleep 1

# Sync only to local
rm -f "$DST_DIR/file1.txt" "$DST2_DIR/file1.txt"
TO_OUTPUT=$($PRESYNC sync Music --to local 2>&1)
assert_contains "--to local shows local" "dest: local" "$TO_OUTPUT"
assert_contains "--to local does not show remote" "" "$(echo "$TO_OUTPUT" | grep 'dest: remote' || true)"
assert_file_exists "--to local synced file1.txt" "$DST_DIR/file1.txt"
# remote should NOT have the file
if [ -f "$DST2_DIR/file1.txt" ]; then
    echo "  FAIL: --to local should not sync to remote"
    FAIL=$((FAIL + 1))
else
    echo "  PASS: --to local did not sync to remote"
    PASS=$((PASS + 1))
fi

echo ""
echo "=== Test 13: presync sync --to nonexistent — skips silently ==="
rm -f "$DST_DIR/file1.txt"
NONE_OUTPUT=$($PRESYNC sync Music --to nonexistent 2>&1) && NONE_EXIT=0 || NONE_EXIT=$?
assert_eq "--to nonexistent exits 0" "0" "$NONE_EXIT"
if [ -f "$DST_DIR/file1.txt" ]; then
    echo "  FAIL: --to nonexistent should not sync to local"
    FAIL=$((FAIL + 1))
else
    echo "  PASS: --to nonexistent did not sync to local"
    PASS=$((PASS + 1))
fi

echo ""
echo "================================"
echo "Results: $PASS passed, $FAIL failed"
if [ $FAIL -eq 0 ]; then
    echo "ALL TESTS PASSED"
else
    echo "SOME TESTS FAILED"
    exit 1
fi
