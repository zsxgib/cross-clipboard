// Package clipboardfile watches and writes file URIs on the OS clipboard.
//
// On Linux it polls `xclip -selection clipboard -o -t text/uri-list` so the
// user's file-manager "Copy" action surfaces here. On Windows it polls
// PowerShell `[Windows.Forms.Clipboard]::GetFileDropList()`. Writes go back
// the other way, and `Paste` simulates Ctrl+V via xdotool / SendInput.
package clipboardfile

import (
	"context"
	"time"
)

// FileClipboard abstracts the OS-level file URI clipboard operations.
type FileClipboard interface {
	// Watch returns a channel that emits the absolute file paths currently on
	// the system clipboard. It deduplicates emissions and only fires when the
	// set of file paths actually changes. The channel is closed when ctx is
	// done.
	Watch(ctx context.Context) <-chan []string
	// Set replaces the OS clipboard content with the given absolute file
	// paths so a subsequent Paste lands the files in the focused window.
	Set(paths []string) error
	// Paste simulates a Ctrl+V keystroke so the focused app consumes the
	// clipboard content set by Set.
	Paste() error
	// Available reports whether this implementation can run on the current OS.
	Available() bool
}

// PollingInterval is the default Watch poll interval.
var PollingInterval = 500 * time.Millisecond

// samePathSet reports whether the two file path slices contain the same
// elements regardless of order. Used by both the Linux and Windows
// implementations of Watch to skip duplicate emissions.
func samePathSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]struct{}, len(a))
	for _, p := range a {
		m[p] = struct{}{}
	}
	for _, p := range b {
		if _, ok := m[p]; !ok {
			return false
		}
	}
	return true
}
