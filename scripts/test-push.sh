#!/usr/bin/env bash
set -e

# Integration test for push profiles (client -> server).
# Uses a temp config dir — never touches real ~/.config/presync

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
export PRESYNC_CONFIG_DIR=$(mktemp -d)
PORT=17702
BASE="http://localhost:$PORT"
PASS=0
FAIL=0
PRESYNC="$PROJECT_DIR/presync-test"

SRC_ADD="$PRESYNC_CONFIG_DIR/phone-additive"
SRC_MIRROR="$PRESYNC_CONFIG_DIR/phone-mirror"
SRC_EMPTY="$PRESYNC_CONFIG_DIR/phone-empty"
DST_ADD="$PRESYNC_CONFIG_DIR/server-additive"
DST_MIRROR="$PRESYNC_CONFIG_DIR/server-mirror"

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

assert_file_missing() {
    local label="$1" path="$2"
    if [ ! -e "$path" ]; then
        echo "  PASS: $label"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $label (file still exists: $path)"
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

json_get() {
    python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"
}

build_manifest() {
    python3 - "$1" <<'PY'
import json, os, sys, time

root = sys.argv[1]
files = []
for dirpath, _, names in os.walk(root):
    for name in names:
        if name.endswith(".presync-tmp"):
            continue
        full = os.path.join(dirpath, name)
        rel = os.path.relpath(full, root).replace(os.sep, "/")
        st = os.stat(full)
        files.append({
            "path": rel,
            "size": st.st_size,
            "mod_time": int(st.st_mtime),
        })
files.sort(key=lambda f: f["path"])
print(json.dumps({"root": root, "files": files, "built_at": int(time.time())}))
PY
}

start_push() {
    local profile="$1" src="$2"
    local manifest
    manifest=$(build_manifest "$src")
    curl -s -X POST "$BASE/push" \
        -H "X-Presync-Token: $LONG_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"device_id\": \"$DEVICE_ID\", \"profile\": \"$profile\", \"source_manifest\": $manifest}"
}

push_chunk() {
    local session="$1" path="$2" offset="$3" data_file="$4"
    curl -s -X POST "$BASE/push/file?session=$(basename "$session")&path=$path&offset=$offset" \
        -H "X-Presync-Token: $LONG_TOKEN" \
        --data-binary @"$data_file"
}

push_confirm() {
    local session="$1" path="$2" sha="$3"
    curl -s -X POST "$BASE/push/confirm" \
        -H "X-Presync-Token: $LONG_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"session\": \"$(basename "$session")\", \"path\": \"$path\", \"sha256\": \"$sha\"}"
}

push_complete() {
    local session="$1"
    curl -s -X POST "$BASE/push/complete" \
        -H "X-Presync-Token: $LONG_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"session\": \"$(basename "$session")\"}"
}

echo "=== Building ==="
(cd "$PROJECT_DIR" && go build -o presync-test ./cmd/presync)

echo "=== Creating phone and server folders ==="
mkdir -p "$SRC_ADD/subdir" "$SRC_MIRROR" "$SRC_EMPTY" "$DST_ADD" "$DST_MIRROR"
echo "hello world" > "$SRC_ADD/file1.txt"
echo "nested content" > "$SRC_ADD/subdir/nested.txt"
printf 'mirror-keep' > "$SRC_MIRROR/keep.mp3"
printf 'mirror-gone' > "$DST_MIRROR/gone.mp3"

cat > "$PRESYNC_CONFIG_DIR/config.toml" <<EOF
[daemon]
port = $PORT
log_level = "info"

[[profiles]]
name = "PhoneMusic"
direction = "push"
destination = "$DST_ADD"
mode = "additive"

[[profiles]]
name = "PhoneMirror"
direction = "push"
destination = "$DST_MIRROR"
mode = "mirror"
EOF

echo ""
echo "=== Starting daemon ==="
$PRESYNC start
sleep 1
STATUS=$($PRESYNC status 2>&1)
assert_contains "daemon is running" "running" "$STATUS"

echo ""
echo "=== Pairing a device ==="
LOCAL_TOKEN=$(curl -s "$BASE/web/token" | json_get "d['token']")
PAIR_RESP=$(curl -s -X POST "$BASE/pair")
SESSION_TOKEN=$(echo "$PAIR_RESP" | json_get "d['token']")
curl -s -X POST "$BASE/pair/accept" \
    -H "Content-Type: application/json" \
    -d "{\"token\": \"$SESSION_TOKEN\", \"name\": \"phone-test\"}" > /dev/null
CONFIRM_RESP=$(curl -s -X POST -H "X-Presync-Token: $LOCAL_TOKEN" "$BASE/pair/confirm")
DEVICE_ID=$(echo "$CONFIRM_RESP" | json_get "d['device_id']")
LONG_TOKEN=$(echo "$CONFIRM_RESP" | json_get "d['token']")

assert_eq "device ID is 16 chars" "16" "${#DEVICE_ID}"
assert_eq "long token is 64 chars" "64" "${#LONG_TOKEN}"

echo ""
echo "=== Test 1: push files and verify content ==="
START=$(start_push "PhoneMusic" "$SRC_ADD")
SESSION=$(echo "$START" | json_get "d['session']")
FILE_COUNT=$(echo "$START" | json_get "len(d['files'])")
assert_eq "start returns two files" "2" "$FILE_COUNT"
assert_contains "start returns destination" "$DST_ADD" "$START"

push_chunk "$SESSION" "file1.txt" 0 "$SRC_ADD/file1.txt" > /dev/null
SHA=$(sha256sum "$SRC_ADD/file1.txt" | cut -d' ' -f1)
CONFIRM=$(push_confirm "$SESSION" "file1.txt" "$SHA")
assert_eq "file1 confirmed" "True" "$(echo "$CONFIRM" | json_get "d['ok']")"

push_chunk "$SESSION" "subdir/nested.txt" 0 "$SRC_ADD/subdir/nested.txt" > /dev/null
SHA=$(sha256sum "$SRC_ADD/subdir/nested.txt" | cut -d' ' -f1)
push_confirm "$SESSION" "subdir/nested.txt" "$SHA" > /dev/null

assert_file_exists "file1 landed on server" "$DST_ADD/file1.txt"
assert_file_exists "nested file landed on server" "$DST_ADD/subdir/nested.txt"
assert_files_equal "file1 content matches" "$SRC_ADD/file1.txt" "$DST_ADD/file1.txt"
assert_files_equal "nested content matches" "$SRC_ADD/subdir/nested.txt" "$DST_ADD/subdir/nested.txt"

COMPLETE=$(push_complete "$SESSION")
assert_eq "session completed" "True" "$(echo "$COMPLETE" | json_get "d['ok']")"

echo ""
echo "=== Test 1b: unchanged second push uploads nothing ==="
AGAIN=$(start_push "PhoneMusic" "$SRC_ADD")
AGAIN_SESSION=$(echo "$AGAIN" | json_get "d['session']")
AGAIN_COUNT=$(echo "$AGAIN" | json_get "len(d['files'])")
assert_eq "unchanged second push has no files" "0" "$AGAIN_COUNT"
push_complete "$AGAIN_SESSION" > /dev/null

echo ""
echo "=== Test 2: resume from server-held offset ==="
dd if=/dev/urandom of="$SRC_ADD/resume.bin" bs=1024 count=300 2>/dev/null
START=$(start_push "PhoneMusic" "$SRC_ADD")
SESSION=$(echo "$START" | json_get "d['session']")
head -c 102400 "$SRC_ADD/resume.bin" > "$PRESYNC_CONFIG_DIR/part1"
push_chunk "$SESSION" "resume.bin" 0 "$PRESYNC_CONFIG_DIR/part1" > /dev/null

START2=$(start_push "PhoneMusic" "$SRC_ADD")
OFFSET=$(echo "$START2" | json_get "d['files'][0]['offset']")
assert_eq "resume offset is 102400" "102400" "$OFFSET"
assert_eq "resume reuses session" "$(basename "$SESSION")" "$(basename "$(echo "$START2" | json_get "d['session']")")"

tail -c +102401 "$SRC_ADD/resume.bin" > "$PRESYNC_CONFIG_DIR/part2"
push_chunk "$SESSION" "resume.bin" 102400 "$PRESYNC_CONFIG_DIR/part2" > /dev/null
SHA=$(sha256sum "$SRC_ADD/resume.bin" | cut -d' ' -f1)
push_confirm "$SESSION" "resume.bin" "$SHA" > /dev/null
push_complete "$SESSION" > /dev/null
assert_files_equal "resumed file matches source" "$SRC_ADD/resume.bin" "$DST_ADD/resume.bin"

echo ""
echo "=== Test 3: checksum mismatch is rejected ==="
printf 'checksum-content' > "$SRC_ADD/bad.txt"
START=$(start_push "PhoneMusic" "$SRC_ADD")
SESSION=$(echo "$START" | json_get "d['session']")
push_chunk "$SESSION" "bad.txt" 0 "$SRC_ADD/bad.txt" > /dev/null
BAD=$(push_confirm "$SESSION" "bad.txt" "$(printf 'bad' | sha256sum | cut -d' ' -f1)")
assert_eq "mismatch returns ok=false" "False" "$(echo "$BAD" | json_get "d['ok']")"
assert_eq "mismatch asks for retry" "retry" "$(echo "$BAD" | json_get "d['action']")"
assert_file_missing "mismatched file not finalized" "$DST_ADD/bad.txt"

# The temp is discarded and the offset reset, so a full re-upload succeeds.
push_chunk "$SESSION" "bad.txt" 0 "$SRC_ADD/bad.txt" > /dev/null
GOOD_SHA=$(sha256sum "$SRC_ADD/bad.txt" | cut -d' ' -f1)
GOOD=$(push_confirm "$SESSION" "bad.txt" "$GOOD_SHA")
assert_eq "retry after mismatch confirms" "True" "$(echo "$GOOD" | json_get "d['ok']")"
assert_file_exists "retried file finalized" "$DST_ADD/bad.txt"
push_complete "$SESSION" > /dev/null

echo ""
echo "=== Test 4: empty file round-trips ==="
: > "$SRC_ADD/empty.txt"
START=$(start_push "PhoneMusic" "$SRC_ADD")
SESSION=$(echo "$START" | json_get "d['session']")
EMPTY_SHA=$(sha256sum "$SRC_ADD/empty.txt" | cut -d' ' -f1)
EMPTY_CONFIRM=$(push_confirm "$SESSION" "empty.txt" "$EMPTY_SHA")
assert_eq "empty file confirmed" "True" "$(echo "$EMPTY_CONFIRM" | json_get "d['ok']")"
assert_file_exists "empty file landed" "$DST_ADD/empty.txt"
push_complete "$SESSION" > /dev/null

echo ""
echo "=== Test 5: additive push never deletes ==="
printf 'server-extra' > "$DST_ADD/extra.mp3"
START=$(start_push "PhoneMusic" "$SRC_ADD")
SESSION=$(echo "$START" | json_get "d['session']")
push_complete "$SESSION" > /dev/null
assert_file_exists "additive profile keeps extra file" "$DST_ADD/extra.mp3"

echo ""
echo "=== Test 6: mirror deletion applies after complete ==="
START=$(start_push "PhoneMirror" "$SRC_MIRROR")
SESSION=$(echo "$START" | json_get "d['session']")
MIRROR_FILES=$(echo "$START" | json_get "len(d['files'])")
if [ "$MIRROR_FILES" != "0" ]; then
    push_chunk "$SESSION" "keep.mp3" 0 "$SRC_MIRROR/keep.mp3" > /dev/null
    SHA=$(sha256sum "$SRC_MIRROR/keep.mp3" | cut -d' ' -f1)
    push_confirm "$SESSION" "keep.mp3" "$SHA" > /dev/null
fi
assert_file_exists "mirror deletion waits for complete" "$DST_MIRROR/gone.mp3"
push_complete "$SESSION" > /dev/null
assert_file_missing "mirror deletion applied on complete" "$DST_MIRROR/gone.mp3"
assert_file_exists "mirror keeps source file" "$DST_MIRROR/keep.mp3"

echo ""
echo "=== Test 7: mirror refuses an empty source ==="
EMPTY_RESP=$(start_push "PhoneMirror" "$SRC_EMPTY")
assert_contains "empty mirror source rejected" "refusing to mirror" "$EMPTY_RESP"

echo ""
echo "=== Test 8: traversal path rejected ==="
TRAVERSAL='{"device_id":"'"$DEVICE_ID"'","profile":"PhoneMusic","source_manifest":{"root":"/x","files":[{"path":"../escape.txt","size":3,"mod_time":1}],"built_at":1}}'
TRAVERSAL_RESP=$(curl -s -X POST "$BASE/push" \
    -H "X-Presync-Token: $LONG_TOKEN" \
    -H "Content-Type: application/json" \
    -d "$TRAVERSAL")
assert_contains "traversal path rejected" "escapes the destination" "$TRAVERSAL_RESP"

echo ""
echo "=== Test 9: push profiles excluded from pull discovery ==="
PROFILES_RESP=$(curl -s -H "X-Presync-Token: $LONG_TOKEN" "$BASE/profiles")
assert_eq "no push profiles offered to pull clients" "[]" "$(echo "$PROFILES_RESP" | json_get "d['profiles']")"

echo ""
echo "=================================="
echo "Results: $PASS passed, $FAIL failed"
echo "=================================="
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
echo "ALL TESTS PASSED"
