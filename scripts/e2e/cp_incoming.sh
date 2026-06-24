#!/bin/bash
# scripts/e2e/cp_incoming.sh
# Copy every file under ~/.config/cross-clipboard/incoming/<peer_id>/
# to a sibling file with a "-cp" suffix appended to the basename.
# Verifies cross-clipboard upload pipeline (read + write + checksum
# integrity for arbitrary user data).
set -u

INCOMING="$HOME/.config/cross-clipboard/incoming"
PEER="${1:-}"
FILE="${2:-}"

copy_one() {
  local src="$1"
  local dir base dst
  dir="$(dirname "$src")"
  base="$(basename "$src")"
  dst="${dir}/${base}-cp"
  if cp -f "$src" "$dst"; then
    local sa db
    sa=$(sha256sum "$src" | awk '{print $1}')
    db=$(sha256sum "$dst" | awk '{print $1}')
    if [ "$sa" = "$db" ]; then
      printf '  PASS  %s -> %s\n' "$src" "$dst"
      return 0
    else
      printf '  FAIL  %s -> %s  sha mismatch\n' "$src" "$dst"
      return 1
    fi
  else
    printf '  FAIL  cp %s -> %s\n' "$src" "$dst"
    return 1
  fi
}

if [ -n "$PEER" ] && [ -n "$FILE" ]; then
  SRC="$INCOMING/$PEER/$FILE"
  if [ ! -f "$SRC" ]; then
    printf 'FAIL  source not found: %s\n' "$SRC" >&2
    exit 2
  fi
  copy_one "$SRC"
  exit $?
fi

if [ ! -d "$INCOMING" ]; then
  printf 'FAIL  incoming dir missing: %s\n' "$INCOMING" >&2
  exit 2
fi

P=0; F=0
shopt -s nullglob
for peer_dir in "$INCOMING"/*/; do
  peer="$(basename "$peer_dir")"
  for f in "$peer_dir"*; do
    [ -f "$f" ] || continue
    case "$f" in
      *-cp) continue ;;  # skip already-copied
    esac
    if copy_one "$f"; then P=$((P+1)); else F=$((F+1)); fi
  done
done
shopt -u nullglob
printf 'summary: pass=%d fail=%d\n' "$P" "$F"
[ "$F" -eq 0 ]
