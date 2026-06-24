//go:build linux

package clipboardfile

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// linuxFileClipboard implements FileClipboard using xclip + xdotool.
type linuxFileClipboard struct{}

// New returns a FileClipboard for the current OS.
func New() FileClipboard {
	return &linuxFileClipboard{}
}

func (l *linuxFileClipboard) Available() bool {
	_, errXclip := exec.LookPath("xclip")
	_, errXdotool := exec.LookPath("xdotool")
	return errXclip == nil && errXdotool == nil
}

// Watch polls xclip every PollingInterval. It only emits when the set of
// file URIs actually changes. URI list is split on newlines; `file://` and
// bare paths are both accepted.
func (l *linuxFileClipboard) Watch(ctx context.Context) <-chan []string {
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

// Set writes file:// URIs to the system clipboard using xclip.
//
// Populates three MIME types so GNOME / KDE / X11 file managers and other
// apps recognize the clipboard as a "copied file" and accept Ctrl+V as a
// file paste:
//
//   * x-special/gnome-copied-files -- Nautilus / GNOME Files primary signal
//   * text/uri-list                 -- POSIX standard fallback (Dolphin,
//                                      PCManFM, Firefox, Chrome, etc.)
//   * text/plain                    -- safety net for apps that only
//                                      request STRING
//
// Without x-special/gnome-copied-files, Nautilus ignores the clipboard
// when Ctrl+V is pressed and the file does not get pasted.
func (l *linuxFileClipboard) Set(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// User-requested: drop the file:// scheme prefix.
	// text/uri-list and text/plain now carry the bare absolute path
	// (one per line).
	var uriList bytes.Buffer
	for _, p := range paths {
		uriList.WriteString(p)
		uriList.WriteByte('\n')
	}
	plain := uriList.String()

	// Call xclip once per target. A single xclip invocation with
	// multiple -t flags does NOT populate each target with the full
	// stdin; it slices stdin bytes sequentially across targets in the
	// order given, so the same content goes into one target and the
	// wrong slice into the others. Three separate calls guarantee
	// each target receives the full plain payload.
	// Set the three MIME targets one at a time, but keep the *last*
	// xclip process alive in the background so it holds the clipboard
	// selection. Each xclip invocation with -loops 0 sets the target
	// and then exits, which transfers selection ownership to the next
	// xclip call. By keeping the last xclip alive with -loops we
	// guarantee the receiving app sees all three targets when it
	// converts the selection.
	//
	// If a previous Set is still holding the clipboard, kill it first
	// so the new xclip can take ownership cleanly.
	if prev := prevXclipPID(); prev > 0 {
		_ = syscall.Kill(prev, syscall.SIGTERM)
	}
	targets := []string{
		"x-special/gnome-copied-files",
		"text/uri-list",
		"text/plain",
	}
	for i, t := range targets {
		loops := "0"
		if i == len(targets)-1 {
			// Final target: stay alive to hold the selection
			loops = "100"
		}
		// All three targets (x-special/gnome-copied-files,
		// text/uri-list, text/plain) carry the bare absolute path
		// (no file:// prefix) per user direction.
		payload := plain
		cmd := exec.Command("xclip", "-selection", "clipboard", "-loops", loops, "-t", t)
		cmd.Stdin = strings.NewReader(payload)
		var err error
		if i == len(targets)-1 {
			err = cmd.Start()
			if err == nil {
				setPrevXclipPID(cmd.Process.Pid)
				go func() { _ = cmd.Wait() }()
			}
		} else {
			err = cmd.Run()
		}
		if err != nil {
			return fmt.Errorf("xclip -t %s: %w", t, err)
		}
	}
	return nil
}

// Paste is a no-op on Linux because the XTest / xdotool synthetic
// Ctrl+V path is unreliable on gdm + gnome-shell sessions (the X
// server refuses to deliver XTest key events to the active window).
// The clipboard has already been populated with the right MIME
// targets by Set(), so the user can press Ctrl+V themselves in the
// focused application. main.go logs a hint when Paste returns nil
// after Set, telling the user "press Ctrl+V to paste".
func (l *linuxFileClipboard) Paste() error {
	time.Sleep(200 * time.Millisecond)
	// Best-effort: try xdotool. It works on non-gdm sessions and
	// when the user has not blocked XTest. On gdm it is silently
	// dropped, which is fine because the user can press Ctrl+V
	// manually; main.go's hint log makes that explicit.
	return exec.Command("xdotool", "key", "--clearmodifiers", "ctrl+v").Run()
}


func (l *linuxFileClipboard) readURIList() ([]string, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-o", "-t", "text/uri-list")
	out, err := cmd.Output()
	if err != nil {
		// xclip returns exit 1 when the format is not present, which is fine.
		return nil, err
	}
	return parseURIList(string(out)), nil
}

// --- background xclip PID tracking ------------------------------------
//
// When Set() is called repeatedly, only the most recent xclip should
// remain holding the clipboard selection. We track the last xclip
// process PID in a package-level variable and SIGTERM it on the next
// Set, so two stale xclip processes don't fight for ownership.

var (
	xclipMu  sync.Mutex
	xclipPID int
)

func setPrevXclipPID(pid int) {
	xclipMu.Lock()
	xclipPID = pid
	xclipMu.Unlock()
}

func prevXclipPID() int {
	xclipMu.Lock()
	defer xclipMu.Unlock()
	return xclipPID
}

// parseURIList accepts `file://` URIs only. Lines that do not start with
// `file://` are ignored; this prevents the watcher from interpreting
// arbitrary clipboard text as a file path when the user just copies
// text or an image. The xclip invocation can return 0 bytes or even the
// plain text payload if the system clipboard does not have a uri-list,
// so we must be strict here.
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
		// Percent-decode the path so UTF-8 names, spaces, and other
		// reserved characters come through as the user-typed filesystem
		// path. File managers like Nautilus URL-encode the path before
		// putting it on the clipboard, so we must decode before stat.
		if decoded, derr := url.PathUnescape(rest); derr == nil {
			rest = decoded
		}
		out = append(out, rest)
	}
	return out
}

