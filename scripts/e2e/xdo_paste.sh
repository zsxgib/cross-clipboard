#!/bin/bash
# scripts/e2e/xdo_paste.sh
# 用 xdotool 聚焦目标 nautilus 窗口 -> Ctrl+V -> 选 "Keep Both" -> F2 重命名新文件
# 假设剪贴板里已经有 text/uri-list (即 xdo_copy.sh 跑过)
#
# 用法:
#   /home/zsx/tmp/cross-device-sync/cross-clipboard/scripts/e2e/xdo_paste.sh '副本名字'
#
# 参数:
#   $1  目标新文件的前缀名 (不含扩展名, F2 进入重命名后 Nautilus 默认只选中前缀)
#       默认 = 'test-1782300467-cp'
#
# 可调用的环境变量:
#   WIN_ID  目标窗口 id (默认 60821710, 同目录复制)
set -u

DST_BASENAME="${1:-test-1782300467-cp}"
WIN_ID="${WIN_ID:-60821710}"

# 1) 切焦点 (重试 3 次, GNOME X11 有时切完要点时间)
echo "=== xdo_paste: focus window $WIN_ID ==="
FOC=""
for i in 1 2 3; do
  /usr/bin/xdotool windowraise "$WIN_ID" 2>/dev/null
  /usr/bin/xdotool windowactivate "$WIN_ID" 2>/dev/null
  /usr/bin/xdotool windowfocus "$WIN_ID" 2>/dev/null
  /bin/sleep 0.3
  FOC=$(/usr/bin/xdotool getwindowfocus 2>/dev/null || echo none)
  if [ "$FOC" = "$WIN_ID" ]; then
    break
  fi
  echo "  retry $i: focused=$FOC want=$WIN_ID"
done
if [ "$FOC" != "$WIN_ID" ]; then
  echo "FAIL  focus not on $WIN_ID (got $FOC after 3 retries)"
  exit 1
fi
echo "  focus OK: $FOC"

# 2) 剪贴板检查
URI=$(/usr/bin/xclip -selection clipboard -t text/uri-list -o 2>/dev/null)
if [ -z "$URI" ]; then
  echo "FAIL  clipboard has no text/uri-list; run xdo_copy.sh first"
  exit 1
fi
echo "=== xdo_paste: clipboard uri-list ==="
echo "$URI" | /usr/bin/sed 's/^/    /'

# 3) Ctrl+V
echo "=== xdo_paste: Ctrl+V ==="
/usr/bin/xdotool key --clearmodifiers ctrl+v
/bin/sleep 0.6

# 4) 选 Keep Both (Nautilus 默认焦点在 Replace 按钮)
echo "=== xdo_paste: dialog -> Keep Both ==="
/usr/bin/xdotool key --clearmodifiers Right
/bin/sleep 0.2
/usr/bin/xdotool key --clearmodifiers Return
/bin/sleep 0.5

# 5) 选新文件 -> F2 -> type 前缀 -> Enter
echo "=== xdo_paste: F2 rename (type '$DST_BASENAME') ==="
/usr/bin/xdotool key --clearmodifiers Right
/bin/sleep 0.2
/usr/bin/xdotool key --clearmodifiers F2
/bin/sleep 0.3
/usr/bin/xdotool type --clearmodifiers "$DST_BASENAME"
/bin/sleep 0.2
/usr/bin/xdotool key --clearmodifiers Return
/bin/sleep 0.3

echo "=== xdo_paste: DONE ==="
