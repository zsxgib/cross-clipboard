//go:build windows

package clipboardfile

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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

// Watch polls GetFileDropList via PowerShell.
func (w *windowsFileClipboard) Watch(ctx context.Context) <-chan []string {
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
				paths, err := w.readFileDropList()
				if err != nil {
					continue
				}
				if !samePathSet(last, paths) {
					last = paths
					select {
					case <-ctx.Done():
						return
					case out <- paths:
					}
				}
			}
		}
	}()
	return out
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
