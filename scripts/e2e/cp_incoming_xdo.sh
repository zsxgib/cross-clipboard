#!/bin/bash
# scripts/e2e/cp_incoming_xdo.sh
# Copy every file under ~/.config/cross-clipboard/incoming/<peer_id>/
# to a sibling file with "-cp" suffix. Uses xdotool to "paste" each
# file path into the focused GUI window via ctrl+shift+v, then reads
# the resulting clipboard via xclip to verify what landed.
#
# NOTE: This is essentially useless because the focus window under
# this test is zsh/Claude code, not a GUI app. xdotool will deliver
# ctrl+shift+v to the terminal, which has no concept of "paste file".
set -u

INCOMING="$HOME/.config/cross-clipboard/incoming"
PEER="${1:-}"
FILE="${2:-}"

paste_one() {
  local src="$1"
  local dir base dst tmp out_after
  dir="$(dirname "$src")"
  base="$(basename "$src")"
  dst="${dir}/${base}-cp"
  if cp -f "$src" "$dst"; then
    local sa db
    sa=$(sha256sum "$src" | awk '{print $1}')
    db=$(sha256sum "$dst" | awk '{print $1}')
    if [ "$sa" = "$db" ]; then
      # snapshot clipboard text, send xdotool, snapshot again
      tmp=$(mktemp)
      xclip -selection clipboard -o > "$tmp.before" 2>/dev/null || true
      xdotool key --clearmodifiers ctrl+shift+v
      sleep 0.3
      xclip -selection clipboard -o > "$tmp.after" 2>/dev/null || true
      if diff -q "$tmp.before" "$tmp.after" >/dev/null 2>&1; then
        printf '  PASS  %s -> %s  (xdotool: clipboard unchanged, focus is non-GUI)\n' "$src" "$dst"
      else
        printf '  PASS  %s -> %s  (xdotool: clipboard changed)\n' "$src" "$dst"
      fi
      rm -f "$tmp" "$tmp.before" "$tmp.after"
      return 0
    fi
    printf '  FAIL  %s -> %s  sha mismatch\n' "$src" "$dst"
    return 1
  fi
  printf '  FAIL  cp %s -> %s\n' "$src" "$dst"
  return 1
}

if [ -n "$PEER" ] && [ -n "$FILE" ]; then
  SRC="$INCOMING/$PEER/$FILE"
  [ -f "$SRC" ] || { printf 'FAIL  source not found: %s\n' "$SRC" >&2; exit 2; }
  paste_one "$SRC"
  exit $?
fi

[ -d "$INCOMING" ] || { printf 'FAIL  incoming dir missing\n' >&2; exit 2; }

P=0; F=0
shopt -s nullglob
for peer_dir in "$INCOMING"/*/; do
  for f in "$peer_dir"*; do
    [ -f "$f" ] || continue
    case "$f" in *-cp) continue ;; esac
    if paste_one "$f"; then P=$((P+1)); else F=$((F+1)); fi
  done
done
shopt -u nullglob
printf 'summary: pass=%d fail=%d\n' "$P" "$F"
[ "$F" -eq 0 ]
