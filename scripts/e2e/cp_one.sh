#!/bin/bash
# scripts/e2e/cp_one.sh
# Copy a single file from incoming/<peer>/<file> to a sibling
# file with the suffix specified by the user (e.g. "-cp" -> "-cp.bin").
#
# Usage:
#   bash scripts/e2e/cp_one.sh <peer_id> <filename> <target_basename>
# Example:
#   bash scripts/e2e/cp_one.sh 2f2d78fa7228ec92 test-1782300467.bin test-1782300467-cp.bin
set -u

INCOMING="$HOME/.config/cross-clipboard/incoming"
PEER="${1:-}"
SRC_FILE="${2:-}"
DST_FILE="${3:-}"

if [ -z "$PEER" ] || [ -z "$SRC_FILE" ] || [ -z "$DST_FILE" ]; then
  printf 'usage: %s <peer_id> <src_filename> <dst_filename>\n' "$0" >&2
  exit 2
fi

SRC="$INCOMING/$PEER/$SRC_FILE"
DST="$INCOMING/$PEER/$DST_FILE"

if [ ! -f "$SRC" ]; then
  printf 'FAIL  source not found: %s\n' "$SRC" >&2
  exit 2
fi

if cp -f "$SRC" "$DST"; then
  SA=$(sha256sum "$SRC" | awk '{print $1}')
  DB=$(sha256sum "$DST" | awk '{print $1}')
  if [ "$SA" = "$DB" ]; then
    printf '  PASS  %s -> %s  sha=%s\n' "$SRC" "$DST" "$SA"
    exit 0
  fi
  printf '  FAIL  %s -> %s  sha mismatch (src=%s dst=%s)\n' "$SRC" "$DST" "$SA" "$DB" >&2
  exit 1
fi

printf '  FAIL  cp %s -> %s\n' "$SRC" "$DST" >&2
exit 1
