#!/bin/bash
# scripts/e2e/xdo_copy.sh
# 用 xdotool 聚焦 nautilus '2f2d78fa7228ec92' 窗口 -> Ctrl+A -> Ctrl+C
# 把文件复制进剪贴板 (剪贴板同时含 text/uri-list 和 text/plain)
#
# 用法:
#   /home/zsx/tmp/cross-device-sync/cross-clipboard/scripts/e2e/xdo_copy.sh
#
# 可调用的 hook:
#   WIN_ID   源窗口 id (默认 60821710, nautilus '2f2d78fa7228ec92')
set -u

WIN_ID="${WIN_ID:-60821710}"

echo "=== xdo_copy: focus window $WIN_ID ==="
/usr/bin/xdotool windowraise "$WIN_ID"
/usr/bin/xdotool windowactivate "$WIN_ID"
/usr/bin/xdotool windowfocus "$WIN_ID"
/bin/sleep 0.5
FOC=$(/usr/bin/xdotool getwindowfocus 2>/dev/null || echo none)
if [ "$FOC" != "$WIN_ID" ]; then
  echo "FAIL  focus not on $WIN_ID (got $FOC)"
  exit 1
fi
echo "  focus OK: $FOC"

echo "=== xdo_copy: Ctrl+A ==="
/usr/bin/xdotool key --clearmodifiers ctrl+a
/bin/sleep 0.2

echo "=== xdo_copy: Ctrl+C ==="
/usr/bin/xdotool key --clearmodifiers ctrl+c
/bin/sleep 0.3

echo "=== clipboard state ==="
echo "  -- TARGETS --"
/usr/bin/xclip -selection clipboard -t TARGETS -o 2>/dev/null | /usr/bin/sed 's/^/    /'
echo "  -- text/uri-list --"
/usr/bin/xclip -selection clipboard -t text/uri-list -o 2>/dev/null | /usr/bin/sed 's/^/    /'
echo "  -- text/plain --"
/usr/bin/xclip -selection clipboard -t text/plain -o 2>/dev/null | /usr/bin/sed 's/^/    /'

echo "=== xdo_copy: DONE (clipboard now has file URI) ==="
