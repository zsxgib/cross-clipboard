//go:build windows

package clipboardfile

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
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
// Two implementations are tried in order by readFileDropListDirect():
//   1. Native Win32 via golang.org/x/sys/windows (preferred - no subprocess)
//   2. PowerShell P/Invoke fallback (used only if native fails)
//
// Why this matters: PowerShell [Add-Type] + repeated process start was hitting
// 0xc0000005 access violations under sustained load (every 500ms poll) because
// the new powershell.exe process crashed before Add-Type could complete. Native
// calls are stable, fast, and free of process startup overhead.
func (w *windowsFileClipboard) Watch(ctx context.Context) <-chan []string {
	out := make(chan []string, 4)
	go func() {
		defer close(out)
		var last []string
		var lastSeq uint32
		log.Printf("file watcher started: polling OS clipboard every %v (CF_HDROP via user32)", PollingInterval)
		t := time.NewTicker(PollingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				paths, seq, err := w.readFileDropListDirectNative()
				if err != nil {
					log.Printf("runtime error: readFileDropListDirectNative: %v", err)
					continue
				}
				log.Printf("watcher tick: seq=%d CF_HDROP=%d", seq, len(paths))
				// Skip work if clipboard sequence number is unchanged
				// AND the last emitted set equals the current set.
				if seq == lastSeq && samePathSet(last, paths) {
					continue
				}
				lastSeq = seq
				if samePathSet(last, paths) {
					continue
				}
				log.Printf("watcher emit: %d paths %v", len(paths), paths)
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


// readFileDropListDirectNative reads CF_HDROP directly from the Windows clipboard
// using golang.org/x/sys/windows syscalls. Returns the file paths plus the
// current clipboard sequence number. This avoids spawning a powershell.exe
// process per poll (which was crashing with 0xc0000005 under load).
func (w *windowsFileClipboard) readFileDropListDirectNative() (paths []string, seq uint32, err error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	shell32 := windows.NewLazySystemDLL("shell32.dll")

	procOpenClipboard := user32.NewProc("OpenClipboard")
	procCloseClipboard := user32.NewProc("CloseClipboard")
	procGetClipboardData := user32.NewProc("GetClipboardData")
	procGetSeqNum := user32.NewProc("GetClipboardSequenceNumber")
	procGlobalLock := kernel32.NewProc("GlobalLock")
	procGlobalUnlock := kernel32.NewProc("GlobalUnlock")
	procDragQueryFileW := shell32.NewProc("DragQueryFileW")

	// 1) Get sequence number first - always works, no clipboard lock needed
	r1, _, _ := procGetSeqNum.Call()
	seq = uint32(r1)

	// 2) Try to open clipboard (may fail if another process holds it)
	r2, _, _ := procOpenClipboard.Call(0)
	if r2 == 0 {
		// Clipboard busy; return empty list but valid seq so caller knows it changed
		return nil, seq, nil
	}
	defer procCloseClipboard.Call()

	// 3) Get CF_HDROP (format 15)
	hDrop, _, _ := procGetClipboardData.Call(15)
	if hDrop == 0 {
		// Try CF_UNICODETEXT to know if clipboard is empty or just no file
		hText, _, _ := procGetClipboardData.Call(13) // CF_UNICODETEXT
		if hText != 0 {
			// Read a few bytes for debug
			pPtr, _, _ := procGlobalLock.Call(hText)
			if pPtr != 0 {
				defer procGlobalUnlock.Call(hText)
				// Read first 64 wide chars
				wide := (*[64]uint16)(unsafe.Pointer(pPtr))
				n := 0
				for n < 64 && wide[n] != 0 {
					n++
				}
				}
		}
		return nil, seq, nil
	}

	// 4) Lock the global memory
	ptr, _, _ := procGlobalLock.Call(hDrop)
	if ptr == 0 {
		return nil, seq, nil
	}
	defer procGlobalUnlock.Call(hDrop)

	// 5) DragQueryFileW: first call with nil filename returns the count
	const dragQueryFileWNoPath = 0xFFFFFFFF
	rCount, _, _ := procDragQueryFileW.Call(ptr, dragQueryFileWNoPath, 0, 0)
	count := uint32(rCount)
	if count == 0 || count > 1024 {
		return nil, seq, nil
	}

	// 6) Allocate buffer and query each file
	buf := make([]uint16, 1024)
	for i := uint32(0); i < count; i++ {
		// bufLen includes terminating null; per docs returns copied chars (not including null)
		rLen, _, _ := procDragQueryFileW.Call(ptr, uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		n := uint32(rLen)
		if n == 0 || n > uint32(len(buf)) {
			continue
		}
		// DragQueryFileW null-terminates; trim
		if n < uint32(len(buf)) {
			paths = append(paths, windows.UTF16ToString(buf[:n]))
		} else {
			paths = append(paths, windows.UTF16ToString(buf))
		}
	}
	return paths, seq, nil
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
try {
  if (-not ('Clip' -as [type])) {
    Add-Type -TypeDefinition $src -Language CSharp -ErrorAction Stop
  }
} catch {
  # If Add-Type still fails (corrupt session), skip this poll rather than crash
  Write-Host "__ERR__addtype"
  exit 0
}
$seq = 0
$paths = @()
$attempt = 0
while ($attempt -lt 3) {
  $attempt++
  try {
    $seq = [Clip]::GetClipboardSequenceNumber()
    $paths = [Clip]::GetHDrop()
    break
  } catch {
    Start-Sleep -Milliseconds 50
  }
}
Write-Host "__SEQ__$seq"
foreach ($p in $paths) { Write-Host "__P__$p" }
`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", ps)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("powershell read CF_HDROP: %w", err)
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

// Set writes a FileDrop list (CF_HDROP) directly to the Windows clipboard
// using native Win32 APIs. We deliberately avoid the PowerShell path here
// because the previous PowerShell SetFileDropList call could hang for tens
// of seconds in the test harness (SSH-launched PowerShell starts in
// session 0, then the SetFileDropList call routed through the .NET
// Clipboard class deadlocked against our own watcher process). Native
// OpenClipboard + SetClipboardData with a DROPFILES struct is the same
// path Explorer uses, works in every session, and finishes in <1ms.
func (w *windowsFileClipboard) Set(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// Build the DROPFILES payload: a header followed by a double-null-
	// terminated wide-char list of paths.
	//   struct DROPFILES { DWORD pFiles; POINT pt; DWORD fNC; DWORD fWide; }
	const sizeOfDrop = 20 // 5 * sizeof(DWORD)
	var strBytes []byte
	for _, p := range paths {
		u16 := windows.StringToUTF16(p)
		// Append each uint16 as 2 little-endian bytes
		for _, c := range u16 {
			strBytes = append(strBytes, byte(c), byte(c>>8))
		}
		// null terminator
		strBytes = append(strBytes, 0, 0)
	}
	// Final double null terminator (empty file name) for the list
	strBytes = append(strBytes, 0, 0)
	total := sizeOfDrop + len(strBytes)

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")
	procGlobalAlloc := kernel32.NewProc("GlobalAlloc")
	procGlobalLock := kernel32.NewProc("GlobalLock")
	procGlobalUnlock := kernel32.NewProc("GlobalUnlock")
	procOpenClipboard := user32.NewProc("OpenClipboard")
	procCloseClipboard := user32.NewProc("CloseClipboard")
	procSetClipboardData := user32.NewProc("SetClipboardData")
	procEmptyClipboard := user32.NewProc("EmptyClipboard")

	// GMEM_MOVEABLE = 0x0002, GMEM_ZEROINIT = 0x0040
	hMem, _, _ := procGlobalAlloc.Call(0x0042, uintptr(total))
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock failed")
	}
	// pFiles = sizeOfDrop (offset to file list)
	*(*uint32)(unsafe.Pointer(ptr)) = uint32(sizeOfDrop)
	// pt.x, pt.y, fNC = 0
	*(*uint32)(unsafe.Pointer(ptr + 4)) = 0
	*(*uint32)(unsafe.Pointer(ptr + 8)) = 0
	*(*uint32)(unsafe.Pointer(ptr + 12)) = 0
	// fWide = 1 (paths are wide chars)
	*(*uint32)(unsafe.Pointer(ptr + 16)) = 1
	// Copy the file list
	dst := (*[1 << 30]byte)(unsafe.Pointer(ptr + sizeOfDrop))[:len(strBytes):len(strBytes)]
	copy(dst, strBytes)
	procGlobalUnlock.Call(hMem)

	// Open, empty, set, close
	r1, _, _ := procOpenClipboard.Call(0)
	if r1 == 0 {
		return fmt.Errorf("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	// CF_HDROP = 15
	hRet, _, _ := procSetClipboardData.Call(15, hMem)
	if hRet == 0 {
		return fmt.Errorf("SetClipboardData failed")
	}
	log.Printf("set file clipboard: %d paths", len(paths))
	return nil
}

// Paste simulates Ctrl+V by sending the keystroke to the current
// foreground window. We deliberately do NOT call AppActivate: the
// previous implementation activated the PowerShell host's own
// main-window handle, which stole focus from the user's actual
// application and made Ctrl+V land in PowerShell instead of the
// recipient (chat client, Explorer, Office, ...). WScript.Shell.SendKeys
// already targets whatever window currently has focus, so the only
// thing left to do is wait briefly for the OS clipboard write to settle
// before the recipient app asks for it.
func (w *windowsFileClipboard) Paste() error {
	ps := `(Get-Process -Id $PID) | Out-Null; Start-Sleep -Milliseconds 200; (New-Object -ComObject WScript.Shell).SendKeys('^v')`
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
