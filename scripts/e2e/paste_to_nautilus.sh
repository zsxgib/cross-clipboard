#!/bin/bash
# scripts/e2e/paste_to_nautilus.sh
# 从当前焦点切到 Nautilus 主窗口，再 xdotool 发 ctrl+shift+v 粘贴剪贴板
#
# 用法:
#   /home/zsx/tmp/cross-device-sync/cross-clipboard/scripts/e2e/paste_to_nautilus.sh
#   /home/zsx/tmp/cross-device-sync/cross-clipboard/scripts/e2e/paste_to_nautilus.sh 'cc-test'
#
# 参数 (可选): 窗口标题关键字（不传 = 挑当前 visible 中最大的那个）
set -u

KEYWORD="${1:-}"

# 1) 选窗口：只考虑 visible + 面积大（>200x200）+ title 包含 keyword（如果有）
pick_window() {
  /usr/bin/xdotool search --onlyvisible --class 'nautilus' 2>/dev/null | while read w; do
    geo=$(/usr/bin/xdotool getwindowgeometry "$w" 2>/dev/null | /usr/bin/awk '/Geometry:/ {print $2}')
    w_px=$(echo "$geo" | /usr/bin/cut -d'x' -f1)
    h_px=$(echo "$geo" | /usr/bin/cut -d'x' -f2)
    name=$(/usr/bin/xdotool getwindowname "$w" 2>/dev/null)
    if [ -n "$KEYWORD" ]; then
      case "$name" in
        *"$KEYWORD"*) : ;;
        *) continue ;;
      esac
    fi
    if [ -n "$w_px" ] && [ -n "$h_px" ] && [ "$w_px" -gt 200 ] && [ "$h_px" -gt 200 ]; then
      area=$((w_px * h_px))
      echo "$area $w $w_px $h_px $name"
    fi
  done | /usr/bin/sort -rn | /usr/bin/head -1
}

PICK=$(pick_window)
if [ -z "$PICK" ]; then
  echo "FAIL  no visible nautilus window matched (keyword='${KEYWORD:-<none>}')"
  exit 1
fi

WIN=$(echo "$PICK" | /usr/bin/awk '{print $2}')
W=$(echo "$PICK" | /usr/bin/awk '{print $3}')
H=$(echo "$PICK" | /usr/bin/awk '{print $4}')
NAME=$(echo "$PICK" | /usr/bin/cut -d' ' -f5-)
echo "target: id=$WIN ${W}x${H} name='$NAME'"

# 2) 切焦点
/usr/bin/xdotool windowraise "$WIN" 2>/dev/null || true
/usr/bin/xdotool windowactivate "$WIN" 2>/dev/null || true
/usr/bin/xdotool windowfocus "$WIN" 2>/dev/null || true
/bin/sleep 0.5

# 3) 确认当前焦点
FOCUSED=$(/usr/bin/xdotool getwindowfocus 2>/dev/null || echo 'none')
FNAME=$(/usr/bin/xdotool getwindowname "$FOCUSED" 2>/dev/null || echo '?')
echo "focused now: id=$FOCUSED name='$FNAME'"

if [ "$FOCUSED" != "$WIN" ]; then
  echo "WARN  focus switch failed (still on $FOCUSED, want $WIN). Will send key to target window directly."
  /usr/bin/xdotool key --window "$WIN" --clearmodifiers ctrl+shift+v
  echo "sent: /usr/bin/xdotool key --window $WIN --clearmodifiers ctrl+shift+v"
else
  /usr/bin/xdotool key --clearmodifiers ctrl+shift+v
  echo "sent: /usr/bin/xdotool key --clearmodifiers ctrl+shift+v"
fi
