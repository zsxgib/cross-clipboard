//go:build windows

package clipboardfile

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type windowsFileClipboard struct{}

// New returns a FileClipboard for the current OS.
func New() FileClipboard {
	return &windowsFileClipboard{}
}

func (w *windowsFileClipboard) Available() bool {
	_, err := exec.LookPath("powershell.exe")
	return err == nil
}

// ps runs a PowerShell snippet in STA mode (required by System.Windows.Forms
// clipboard APIs). Windows PowerShell 5.1 (powershell.exe) is used.
func (w *windowsFileClipboard) ps(script string) (string, error) {
	out, err := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script).Output()
	return string(out), err
}

func (w *windowsFileClipboard) readDropList() []string {
	out, err := w.ps("Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Clipboard]::GetFileDropList()")
	if err != nil {
		return nil
	}
	var paths []string
	for _, p := range strings.Split(out, "\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func (w *windowsFileClipboard) Watch(ctx interface{ Done() <-chan struct{} }) <-chan []string {
	out := make(chan []string, 4)
	go func() {
		defer close(out)
		var last []string
		t := time.NewTicker(PollingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				paths := w.readDropList()
				if len(paths) == 0 {
					last = nil
					continue
				}
				if samePaths(last, paths) {
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

// SetFiles places the paths on the clipboard as a FileDropList (CF_HDROP).
func (w *windowsFileClipboard) SetFiles(paths []string) error {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "'" + strings.ReplaceAll(p, "'", "''") + "'"
	}
	script := fmt.Sprintf(
		`Add-Type -AssemblyName System.Windows.Forms; $fl = New-Object System.Collections.Specialized.StringCollection; $fl.AddRange(@(%s)); [System.Windows.Forms.Clipboard]::SetFileDropList($fl)`,
		strings.Join(quoted, ","),
	)
	_, err := w.ps(script)
	return err
}

// Paste simulates Ctrl+V via SendKeys.
func (w *windowsFileClipboard) Paste() error {
	_, err := w.ps("Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('^v')")
	return err
}
