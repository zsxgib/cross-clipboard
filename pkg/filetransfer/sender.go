package filetransfer

import (
	"bufio"
			"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
)

// ProgressFunc is called periodically during a file send with bytes sent
// and the total size. Errors are non-fatal; the sender continues.
type ProgressFunc func(sent, total int64)

// SendFile streams one file from disk to the given device over its
// established libp2p stream. The function is safe to call concurrently from
// multiple goroutines for different files; per-device writes are serialized
// via the per-device writer mutex.
func SendFile(
	dv *device.Device,
	srcPath string,
	dedup *Dedup,
	logf func(string),
	errf func(error),
	progress ProgressFunc,
) error {
	if dv == nil || dv.Writer == nil {
		return errors.New("device or device writer is nil")
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory: %s", srcPath)
	}
	sha, err := ComputeSHA256(srcPath)
	if err != nil {
		return fmt.Errorf("compute sha256: %w", err)
	}
	if dedup != nil && dedup.Touch(sha) {
		logf(fmt.Sprintf("dedup: skipping %s (sha %s) sent within window", filepath.Base(srcPath), sha[:8]))
		return nil
	}

	id, err := newID()
	if err != nil {
		return err
	}
	meta := &FileMeta{
		ID:     id,
		Name:   filepath.Base(srcPath),
		Size:   info.Size(),
		SHA256: sha,
		MTime:  info.ModTime().Unix(),
	}

	metaBytes, err := MarshalFileMeta(meta)
	if err != nil {
		return err
	}

	// Lock the per-device writer so chunked writes don't interleave with
	// clipboard writes from other goroutines.
	dv.WriteMu.Lock()
	defer dv.WriteMu.Unlock()

	// 1) Send metadata
	if err := writeFrame(dv.Writer, byte(DataTypeFileMeta), metaBytes); err != nil {
		dv.Status = device.StatusError
		return fmt.Errorf("send meta: %w", err)
	}
	logf(fmt.Sprintf("sending file: %s size=%d sha=%s to %s", meta.Name, meta.Size, sha[:8], dv.AddressInfo.ID))

	// 2) Stream chunks
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	buf := make([]byte, ChunkSize)
	var sent int64
	lastProgress := time.Now()
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if werr := writeFrame(dv.Writer, byte(DataTypeFileChunk), buf[:n]); werr != nil {
				dv.Status = device.StatusError
				return fmt.Errorf("send chunk: %w", werr)
			}
			sent += int64(n)
			if progress != nil && (sent == info.Size() || time.Since(lastProgress) > 200*time.Millisecond) {
				progress(sent, info.Size())
				lastProgress = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("read source: %w", rerr)
		}
	}
	if progress != nil {
		progress(sent, info.Size())
	}

	// 3) Send end
	end := []byte("ok")
	if err := writeFrame(dv.Writer, byte(DataTypeFileEnd), end); err != nil {
		dv.Status = device.StatusError
		return fmt.Errorf("send end: %w", err)
	}
	logf(fmt.Sprintf("sent file: %s (%d bytes) to %s", meta.Name, meta.Size, dv.AddressInfo.ID))
	return nil
}

// writeFrame writes | data size (int64, 8 bytes) | data type (1 byte) | payload |.
// This mirrors the existing wire format in pkg/stream/io.go.
func writeFrame(w *bufio.Writer, dataType byte, payload []byte) error {
	size := int64(len(payload) + 1)
	var sizeBuf [8]byte
	for i := 0; i < 8; i++ {
		sizeBuf[i] = byte(size >> (8 * i))
	}
	if _, err := w.Write(sizeBuf[:]); err != nil {
		return xerror.NewRuntimeError("write size").Wrap(err)
	}
	if err := w.WriteByte(dataType); err != nil {
		return xerror.NewRuntimeError("write type").Wrap(err)
	}
	if _, err := w.Write(payload); err != nil {
		return xerror.NewRuntimeError("write payload").Wrap(err)
	}
	return w.Flush()
}

// silence unused imports when the file builds without receiver
