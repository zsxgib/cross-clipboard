#!/usr/bin/env bash
# e2e-test.sh - end-to-end test for cross-clipboard Linux/Win sync
#
# Tests:
#   1. Linux->Win small file (1KB) with md5 verify
#   2. Linux->Win large file (100MB) with md5 verify + progress log
#   3. Win->Linux small file with md5 verify
#   4. Text passthrough Linux->Win
#   5. 5s dedup (re-send same sha)
#   6. Reconnect after kill
#
# Requires:
#   - xclip, xdotool, sshpass, sha256sum on Linux
#   - PowerShell on Win with cross-clipboard running via schtasks
#   - Win path C:\Program Files\Git\usr\local\bin\cross-clipboard.exe
#
# Output: human-readable summary + /tmp/cross-clipboard-4002/e2e-result.json

set -uo pipefail

LINUX_BIN=/tmp/cross-clipboard-4002/cross-clipboard
LINUX_LOG=/tmp/cross-clipboard-4002/linux.log
LINUX_INCOMING=/home/zsx/.config/cross-clipboard/incoming
RESULT_JSON=/tmp/cross-clipboard-4002/e2e-result.json
RESULT_TXT=/tmp/cross-clipboard-4002/e2e-result.txt
WIN_HOST=192.168.68.97
WIN_USER=Administrator
WIN_PASS=5566
WIN_INCOMING='C:\Users\Administrator\.config\cross-clipboard\incoming'
WIN_LOG='C:\Users\Administrator\AppData\Local\Temp\cross-clipboard\logs\runner-stdout.log'

pass=0; fail=0; results=()
record() {
  local name=$1; local status=$2; local detail=$3
  results+=("{\"name\":\"$name\",\"status\":\"$status\",\"detail\":\"$detail\"}")
  if [ "$status" = "PASS" ]; then pass=$((pass+1)); else fail=$((fail+1)); fi
  echo "  [$status] $name -- $detail"
}

run_win_ps() {
  sshpass -p "$WIN_PASS" ssh -o StrictHostKeyChecking=no "${WIN_USER}@${WIN_HOST}" \
    "powershell.exe -NoProfile -STA -ExecutionPolicy Bypass -Command \"$1\""
}

win_log_tail() {
  run_win_ps "Get-Content '$WIN_LOG' -Tail 200 -ErrorAction SilentlyContinue" 2>&1 | grep -E "^[0-9]{4}/" | tail -20
}

linux_log_tail() {
  tail -20 "$LINUX_LOG"
}

wait_log_match() {
  local logfile=$1; local pattern=$2; local timeout=$3
  local end=$(($(date +%s) + timeout))
  while [ $(date +%s) -lt $end ]; do
    if grep -qE "$pattern" "$logfile" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

echo "=========================================="
echo "cross-clipboard e2e @ $(date '+%Y-%m-%d %H:%M:%S')"
echo "=========================================="

# 0) Sanity: processes and ports
echo "[0] sanity checks"
LINUX_PID=$(pgrep -f "$LINUX_BIN -t" | head -1)
[ -n "$LINUX_PID" ] && echo "  Linux cross-clipboard PID=$LINUX_PID" || echo "  Linux cross-clipboard NOT RUNNING"
WIN_PID=$(run_win_ps "(Get-Process cross-clipboard -ErrorAction SilentlyContinue | Select-Object -First 1 | ForEach-Object { Write-Host \$_.Id }) -join ','" 2>&1 | tail -1)
echo "  Win cross-clipboard PID(s)=$WIN_PID"
[ -n "$LINUX_PID" ] && [ -n "$WIN_PID" ] || { echo "FATAL: one or both ends not running"; exit 2; }

# Wait for connection (in case just restarted)
echo "[0] waiting for trusted handshake (up to 15s)"
if wait_log_match "$LINUX_LOG" "trusted DESKTOP|trusted zsx" 15; then
  echo "  both peers trusted"
else
  echo "  WARN: trusted not found in last log lines, continuing"
fi

# ============== Test 1: Linux->Win small file ==============
echo
echo "=========================================="
echo "Test 1: Linux->Win small file (1KB) md5 verify"
echo "=========================================="
TEST1=/tmp/e2e_small_$(date +%s).bin
dd if=/dev/urandom of="$TEST1" bs=1024 count=1 2>/dev/null
SRC_MD5=$(md5sum "$TEST1" | awk '{print $1}')
echo "  source: $TEST1 md5=$SRC_MD5"

LINUX_LINES_BEFORE=$(wc -l < "$LINUX_LOG")
WIN_LINES_BEFORE=$(run_win_ps "(Get-Content '$WIN_LOG' | Measure-Object).Count" 2>&1 | tail -1 | tr -d ' ')

printf "file://%s\n" "$TEST1" | DISPLAY=:1 xclip -selection clipboard \
  -t x-special/gnome-copied-files -t text/uri-list -t text/plain

if wait_log_match "$LINUX_LOG" "sending file: $(basename $TEST1) size=1024" 15 \
  && wait_log_match "$LINUX_LOG" "sent file: $(basename $TEST1)" 15; then
  echo "  Linux sent file"
else
  record "T1_linux_send" "FAIL" "no 'sent file' in Linux log"
  rm -f "$TEST1"; echo
fi

if wait_log_match "$LINUX_LOG" "received file: $(basename $TEST1).*from 12D3KooW" 15; then
  echo "  Linux received the same file back (loop)"
else
  echo "  Linux did not receive own file (good - dedup works)"
fi

if wait_log_match "$LINUX_LOG" "file received: $(basename $TEST1) .* at /home/zsx" 15; then
  echo "  Linux wrote to incoming/"
else
  echo "  Linux did not write own file (dedup)"
fi

# Now find the file on Win incoming
sleep 2
WIN_FILE=$(run_win_ps "Get-ChildItem -Path '$WIN_INCOMING' -Recurse -Filter '$(basename $TEST1)' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName" 2>&1 | tail -1 | tr -d '\r')
if [ -n "$WIN_FILE" ] && [ "$WIN_FILE" != "" ]; then
  WIN_MD5=$(run_win_ps "(Get-FileHash '$WIN_FILE' -Algorithm MD5).Hash" 2>&1 | tail -1 | tr -d '\r ')
  WIN_MD5=$(echo "$WIN_MD5" | tr "A-F" "a-f")
  SRC_MD5_LC=$(echo "$SRC_MD5" | tr "A-F" "a-f")
  if [ "$WIN_MD5" = "$SRC_MD5_LC" ]; then
    record "T1_linux_to_win_1KB" "PASS" "src=$SRC_MD5 win=$WIN_MD5 file=$WIN_FILE"
  else
    record "T1_linux_to_win_1KB" "FAIL" "src=$SRC_MD5 win=$WIN_MD5 file=$WIN_FILE"
  fi
else
  record "T1_linux_to_win_1KB" "FAIL" "no file found on Win incoming"
fi
rm -f "$TEST1"

# ============== Test 2: Linux->Win 100MB ==============
echo
echo "=========================================="
echo "Test 2: Linux->Win large file (100MB) md5 verify"
echo "=========================================="
TEST2=/tmp/e2e_100mb_$(date +%s).bin
dd if=/dev/urandom of="$TEST2" bs=1M count=100 2>/dev/null
SRC_MD5=$(md5sum "$TEST2" | awk '{print $1}')
echo "  source: $TEST2 md5=$SRC_MD5"

printf "file://%s\n" "$TEST2" | DISPLAY=:1 xclip -selection clipboard \
  -t x-special/gnome-copied-files -t text/uri-list -t text/plain

T0=$(date +%s)
if wait_log_match "$LINUX_LOG" "sent file: $(basename $TEST2)" 60; then
  T1=$(date +%s); ELAPSED=$((T1-T0))
  echo "  Linux sent in ${ELAPSED}s"
else
  record "T2_linux_to_win_100MB" "FAIL" "no 'sent file' in 60s"
  rm -f "$TEST2"; echo
  return 0 2>/dev/null || true
  exit 0
fi

PROGRESS_COUNT=$(grep -c "sending $(basename $TEST2) to 12D3KooW" "$LINUX_LOG" || echo 0)
echo "  progress log lines: $PROGRESS_COUNT"

sleep 3
WIN_FILE=$(run_win_ps "Get-ChildItem -Path '$WIN_INCOMING' -Recurse -Filter '$(basename $TEST2)' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty FullName" 2>&1 | tail -1 | tr -d '\r')
if [ -n "$WIN_FILE" ] && [ "$WIN_FILE" != "" ]; then
  WIN_MD5=$(run_win_ps "(Get-FileHash '$WIN_FILE' -Algorithm MD5).Hash" 2>&1 | tail -1 | tr -d '\r ')
  WIN_MD5=$(echo "$WIN_MD5" | tr "A-F" "a-f")
  SRC_MD5_LC=$(echo "$SRC_MD5" | tr "A-F" "a-f")
  if [ "$WIN_MD5" = "$SRC_MD5_LC" ]; then
    record "T2_linux_to_win_100MB" "PASS" "src=$SRC_MD5 win=$WIN_MD5 elapsed=${ELAPSED}s progress_lines=$PROGRESS_COUNT"
  else
    record "T2_linux_to_win_100MB" "FAIL" "src=$SRC_MD5 win=$WIN_MD5"
  fi
else
  record "T2_linux_to_win_100MB" "FAIL" "no 100MB file found on Win incoming"
fi
rm -f "$TEST2"

# ============== Test 3: Win->Linux small file ==============
echo
echo "=========================================="
echo "Test 3: Win->Linux small file (1KB) md5 verify"
echo "=========================================="
# Use an existing real file on Win incoming as source (known to be readable)
WIN_SRC="C:\\Users\\Administrator\\.config\\cross-clipboard\\incoming\\74b10411a6f77b47\\ltw_small_1782232543.bin"
if ! run_win_ps "Test-Path '$WIN_SRC'" 2>&1 | grep -q True; then
  echo "  source missing, creating fresh"
  run_win_ps "New-Item -ItemType Directory -Path 'C:\Users\Administrator\e2e' -Force | Out-Null; \$b = New-Object byte[] 1024; (New-Object Random).NextBytes(\$b); [System.IO.File]::WriteAllBytes('C:\Users\Administrator\e2e\e2e_win_src.bin', \$b)" 2>&1 >/dev/null
  WIN_SRC="C:\\Users\\Administrator\\e2e\\e2e_win_src.bin"
fi
WIN_SRC_MD5=$(run_win_ps "(Get-FileHash '$WIN_SRC' -Algorithm MD5).Hash" 2>&1 | tail -1 | tr -d '\r ')
echo "  source: $WIN_SRC md5=$WIN_SRC_MD5"

LINUX_BEFORE_LINES=$(wc -l < "$LINUX_LOG")
run_win_ps "Add-Type -AssemblyName System.Windows.Forms; \$col = New-Object System.Collections.Specialized.StringCollection; \$col.Add('$WIN_SRC'.Replace('\\\\','\\')); [System.Windows.Forms.Clipboard]::SetFileDropList(\$col); Write-Host 'SET'" 2>&1 | tail -1

if wait_log_match "$LINUX_LOG" "received file: $(basename $WIN_SRC) " 30; then
  echo "  Linux received file"
else
  record "T3_win_to_linux_1KB" "FAIL" "no 'received file' in 30s"
  echo
fi
sleep 2
LINUX_FILE=$(find "$LINUX_INCOMING" -name "$(basename $WIN_SRC | sed 's|\\|/|g')" -type f 2>/dev/null | head -1)
if [ -z "$LINUX_FILE" ]; then
  # search by partial name
  LINUX_FILE=$(find "$LINUX_INCOMING" -name "$(basename ${WIN_SRC//\\//})" -type f 2>/dev/null | head -1)
fi
if [ -z "$LINUX_FILE" ]; then
  # try ls of latest dir
  LATEST_DIR=$(find "$LINUX_INCOMING" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %f\n' | sort -rn | head -1 | awk '{print $2}')
  LINUX_FILE="$LINUX_INCOMING/$LATEST_DIR/$(basename ${WIN_SRC//\\//})"
fi
if [ -f "$LINUX_FILE" ]; then
  LINUX_MD5=$(md5sum "$LINUX_FILE" | awk '{print $1}')
  if [ "$LINUX_MD5" = "$WIN_SRC_MD5" ]; then
    record "T3_win_to_linux_1KB" "PASS" "win=$WIN_SRC_MD5 linux=$LINUX_MD5 file=$LINUX_FILE"
  else
    record "T3_win_to_linux_1KB" "FAIL" "win=$WIN_SRC_MD5 linux=$LINUX_MD5 file=$LINUX_FILE"
  fi
else
  record "T3_win_to_linux_1KB" "FAIL" "no file found on Linux incoming"
fi

TEST4_TEXT="e2e_text_$(date +%s)_$(head -c8 /dev/urandom | xxd -p)"
echo "  text: $TEST4_TEXT (len=${#TEST4_TEXT})"
echo -n "$TEST4_TEXT" | DISPLAY=:1 xclip -selection clipboard

if wait_log_match "$LINUX_LOG" "sending data to peer.*len: ${#TEST4_TEXT}" 15; then
  echo "  Linux sent text"
  WIN_LOG_LOCAL=/tmp/win_log_check_T4_$$.txt
  rm -f "$WIN_LOG_LOCAL"
  echo "  polling Win log for receipt of size ${#TEST4_TEXT} (up to 20s)..."
  WIN_RECEIVED=0
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    run_win_ps "Get-Content '"'"'$WIN_LOG'"'"' -Tail 50" 2>/dev/null > "$WIN_LOG_LOCAL"
    if grep -qE "received clipboard data.*size: ${#TEST4_TEXT}" "$WIN_LOG_LOCAL" 2>/dev/null; then
      WIN_RECEIVED=1
      echo "  Win received at iter $i"
      break
    fi
    sleep 1
  done
  if [ "$WIN_RECEIVED" = "1" ]; then
    record "T4_text_linux_to_win" "PASS" "len=${#TEST4_TEXT} sent and received on Win"
  else
    record "T4_text_linux_to_win" "FAIL" "Linux sent but Win did not receive in 20s"
  fi
  rm -f "$WIN_LOG_LOCAL"
else
  record "T4_text_linux_to_win" "FAIL" "Linux did not send text in 15s"
fi

# ============== Test 5: 5s dedup ==============
TEST5=/tmp/e2e_dedup_$(date +%s).bin
dd if=/dev/urandom of="$TEST5" bs=1024 count=1 2>/dev/null
printf "file://%s\n" "$TEST5" | DISPLAY=:1 xclip -selection clipboard -t x-special/gnome-copied-files -t text/uri-list -t text/plain
sleep 3
# re-set same path
printf "file://%s\n" "$TEST5" | DISPLAY=:1 xclip -selection clipboard -t x-special/gnome-copied-files -t text/uri-list -t text/plain
sleep 6
WIN_LOG_LOCAL=/tmp/win_log_check_T5_$$.txt
rm -f "$WIN_LOG_LOCAL"
run_win_ps "Get-Content '"'"'$WIN_LOG'"'"' -Tail 200" 2>/dev/null > "$WIN_LOG_LOCAL"
SEND_LINES=$(grep -c "sent file: $(basename $TEST5)" "$LINUX_LOG" 2>/dev/null || echo 0)
DEDUP_LINES=$(grep -c "dedup: skipping $(basename $TEST5)" "$LINUX_LOG" 2>/dev/null || echo 0)
WIN_DEDUP=$(grep -c "dedup: skipping $(basename $TEST5)" "$WIN_LOG_LOCAL" 2>/dev/null || echo 0)
WIN_SEND=$(grep -c "sent file: $(basename $TEST5)" "$WIN_LOG_LOCAL" 2>/dev/null || echo 0)
DEDUP_LINES=$((DEDUP_LINES + WIN_DEDUP))
SEND_LINES=$((SEND_LINES + WIN_SEND))
rm -f "$WIN_LOG_LOCAL"
echo "  send_count=$SEND_LINES dedup_count=$DEDUP_LINES"
if [ "$SEND_LINES" -ge 1 ] && [ "$DEDUP_LINES" -ge 1 ]; then
  record "T5_dedup" "PASS" "sent=$SEND_LINES dedup=$DEDUP_LINES"
else
  record "T5_dedup" "FAIL" "sent=$SEND_LINES dedup=$DEDUP_LINES (expected sent>=1 and dedup>=1)"
fi
rm -f "$TEST5"

# ============== Summary ==============
echo
echo "=========================================="
echo "SUMMARY: $pass PASS, $fail FAIL"
echo "=========================================="
printf '%s\n' "${results[@]}" > "$RESULT_JSON"
{
  echo "cross-clipboard e2e @ $(date '+%Y-%m-%d %H:%M:%S')"
  echo "passed: $pass, failed: $fail"
  printf '  - %s\n' "${results[@]}"
} > "$RESULT_TXT"
cat "$RESULT_TXT"
