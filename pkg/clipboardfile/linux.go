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
	var uriList bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&uriList, "file://%s\n", p)
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
		cmd := exec.Command("xclip", "-selection", "clipboard", "-loops", loops, "-t", t)
		cmd.Stdin = strings.NewReader(plain)
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

// Paste simulates Ctrl+V with xdotool. A small delay between the
// preceding Set() and the keystroke gives the X11 clipboard manager
// (xclip, xfce4-clipman, etc.) time to commit the new selection so the
// receiving app reads the just-copied file URIs, not the previous
// clipboard contents.
func (l *linuxFileClipboard) Paste() error {
	time.Sleep(200 * time.Millisecond)
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

