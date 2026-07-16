//go:build linux

package clipboardfile

import (
	"net/url"
	"os/exec"
	"strings"
	"time"
)

type linuxFileClipboard struct{}

// New returns a FileClipboard for the current OS.
func New() FileClipboard {
	return &linuxFileClipboard{}
}

func (l *linuxFileClipboard) Available() bool {
	_, err := exec.LookPath("xclip")
	return err == nil
}

// readURIs reads file paths from the clipboard.
// It tries x-special/gnome-copied-files first (Nautilus/GNOME format),
// then falls back to text/uri-list.
func (l *linuxFileClipboard) readURIs() []string {
	// Try x-special/gnome-copied-files first (Nautilus, Nemo, etc.)
	out, err := exec.Command("xclip", "-o", "-selection", "clipboard", "-t", "x-special/gnome-copied-files").Output()
	if err == nil && len(out) > 0 {
		if paths := parseCopiedFiles(string(out)); len(paths) > 0 {
			return paths
		}
	}
	// Fall back to text/uri-list
	out, err = exec.Command("xclip", "-o", "-selection", "clipboard", "-t", "text/uri-list").Output()
	if err != nil {
		return nil
	}
	return parseURIList(string(out))
}

// parseCopiedFiles parses x-special/gnome-copied-files format:
// "copy\nfile:///path1\nfile:///path2\n" or "cut\n..."
func parseCopiedFiles(data string) []string {
	lines := strings.Split(data, "\n")
	var paths []string
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// First non-empty line is "copy" or "cut"
		if i == 0 && (line == "copy" || line == "cut") {
			continue
		}
		p := parseFileURI(line)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// parseURIList parses text/uri-list format: "file:///path1\nfile:///path2\n"
func parseURIList(data string) []string {
	var paths []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := parseFileURI(line)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// parseFileURI extracts the local path from a file:// URI.
func parseFileURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	if u.Scheme != "file" {
		return ""
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		p = u.Path
	}
	return p
}

func (l *linuxFileClipboard) Watch(ctx interface{ Done() <-chan struct{} }) <-chan []string {
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
				paths := l.readURIs()
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

// SetFiles writes the paths onto the clipboard using x-special/gnome-copied-files
// format so that Nautilus and other GNOME file managers can paste them.
func (l *linuxFileClipboard) SetFiles(paths []string) error {
	var b strings.Builder
	b.WriteString("copy\n")
	for _, p := range paths {
		u := &url.URL{Scheme: "file", Path: p}
		b.WriteString(u.String())
		b.WriteString("\n")
	}
	cmd := exec.Command("xclip", "-i", "-selection", "clipboard", "-t", "x-special/gnome-copied-files")
	cmd.Stdin = strings.NewReader(b.String())
	return cmd.Run()
}

func (l *linuxFileClipboard) Paste() error {
	return exec.Command("xdotool", "key", "ctrl+v").Run()
}
