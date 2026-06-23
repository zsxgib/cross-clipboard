//go:build windows

package clipboardfile

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// windowsFileClipboard implements FileClipboard using PowerShell against
// System.Windows.Forms.Clipboard. Set writes a FileDrop list; Paste uses
// SendInput via Add-Type.
type windowsFileClipboard struct{}

// New returns a FileClipboard for the current OS.
func New() FileClipboard {
	return &windowsFileClipboard{}
}

func (w *windowsFileClipboard) Available() bool {
	_, err := exec.LookPath("powershell.exe")
	return err == nil
}

// Watch polls the OS clipboard for a FileDrop list.
//
// The previous implementation used PowerShell [Windows.Forms.Clipboard]::GetFileDropList(),
// which has a well-known problem: when the OS clipboard currently holds text (not a file
// list), GetFileDropList() can return the *stale* FileDrop list from a previous copy
// instead of empty. That caused the watcher to repeatedly re-emit a path the user no
// longer had on the clipboard, leading to a flood of "stat source: file not found"
// errors when the user copied a new file (or text).
//
// The fix uses the Win32 clipboard sequence number + a low-level CF_HDROP read via
// P/Invoke. The sequence number increments on every clipboard change, so we only
// re-read when the clipboard actually changes, and we trust the CF_HDROP payload
// directly without any Windows.Forms fallback that could leak stale state.
func (w *windowsFileClipboard) Watch(ctx context.Context) <-chan []string {
	out := make(chan []string, 4)
	go func() {
		defer close(out)
		var last []string
		var lastSeq uint32
		t := time.NewTicker(PollingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				paths, seq, err := w.readFileDropListDirect()
				if err != nil {
					continue
				}
				// Skip work if clipboard sequence number is unchanged
				// AND the last emitted set equals the current set.
				if seq == lastSeq && samePathSet(last, paths) {
					continue
				}
				lastSeq = seq
				if samePathSet(last, paths) {
					continue
				}
				last = paths
				select {
				case <-ctx.Done():
					return
				case out <- paths:
				}
			}
		}
	}()
	return out
}

// readFileDropListDirect reads the OS clipboard's CF_HDROP via Win32 P/Invoke and
// returns the file paths plus the current clipboard sequence number. The sequence
// number is monotonic per-session; it increases on every successful SetClipboardData
// or OLE-set operation, so it is a reliable change indicator.
func (w *windowsFileClipboard) readFileDropListDirect() (paths []string, seq uint32, err error) {
	ps := `
$ErrorActionPreference = 'Stop'
$src = @"
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public class Clip {
  [DllImport("user32.dll")] public static extern bool OpenClipboard(IntPtr hWndNewOwner);
  [DllImport("user32.dll")] public static extern bool CloseClipboard();
  [DllImport("user32.dll")] public static extern IntPtr GetClipboardData(uint uFormat);
  [DllImport("user32.dll")] public static extern uint GetClipboardSequenceNumber();
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr GlobalLock(IntPtr h);
  [DllImport("kernel32.dll")] public static extern bool GlobalUnlock(IntPtr h);
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
  public struct DROPFILES { public int pFiles; public int pt_x; public int pt_y; public int fNC; public int fWide; }
  public static List<string> GetHDrop() {
    var list = new List<string>();
    if (!OpenClipboard(IntPtr.Zero)) return list;
    try {
      IntPtr h = GetClipboardData(15);
      if (h == IntPtr.Zero) return list;
      IntPtr p = GlobalLock(h);
      if (p == IntPtr.Zero) return list;
      try {
        var df = (DROPFILES)Marshal.PtrToStructure(p, typeof(DROPFILES));
        if (df.fWide != 0) {
          IntPtr s = IntPtr.Add(p, df.pFiles);
          while (true) {
            string part = Marshal.PtrToStringUni(s);
            if (part == null) break;
            if (part.Length > 0) list.Add(part);
            s = IntPtr.Add(s, (part.Length + 1) * 2);
          }
        }
      } finally { GlobalUnlock(h); }
    } finally { CloseClipboard(); }
    return list;
  }
}
"@
Add-Type -TypeDefinition $src -Language CSharp
$seq = [Clip]::GetClipboardSequenceNumber()
$paths = [Clip]::GetHDrop()
Write-Host "__SEQ__$seq"
foreach ($p in $paths) { Write-Host "__P__$p" }
`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", ps)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, 0, err
	}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "__SEQ__") {
			if n, perr := strconv.ParseUint(strings.TrimPrefix(line, "__SEQ__"), 10, 32); perr == nil {
				seq = uint32(n)
			}
		} else if strings.HasPrefix(line, "__P__") {
			if p := strings.TrimPrefix(line, "__P__"); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths, seq, nil
}

// Set uses PowerShell to write a FileDrop list with the given paths.
func (w *windowsFileClipboard) Set(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// Build a PowerShell script that creates a StringCollection and assigns
	// it to the Clipboard. -STA is required for Clipboard access.
	var sb strings.Builder
	sb.WriteString("Add-Type -AssemblyName System.Windows.Forms;")
	sb.WriteString("$paths = @(")
	for i, p := range paths {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, "'%s'", strings.ReplaceAll(p, "'", "''"))
	}
	sb.WriteString(");")
	sb.WriteString("$col = New-Object System.Collections.Specialized.StringCollection;")
	sb.WriteString("$col.AddRange($paths);")
	sb.WriteString("[System.Windows.Forms.Clipboard]::SetFileDropList($col)")
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", sb.String())
	return cmd.Run()
}

// Paste simulates Ctrl+V via SendInput.
func (w *windowsFileClipboard) Paste() error {
	// Use SendWait so the keystroke is delivered to the focused window
	// synchronously, then clear modifiers.
	ps := `$ws = New-Object -ComObject WScript.Shell; $ws.AppActivate((Get-Process -Id $PID).MainWindowHandle) | Out-Null; $ws.SendKeys('^v')`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", ps)
	return cmd.Run()
}

func (w *windowsFileClipboard) readFileDropList() ([]string, error) {
	ps := `Add-Type -AssemblyName System.Windows.Forms; ($files = [System.Windows.Forms.Clipboard]::GetFileDropList()) 2>$null ; if ($files) { $files | ForEach-Object { $_.FullName } }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", ps)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths, nil
}
