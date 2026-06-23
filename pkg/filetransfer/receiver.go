package filetransfer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ReceiveState is a single in-flight file transfer on the receiver. The
// stream reader drives it: feed bytes to HandleFrame as they arrive.
type ReceiveState struct {
	meta      *FileMeta
	written   int64
	hasher    hash.Hash
	f         *os.File
	path      string
	dir       string
}

// HandleFrame is called by the stream reader for each FileMeta/Chunk/End/
// Error frame. It returns the fully-received file path on success, or
// (nil, err) on a hard failure that should close the connection.
type HandleResult struct {
	FinalPath string // set when a transfer completed successfully
	Meta      *FileMeta
	Err       error
}

func NewReceiveState(dir string) *ReceiveState {
	return &ReceiveState{dir: dir}
}

// HandleFrame consumes a single frame. typeByte is one of the
// stream.DataTypeFile* constants. payload is the decrypted frame body.
func (r *ReceiveState) HandleFrame(typeByte byte, payload []byte) (done bool, result HandleResult) {
	switch typeByte {
	case 0xF8: // DataTypeFileMeta
		if r.meta != nil {
			return true, result.withErr(errors.New("received FileMeta while another transfer in progress"))
		}
		m, err := UnmarshalFileMeta(payload)
		if err != nil {
			return true, result.withErr(fmt.Errorf("decode FileMeta: %w", err))
		}
		if m.Size < 0 {
			return true, result.withErr(fmt.Errorf("negative size in FileMeta: %d", m.Size))
		}
		if m.SHA256 == "" || len(m.SHA256) != 64 {
			return true, result.withErr(errors.New("invalid sha256 in FileMeta"))
		}
		// Prepare destination path
		prefix := m.SHA256
		if len(prefix) > 16 {
			prefix = prefix[:16]
		}
		dir := filepath.Join(r.dir, prefix)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return true, result.withErr(fmt.Errorf("mkdir dest dir: %w", err))
		}
		dst := filepath.Join(dir, SafeFileName(m.Name))
		// If the path already exists, suffix with -<id[:6]>
		if _, err := os.Stat(dst); err == nil {
			base := SafeFileName(m.Name)
			ext := filepath.Ext(base)
			stem := base[:len(base)-len(ext)]
			dst = filepath.Join(dir, fmt.Sprintf("%s-%s%s", stem, m.ID[:6], ext))
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return true, result.withErr(fmt.Errorf("open dest: %w", err))
		}
		r.meta = m
		r.path = dst
		r.f = f
		r.hasher = sha256New()
		return false, result

	case 0xF7: // DataTypeFileChunk
		if r.meta == nil {
			return true, result.withErr(errors.New("received FileChunk before FileMeta"))
		}
		if r.written+int64(len(payload)) > r.meta.Size {
			r.f.Close()
			os.Remove(r.path)
			return true, result.withErr(fmt.Errorf("chunk overflows declared size: have %d, chunk %d, declared %d",
				r.written, len(payload), r.meta.Size))
		}
		n, err := r.f.Write(payload)
		if err != nil {
			r.f.Close()
			os.Remove(r.path)
			return true, result.withErr(fmt.Errorf("write chunk: %w", err))
		}
		r.written += int64(n)
		r.hasher.Write(payload[:n])
		return false, result

	case 0xF6: // DataTypeFileEnd
		if r.meta == nil {
			return true, result.withErr(errors.New("received FileEnd before FileMeta"))
		}
		if r.written != r.meta.Size {
			r.f.Close()
			os.Remove(r.path)
			return true, result.withErr(fmt.Errorf("incomplete: got %d bytes, declared %d", r.written, r.meta.Size))
		}
		if err := r.f.Close(); err != nil {
			os.Remove(r.path)
			return true, result.withErr(fmt.Errorf("close dest: %w", err))
		}
		got := hex.EncodeToString(r.hasher.Sum(nil))
		if got != r.meta.SHA256 {
			os.Remove(r.path)
			return true, result.withErr(fmt.Errorf("sha256 mismatch: got %s, want %s", got, r.meta.SHA256))
		}
		result.FinalPath = r.path
		result.Meta = r.meta
		return true, result

	case 0xF5: // DataTypeFileError
		if r.f != nil {
			r.f.Close()
			if r.path != "" {
				os.Remove(r.path)
			}
		}
		return true, result.withErr(fmt.Errorf("remote reported error: %s", string(payload)))

	default:
		return false, result
	}
}

func (h HandleResult) withErr(err error) HandleResult {
	h.Err = err
	return h
}

// discardReader is unused but kept for symmetry with future io.ReadCloser
// integrations.  (compiler hint to keep import set stable)
var _ = io.Copy
var _ = bufio.NewReader

func sha256New() hash.Hash {
	return sha256.New()
}
