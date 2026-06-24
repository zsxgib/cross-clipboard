#!/bin/bash
# Minimal e2e test - just call functions sequentially and log everywhere
set -u
WIN=Administrator@192.168.68.97
PASS=5566
LOG_L=/tmp/cross-clipboard-4002/linux-live.log
LINUX_INCOMING=/home/zsx/.config/cross-clipboard/incoming/
W2L_RESULT=/tmp/cross-clipboard-4002/e2e-w2l.json
L2W_RESULT=/tmp/cross-clipboard-4002/e2e-l2w.json

sha256() { sha256sum "$1" | awk '{print $1}'; }

run_w2l() {
  echo "[W2L] start ts=$(date +%s)"
  FNAME="e2e-w2l-$(date +%s).bin"
  FPATH_L="/tmp/cross-clipboard-4002/$FNAME"
  dd if=/dev/urandom of="$FPATH_L" bs=1024 count=1 2>/dev/null
  ORIG_SHA=$(sha256 "$FPATH_L")
  echo "[W2L] file=$FNAME"
  echo "[W2L] upload to win"
  sshpass -p $PASS scp -o StrictHostKeyChecking=no "$FPATH_L" \
    "$WIN:C:/Users/Administrator/Desktop/$FNAME" 2>&1 | tail -1
  echo "[W2L] write trigger"
  python3 /tmp/trig_writer.py "C:\\\\Users\\\\Administrator\\\\Desktop\\\\$FNAME" /tmp/trig.txt
  sshpass -p $PASS scp -o StrictHostKeyChecking=no /tmp/trig.txt \
    "$WIN:C:/Users/Administrator/AppData/Local/Temp/cross-clipboard/trigger.txt" 2>&1 | tail -1
  echo "[W2L] wait 30s"
  local s=0
  while [ $s -lt 30 ]; do
    if grep -q "received file: $FNAME" "$LOG_L" 2>/dev/null; then
      local RECV=$(find $LINUX_INCOMING -name "${FNAME}*" -mmin -1 2>/dev/null | head -1)
      if [ -n "$RECV" ]; then
        local RECV_SHA=$(sha256 "$RECV")
        if [ "$ORIG_SHA" = "$RECV_SHA" ]; then
          echo "[W2L] PASS"
          echo "{\"test\":\"W2L\",\"file\":\"$FNAME\",\"status\":\"PASS\"}" > $W2L_RESULT
          return 0
        fi
      fi
    fi
    sleep 1; s=$((s+1))
  done
  echo "[W2L] FAIL"
  echo "{\"test\":\"W2L\",\"file\":\"$FNAME\",\"status\":\"FAIL\"}" > $W2L_RESULT
  return 1
}

run_l2w() {
  echo "[L2W] start ts=$(date +%s)"
  FNAME="e2e-l2w-$(date +%s).bin"
  FPATH_L="/tmp/cross-clipboard-4002/$FNAME"
  dd if=/dev/urandom of="$FPATH_L" bs=1024 count=1 2>/dev/null
  ORIG_SHA=$(sha256 "$FPATH_L")
  echo "[L2W] file=$FNAME"
  pkill -f x11_fileclip_helper 2>/dev/null
  sleep 0.5
  nohup python3 /tmp/x11_fileclip_helper.py "$FPATH_L" </dev/null >/dev/null 2>&1 &
  disown
  sleep 2
  echo "[L2W] wait 30s"
  local s=0
  while [ $s -lt 30 ]; do
    if sshpass -p $PASS ssh -o StrictHostKeyChecking=no $WIN \
        "powershell -Command \"if (Select-String -Path 'C:\Users\Administrator\AppData\Local\Temp\cross-clipboard\app.log' -Pattern 'received file: $FNAME' -Quiet) { exit 0 } else { exit 1 }\"" >/dev/null 2>&1; then
      local WIN_RECV=$(sshpass -p $PASS ssh -o StrictHostKeyChecking=no $WIN \
        "powershell -Command \"Get-ChildItem -Path 'C:\Users\Administrator\.config\cross-clipboard\incoming' -Recurse -Filter '$FNAME' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName\"" 2>&1 | tail -1 | tr -d '\r')
      if [ -n "$WIN_RECV" ]; then
        local WIN_SHA=$(sshpass -p $PASS ssh -o StrictHostKeyChecking=no $WIN \
          "powershell -Command \"\$h=(Get-FileHash -Algorithm SHA256 -LiteralPath '$WIN_RECV').Hash.ToLower(); Write-Output \$h\"" 2>&1 | tail -1 | tr -d '\r')
        if [ "$ORIG_SHA" = "$WIN_SHA" ]; then
          echo "[L2W] PASS"
          echo "{\"test\":\"L2W\",\"file\":\"$FNAME\",\"status\":\"PASS\"}" > $L2W_RESULT
          return 0
        fi
      fi
    fi
    sleep 1; s=$((s+1))
  done
  echo "[L2W] FAIL"
  echo "{\"test\":\"L2W\",\"file\":\"$FNAME\",\"status\":\"FAIL\"}" > $L2W_RESULT
  return 1
}

W2L_OK=0; L2W_OK=0
echo "=== w2l ==="
run_w2l && W2L_OK=1
sleep 2
echo "=== l2w ==="
run_l2w && L2W_OK=1
echo "=== summary: w2l=$W2L_OK l2w=$L2W_OK ==="
if [ $W2L_OK = 1 ] && [ $L2W_OK = 1 ]; then
  echo "BOTH PASS"
  exit 0
fi
exit 1
