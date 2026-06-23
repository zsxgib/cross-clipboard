// Package filetransfer implements end-to-end file clipboard sync.
//
// Wire format (over libp2p stream, framed by pkg/stream):
//   1. Sender pushes DataTypeFileMeta carrying a FileMeta record
//      {id (uuid), name, size, sha256, mtime}. id+name+size are PGP-encrypted
//      along with the rest of the metadata so file names don't leak to passive
//      network observers.
//   2. Sender streams DataTypeFileChunk payloads. Each chunk's PGP-encrypted
//      body is at most chunkSize (64KiB). The receiver accumulates until
//      `size` bytes have arrived, then computes sha256 and compares against
//      the metadata.
//   3. Receiver emits DataTypeFileEnd (success) or DataTypeFileError (failure).
//
// Loop prevention: a 5-second dedup window on sha256 blocks immediate
// re-send when the receiver writes the file to its own clipboard and the
// clipboard watcher fires.
package filetransfer

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ChunkSize is the maximum number of payload bytes per DataTypeFileChunk
// frame (64 KiB). 64 KiB matches the typical TCP max segment and keeps the
// buffered writer from sitting on too much memory.
const ChunkSize = 64 * 1024

// File channel data types. These mirror the values previously added to
// pkg/stream/const.go but are owned by the filetransfer package to avoid
// a stream <-> filetransfer import cycle. The numeric values must stay
// stable across the wire.
const (
	DataTypeFileMeta  byte = 0xF8
	DataTypeFileChunk byte = 0xF7
	DataTypeFileEnd   byte = 0xF6
	DataTypeFileError byte = 0xF5
)

// FileMeta is the header sent before a file's body. The ID is a random
// UUID-style hex string used to correlate the stream of chunks that
// follow. SHA256 is the hex-encoded SHA-256 of the file body.
type FileMeta struct {
	ID     string // 32 hex chars (16 random bytes)
	Name   string // base filename, no path
	Size   int64  // file size in bytes
	SHA256 string // hex-encoded sha256
	MTime  int64  // unix seconds
}

// ComputeSHA256 returns the hex sha256 of the given file's content.
func ComputeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FilePath returns the canonical name to use when persisting the file on
// the receiver. It strips any path components and replaces separators so a
// malicious sender can't escape the temp directory.
func SafeFileName(name string) string {
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "" {
		name = "file"
	}
	return name
}

// --- memory dedup ---------------------------------------------------------

// Dedup blocks re-sending the same sha256 within a short window so the
// receive-then-paste cycle doesn't loop back.
type Dedup struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
}

func NewDedup(window time.Duration) *Dedup {
	if window <= 0 {
		window = 5 * time.Second
	}
	return &Dedup{seen: make(map[string]time.Time), window: window}
}

// Touch records a sha256 as just-seen. Returns true if it was already
// within the window (caller should skip the work).
func (d *Dedup) Touch(sha string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if t, ok := d.seen[sha]; ok && now.Sub(t) < d.window {
		d.seen[sha] = now
		return true
	}
	d.seen[sha] = now
	return false
}

// Sweep removes entries older than 2*window. Cheap to call periodically.
func (d *Dedup) Sweep() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for k, t := range d.seen {
		if now.Sub(t) > 2*d.window {
			delete(d.seen, k)
		}
	}
}

// --- temp dir ------------------------------------------------------------

// TempManager owns the receiver's temp file directory and the 24h cleanup
// pass.
type TempManager struct {
	Dir       string
	Retention time.Duration
}

// NewTempManager ensures the temp dir exists and returns a manager for it.
func NewTempManager(dir string, retention time.Duration) (*TempManager, error) {
	if dir == "" {
		return nil, errors.New("temp dir path is empty")
	}
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp dir: %w", err)
	}
	return &TempManager{Dir: dir, Retention: retention}, nil
}

// PathFor returns the absolute path where the file with the given sha and
// name should be placed on disk. Files are grouped by short sha prefix to
// keep directories bounded.
func (t *TempManager) PathFor(sha, name string) string {
	prefix := sha
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	dir := filepath.Join(t.Dir, prefix)
	return filepath.Join(dir, SafeFileName(name))
}

// Sweep removes files/dirs older than t.Retention.
func (t *TempManager) Sweep() (int, error) {
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-t.Retention)
	removed := 0
	for _, e := range entries {
		full := filepath.Join(t.Dir, e.Name())
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(full); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// String returns a human-readable summary of a path for logging.
func (t *TempManager) String() string {
	return strings.TrimSuffix(t.Dir, string(filepath.Separator))
}

// --- FileMeta wire format -----------------------------------------------
//
// FileMeta is JSON-encoded into the DataTypeFileMeta payload. This keeps the
// format human-inspectable for debugging and avoids a protoc dependency for
// the receiver build. A future migration to protobuf is straightforward:
// replace Marshal/UnmarshalFileMeta with proto.Marshal/Unmarshal on a
// generated FileMeta message and the rest of the package is unchanged.

// MarshalFileMeta encodes a FileMeta to JSON.
func MarshalFileMeta(m *FileMeta) ([]byte, error) {
	if m == nil {
		return nil, errors.New("nil FileMeta")
	}
	return []byte(`{"id":"` + m.ID + `","name":"` + escapeJSON(m.Name) +
		`","size":` + itoa(m.Size) + `,"sha256":"` + m.SHA256 +
		`","mtime":` + itoa(m.MTime) + `}`), nil
}

// UnmarshalFileMeta decodes a FileMeta from JSON.
func UnmarshalFileMeta(b []byte) (*FileMeta, error) {
	s := string(b)
	m := &FileMeta{}
	m.ID = extractJSONString(s, "id")
	m.Name = unescapeJSON(extractJSONString(s, "name"))
	m.SHA256 = extractJSONString(s, "sha256")
	var err error
	if m.Size, err = extractJSONInt(s, "size"); err != nil {
		return nil, err
	}
	if m.MTime, err = extractJSONInt(s, "mtime"); err != nil {
		return nil, err
	}
	if m.ID == "" || m.SHA256 == "" {
		return nil, errors.New("FileMeta missing id or sha256")
	}
	return m, nil
}

// escapeJSON is a tiny JSON string escaper. File names almost never contain
// quotes or control chars, so we only handle the necessary cases.
func escapeJSON(s string) string {
	if !strings.ContainsAny(s, `"\\`+"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// unescapeJSON reverses escapeJSON. Tolerant of unknown escapes.
func unescapeJSON(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			break
		}
		i++
		switch s[i] {
		case '"', '\\', '/':
			b.WriteByte(s[i])
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			if i+4 < len(s) {
				// very small subset; we don't expect non-ascii
				i += 4
			}
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// extractJSONString pulls the value of a string field from a flat JSON
// object. It does not handle nested objects, but FileMeta has none.
func extractJSONString(s, key string) string {
	needle := `"` + key + `":"`
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	start := i + len(needle)
	var b strings.Builder
	escape := false
	for j := start; j < len(s); j++ {
		c := s[j]
		if escape {
			// pass through escape; unescapeJSON will clean up later
			if b.Len() > 0 {
				// keep the running buffer escape-free
				last := b.String()[b.Len()-1]
				_ = last
			}
			b.WriteByte('\\')
			b.WriteByte(c)
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			return unescapeJSON(b.String())
		}
		b.WriteByte(c)
	}
	return ""
}

// extractJSONInt pulls the value of an integer field. We don't import
// encoding/json to keep the package self-contained for embedded builds.
func extractJSONInt(s, key string) (int64, error) {
	needle := `"` + key + `":`
	i := strings.Index(s, needle)
	if i < 0 {
		return 0, errors.New("field " + key + " not found")
	}
	start := i + len(needle)
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	var n int64
	sawDigit := false
	for start < len(s) {
		c := s[start]
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
			sawDigit = true
			start++
			continue
		}
		break
	}
	if !sawDigit {
		return 0, errors.New("field " + key + " not a number")
	}
	return n, nil
}

// itoa avoids importing strconv just for one call.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// newID returns a 32-char random hex id (16 random bytes).
func newID() (string, error) {
	var b [16]byte
	if _, err := randRead(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func randRead(p []byte) (int, error) {
	return crand.Read(p)
}
