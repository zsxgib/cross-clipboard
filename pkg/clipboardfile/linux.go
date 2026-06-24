//go:build linux

package clipboardfile

import (
	"bytes"
	"encoding/json"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"log"
	"strings"
	"sync"
	"syscall"
	"time"
)

// linuxFileClipboard implements FileClipboard using a long-lived PyQt5
// helper process as the X11 selection owner. The helper (see
// scripts/x11_fileclip_helper.py) creates a single QMimeData with all
// MIME targets populated and hands it to QClipboard::setMimeData. Because
// one process owns the selection, every target (x-special/gnome-copied-files,
// text/uri-list, text/plain) survives intact until a new Set replaces the
// payload. This fixes the race where 3 sequential xclip calls left only
// the last -t's content visible (only the last xclip owned the selection,
// earlier targets were empty).
type linuxFileClipboard struct{}

// New returns a FileClipboard for the current OS.
func New() FileClipboard {
	return &linuxFileClipboard{}
}

func (l *linuxFileClipboard) Available() bool {
	if _, err := os.Stat("/tmp/x11_fileclip_helper.py"); err != nil {
		return false
	}
	if _, err := exec.LookPath("xclip"); err != nil {
		return false
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		return false
	}
	return true
}

// Watch polls xclip every PollingInterval. It only emits when the set of
// file URIs actually changes. URI list is split on newlines; `file://` and
// bare paths are both accepted.
func (l *linuxFileClipboard) Watch(ctx context.Context) <-chan []string {
	out := make(chan []string, 4)
	go func() {
		defer close(out)
		var last []string
		log.Printf("file watcher started: polling xclip every %v (text/uri-list + gnome-copied-files)", PollingInterval)
		t := time.NewTicker(PollingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				paths, err := l.readURIList()
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

// Set writes file URIs to the system clipboard using a long-lived PyQt5
// helper as the selection owner. The helper holds one selection and
// serves three MIME targets from a single QMimeData:
//
//   - x-special/gnome-copied-files  : "copy\nfile:///abs/path\n" (Nautilus)
//   - text/uri-list                 : "file:///abs/path\n" (Dolphin, etc.)
//   - text/plain                    : "/abs/path\n" (apps that only read STRING)
//
// Without the gnome target, Nautilus ignores the clipboard on Ctrl+V.
// The helper stays alive (sleep loop) until killed, so we keep its PID
// and SIGTERM the previous one before launching a new helper.
func (l *linuxFileClipboard) Set(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if prev := prevHelperPID(); prev > 0 {
		_ = syscall.Kill(prev, syscall.SIGTERM)
		setPrevHelperPID(0)
	}
	// Remove any stale ok file from a previous run.
	_ = os.Remove("/tmp/x11_fileclip_helper.ok")
	// Launch helper fully detached so it survives our process exit
	// (we are the parent of xclip-equivalent, and once the user logs
	// out / we get killed, the helper must keep holding the X11
	// selection). setsid puts it in a new session; stdin/stdout/stderr
	// closed so nothing in the parent can reach it; pid written to
	// /tmp/x11_fileclip_helper.pid by a tiny shim so we can track it.
	args := []string{"setsid", "python3", "/tmp/x11_fileclip_helper.py"}
	args = append(args, paths...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	// setsid returns immediately after the child forks. Wait for the
	// helper to write /tmp/x11_fileclip_helper.ok with {"ok": true}.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile("/tmp/x11_fileclip_helper.ok"); err == nil {
			if bytes.Contains(data, []byte(`"ok": true`)) {
				// Best-effort: read the helper's own pid from the ok
				// file. If parsing fails, fall back to the setsid
				// wrapper pid; either is good enough for the SIGTERM
				// on the next Set.
				var ok struct {
					OK  bool   `json:"ok"`
					PID int    `json:"pid"`
				}
				if json.Unmarshal(data, &ok) == nil && ok.PID > 0 {
					setPrevHelperPID(ok.PID)
				} else {
					setPrevHelperPID(cmd.Process.Pid)
				}
				return nil
			}
			if bytes.Contains(data, []byte(`"ok": false`)) {
				return fmt.Errorf("helper reported error: %s", string(data))
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return fmt.Errorf("helper did not confirm clipboard within 3s")
}

// Paste is a no-op on Linux because the XTest / xdotool synthetic
// Ctrl+V path is unreliable on gdm + gnome-shell sessions (the X
// server refuses to deliver XTest key events to the active window).
// The clipboard has already been populated with the right MIME
// targets by Set(), so the user can press Ctrl+V themselves in the
// focused application.
func (l *linuxFileClipboard) Paste() error {
	time.Sleep(200 * time.Millisecond)
	// Best-effort: try xdotool. Works on non-gdm sessions. On gdm it
	// is silently dropped, which is fine because the user can press
	// Ctrl+V manually; main.go's hint log makes that explicit.
	return exec.Command("xdotool", "key", "--clearmodifiers", "ctrl+v").Run()
}

func (l *linuxFileClipboard) readURIList() ([]string, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-o", "-t", "text/uri-list")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseURIList(string(out)), nil
}

// --- background helper PID tracking ------------------------------------
//
// Set() spawns one helper per call. The previous helper is SIGTERMed on
// the next Set so two stale helpers don't fight for selection ownership.

var (
	helperMu  sync.Mutex
	helperPID int
)

func setPrevHelperPID(pid int) {
	helperMu.Lock()
	helperPID = pid
	helperMu.Unlock()
}

func prevHelperPID() int {
	helperMu.Lock()
	defer helperMu.Unlock()
	return helperPID
}

// parseURIList accepts `file://` URIs only. Lines that do not start with
// `file://` are ignored; this prevents the watcher from interpreting
// arbitrary clipboard text as a file path when the user just copies
// text or an image.
func parseURIList(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rest, ok := strings.CutPrefix(line, "file://")
		if !ok {
			continue
		}
		if rest == "" {
			continue
		}
		if decoded, derr := url.PathUnescape(rest); derr == nil {
			rest = decoded
		}
		out = append(out, rest)
	}
	return out
}
