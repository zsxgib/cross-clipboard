package filetransfer

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"path/filepath"

	"github.com/ntsd/cross-clipboard/pkg/protobuf"
)

// newID returns a random hex id used to correlate a file's chunk stream.
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// eventMsg builds a Message carrying a ReceiveEvent.
func eventMsg(id string, ev protobuf.ReceiveEvent) *protobuf.Message {
	return &protobuf.Message{Id: id, Data: &protobuf.Message_ReceiveEvent{ReceiveEvent: ev}}
}

// ackEvent returns the receive event carried by m, if any.
func ackEvent(m *protobuf.Message) (protobuf.ReceiveEvent, bool) {
	if m == nil {
		return 0, false
	}
	if ev, ok := m.Data.(*protobuf.Message_ReceiveEvent); ok {
		return ev.ReceiveEvent, true
	}
	return 0, false
}

// safeFileName strips path components so a malicious sender can't escape destDir.
func safeFileName(name string) string {
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "" {
		return "file"
	}
	return name
}

// mimeType returns a best-effort MIME type from the file extension.
func mimeType(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return mime.TypeByExtension(ext)
}
