package clipboardfile

import (
	"sort"
	"time"
)

// FileClipboard abstracts OS-level file clipboard operations (copy/paste of
// files, not text/images). The core clipboard manager handles only text/image;
// file transfer needs its own OS integration. Implementations live behind
// build tags (linux.go, windows.go).
type FileClipboard interface {
	// Available reports whether the OS toolchain for the file clipboard is
	// present (xclip on Linux, powershell+WinForms on Windows).
	Available() bool
	// Watch polls the OS clipboard for file-copy events, emitting the copied
	// absolute file paths. It stops when ctx is cancelled.
	Watch(ctx interface{ Done() <-chan struct{} }) <-chan []string
	// SetFiles places the given file paths on the OS clipboard as a file drop
	// list, so a subsequent paste inserts the file.
	SetFiles(paths []string) error
	// Paste simulates Ctrl+V in the currently focused window.
	Paste() error
}

// PollingInterval is how often the watcher polls the OS clipboard.
const PollingInterval = 500 * time.Millisecond

// samePaths reports whether two path slices contain the same elements
// regardless of order. Used by watchers to suppress no-change emissions.
func samePaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ax := append([]string(nil), a...)
	bx := append([]string(nil), b...)
	sort.Strings(ax)
	sort.Strings(bx)
	for i := range ax {
		if ax[i] != bx[i] {
			return false
		}
	}
	return true
}
