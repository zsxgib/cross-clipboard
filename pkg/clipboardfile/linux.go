//go:build linux

package clipboardfile

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
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
func (l *linuxFileClipboard) Set(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&buf, "file://%s\n", p)
	}
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "text/uri-list")
	cmd.Stdin = &buf
	return cmd.Run()
}

// Paste simulates Ctrl+V with xdotool.
func (l *linuxFileClipboard) Paste() error {
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

// parseURIList accepts both `file://` URIs and bare paths. Empty lines are
// skipped. Whitespace is trimmed.
func parseURIList(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "file://"); ok {
			line = rest
		}
		out = append(out, line)
	}
	return out
}

