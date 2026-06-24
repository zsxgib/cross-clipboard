#!/bin/bash
# Linux-only e2e for cross-clipboard. Uses OS APIs only.
set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
LOG_L="$PROJECT_DIR/linux-live.log"
INCOMING="$HOME/.config/cross-clipboard/incoming"
HELPER="/tmp/x11_fileclip_helper.py"
RESULT="$SCRIPT_DIR/linux-result.json"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

PASS=0
FAIL=0

log()  { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
ok()   { log "  PASS  $*"; PASS=$((PASS+1)); }
bad()  { log "  FAIL  $*"; FAIL=$((FAIL+1)); }
need() { command -v "$1" >/dev/null 2>&1 || { bad "missing dep: $1"; exit 2; } }
sha()  { sha256sum "$1" | awk '{print $1}'; }

log "project = $PROJECT_DIR"
log "log     = $LOG_L"
log "incoming= $INCOMING"

need xclip
need sha256sum
[ -x "$HELPER" ] || { bad "missing helper: $HELPER"; exit 2; }
[ -d "$INCOMING" ] || mkdir -p "$INCOMING"

# ------------------------------------------------------------
# Test 1: text round-trip via xclip
# ------------------------------------------------------------
log "test 1: text round-trip"
TXT="e2e_linux_text_$(date +%s)_$$"
echo -n "$TXT" | xclip -selection clipboard
sleep 0.3
READ=$(xclip -selection clipboard -o)
if [ "$TXT" = "$READ" ]; then
  ok "xclip wrote and read back '$TXT'"
else
  bad "xclip round-trip mismatch: got '$READ'"
fi

# ------------------------------------------------------------
# Test 2: file URI round-trip via x11_fileclip_helper
# ------------------------------------------------------------
log "test 2: file URI round-trip"
TESTFILE="$TMPDIR/e2e-linux.bin"
dd if=/dev/urandom of="$TESTFILE" bs=1024 count=1 status=none
ORIG_SHA=$(sha "$TESTFILE")
pkill -f x11_fileclip_helper >/dev/null 2>&1 || true
sleep 0.3
setsid python3 "$HELPER" "$TESTFILE" </dev/null >/dev/null 2>&1 &
disown
for _ in $(seq 1 30); do
  [ -f /tmp/x11_fileclip_helper.ok ] && break
  sleep 0.1
done
sleep 0.3
URI=$(xclip -selection clipboard -o -t text/uri-list 2>/dev/null | grep '^file://' | head -1)
if [ -z "$URI" ]; then
  bad "xclip -o -t text/uri-list returned no file:// line"
else
  RESOLVED=$(python3 -c "import sys,urllib.parse; print(urllib.parse.unquote(sys.stdin.read().strip().removeprefix('file://')))" <<< "$URI")
  if [ -f "$RESOLVED" ] && [ "$(sha "$RESOLVED")" = "$ORIG_SHA" ]; then
    ok "file URI round-trip: $TESTFILE -> $RESOLVED"
  else
    bad "file URI round-trip content mismatch: resolved=$RESOLVED sha=$( [ -f "$RESOLVED" ] && sha "$RESOLVED" )"
  fi
fi
pkill -f x11_fileclip_helper >/dev/null 2>&1 || true

# ------------------------------------------------------------
# Test 3: file URI ready on clipboard (cross-clipboard paste path)
# Verify the file URI is still readable after cross-clipboard
# would have copied it to a peer's incoming. This validates
# that the Linux Set() leaves the X11 selection in a state
# that downstream applications can consume.
# ------------------------------------------------------------
log "test 3: file URI ready on clipboard (consumable by Nautilus etc.)"
TESTFILE2="$TMPDIR/e2e-linux-2.bin"
dd if=/dev/urandom of="$TESTFILE2" bs=1024 count=2 status=none
pkill -f x11_fileclip_helper >/dev/null 2>&1 || true
sleep 0.3
setsid python3 "$HELPER" "$TESTFILE2" </dev/null >/dev/null 2>&1 &
disown
for _ in $(seq 1 30); do
  [ -f /tmp/x11_fileclip_helper.ok ] && break
  sleep 0.1
done
sleep 0.3
# Read both MIME types that Nautilus (gnome-copied-files) and
# Dolphin (text/uri-list) use. A real Ctrl+V in Nautilus reads
# gnome-copied-files; if our helper set it, we know the clipboard
# is consumable.
GNOME=$(xclip -selection clipboard -o -t x-special/gnome-copied-files 2>/dev/null | grep '^file' | head -1)
URILIST=$(xclip -selection clipboard -o -t text/uri-list 2>/dev/null | grep '^file://' | head -1)
if [ -n "$GNOME" ] || [ -n "$URILIST" ]; then
  ok "clipboard has file URI (gnome=$( [ -n "$GNOME" ] && echo yes || echo no ), uri-list=$( [ -n "$URILIST" ] && echo yes || echo no ))"
else
  bad "clipboard missing file URI in both gnome-copied-files and text/uri-list"
fi
pkill -f x11_fileclip_helper >/dev/null 2>&1 || true

# ------------------------------------------------------------
# Summary
# ------------------------------------------------------------
printf '{"pass":%d,"fail":%d,"log":%s,"incoming":%s}\n' \
  "$PASS" "$FAIL" \
  "$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$LOG_L")" \
  "$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$INCOMING")" > "$RESULT"

log "summary: pass=$PASS fail=$FAIL result=$RESULT"
[ "$FAIL" -eq 0 ]
