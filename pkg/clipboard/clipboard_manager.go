package clipboard

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/xclipboard"
)

// ClipboardManager struct for clipbaord manager
type ClipboardManager struct {
	config *config.Config

	ReadTextChannel          <-chan []byte
	ReadImageChannel         <-chan []byte
	ClipboardsHistory        []*Clipboard
	ClipboardsHistoryUpdated chan struct{}
	receivedClipboard        *Clipboard
	fileClipboardActive      atomic.Bool
}

// NewClipboardManager create new clipbaord manager
func NewClipboardManager(cfg *config.Config) *ClipboardManager {
	err := xclipboard.Init()
	if err != nil {
		panic(err)
	}

	textCh := xclipboard.Watch(context.Background(), xclipboard.FmtText)
	imgCh := xclipboard.Watch(context.Background(), xclipboard.FmtImage)

	return &ClipboardManager{
		config:                   cfg,
		ReadTextChannel:          textCh,
		ReadImageChannel:         imgCh,
		ClipboardsHistoryUpdated: make(chan struct{}),
		ClipboardsHistory:        []*Clipboard{},
	}
}

// limitAppend append and rotate when limit
func limitAppend[T any](limit int, slice []T, new T) []T {
	l := len(slice)
	if l >= limit {
		slice = slice[1:]
	}
	slice = append(slice, new)
	return slice
}

// WriteClipboard write os clipbaord
func (c *ClipboardManager) WriteClipboard(newClipboard Clipboard) {
	c.receivedClipboard = &newClipboard

	c.AddClipboardToHistory(&newClipboard)

	if newClipboard.IsImage {
		xclipboard.Write(xclipboard.FmtImage, newClipboard.Data)
		return
	}
	xclipboard.Write(xclipboard.FmtText, newClipboard.Data)
}

// AddClipboardToHistory add clipbaord to clipbaord history
func (c *ClipboardManager) AddClipboardToHistory(newClipboard *Clipboard) {
	c.ClipboardsHistory = limitAppend(c.config.MaxHistory, c.ClipboardsHistory, newClipboard)
	c.ClipboardsHistoryUpdated <- struct{}{}
}

// IsReceivedDevice returns true if it's the same device with the received clipboard
func (c *ClipboardManager) IsReceivedDevice(dv *device.Device) bool {
	if c.receivedClipboard == nil {
		return false
	}

	if c.receivedClipboard.Device == nil {
		return false
	}

	return c.receivedClipboard.Device.AddressInfo.ID == dv.AddressInfo.ID
}

// IsReceivedClipboard returns true if it's same clipboard data with the received clipboard
func (c *ClipboardManager) IsReceivedClipboard(clipboardData []byte) bool {
	if c.receivedClipboard == nil {
		return false
	}

	return bytes.Equal(clipboardData, c.receivedClipboard.Data)
}

// IsFileURIList reports whether the clipboard text is a file:// URI list,
// indicating a file-copy operation that is handled by the file clipboard
// watcher rather than the text sync path.
func (c *ClipboardManager) IsFileURIList(data []byte) bool {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "file://") {
			return true
		}
	}
	return false
}

// SetFileClipboardActive marks that a file-copy operation is in progress.
// The text/image clipboard watcher suppresses sync for 5 seconds to avoid
// broadcasting the file path as text while xclip serves the uri-list target.
func (c *ClipboardManager) SetFileClipboardActive() {
	c.fileClipboardActive.Store(true)
	go func() {
		time.Sleep(5 * time.Second)
		c.fileClipboardActive.Store(false)
	}()
}

// IsFileClipboardActive returns true if a file-copy suppression window is active.
func (c *ClipboardManager) IsFileClipboardActive() bool {
	return c.fileClipboardActive.Load()
}
