# E2E Test: cross-clipboard file copy

End-to-end test that verifies Win ↔ Linux bidirectional file copy works
across a P2P clipboard sync. The test runs both directions and SHA-verifies
the received file content.

## Prerequisites

- Both binaries running:
  - Win: `C:\Program Files\Git\usr\local\bin\cross-clipboard.exe` (via
    scheduled task `ccb-daemon` with `-t -trigger-file ...`)
  - Linux: `/tmp/cross-clipboard-4002/cross-clipboard` (started with
    `setsid nohup ./cross-clipboard -t </dev/null >linux-live.log 2>&1 &`)
- The Win scheduled task must be configured with `CROSS_CLIPBOARD_LOG_FILE`
  so the test can grep its log via SSH.
- `x11_fileclip_helper.py` is the helper script that sets the X11
  clipboard with the right MIME types; it lives at `/tmp/x11_fileclip_helper.py`
  on Linux and is invoked by the test to set a file URI as if a file
  manager had copied it.

## Running

```bash
/tmp/e2e4.sh
```

Exit 0 if both directions pass, non-zero otherwise.

## Assertions

1. W2L: a 1 KiB random file is uploaded to `C:\Users\Administrator\Desktop\`,
   the Win daemon puts it on the OS clipboard via the trigger file, the
   Linux daemon detects the new CF_HDROP, sends the file over P2P, and
   writes it to `~/.config/cross-clipboard/incoming/<sha8>/*.bin`. The
   test waits for `received file: <name>` in `linux-live.log` and then
   SHA-compares the received file against the source.
2. L2W: a 1 KiB random file is set on the Linux clipboard via
   `x11_fileclip_helper.py`. The Linux daemon detects the URI, sends
   the file over P2P, the Win daemon receives it and writes it to
   `%USERPROFILE%\.config\cross-clipboard\incoming\<sha8>\*.bin`. The
   test waits for `received file: <name>` in the Win log and then
   SHA-compares via `Get-FileHash` over SSH.

Result JSON is written to `/tmp/cross-clipboard-4002/e2e-final.json`
when both directions pass.
