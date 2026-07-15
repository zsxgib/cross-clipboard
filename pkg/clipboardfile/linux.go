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

// readURIs reads the clipboard's text/uri-list target and returns the file paths.
func (l *linuxFileClipboard) readURIs() []string {
	out, err := exec.Command("xclip", "-o", "-selection", "clipboard", "-t", "text/uri-list").Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			continue
		}
		if u.Scheme != "file" {
			continue
		}
		p, err := url.PathUnescape(u.Path)
		if err != nil {
			p = u.Path
		}
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
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

// SetFiles writes the paths as a file:// URI list onto the clipboard.
func (l *linuxFileClipboard) SetFiles(paths []string) error {
	var b strings.Builder
	for _, p := range paths {
		u := &url.URL{Scheme: "file", Path: p}
		b.WriteString(u.String())
		b.WriteString("\n")
	}
	cmd := exec.Command("xclip", "-i", "-selection", "clipboard", "-t", "text/uri-list")
	cmd.Stdin = strings.NewReader(b.String())
	return cmd.Run()
}

func (l *linuxFileClipboard) Paste() error {
	return exec.Command("xdotool", "key", "ctrl+v").Run()
}
