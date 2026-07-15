package filetransfer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/protobuf"
)

// ReceiveOptions mirrors zero-share's ReceiveOptions (autoAccept, maxSize).
type ReceiveOptions struct {
	AutoAccept bool
	MaxSize    int32
}

// DefaultReceiveOptions mirrors zero-share's DEFAULT_RECEIVE_OPTIONS.
func DefaultReceiveOptions() ReceiveOptions {
	return ReceiveOptions{AutoAccept: true, MaxSize: 1 << 30} // 1 GiB
}

// FileResult is returned when a transfer completes successfully.
type FileResult struct {
	Path string
	Meta *protobuf.MetaData
}

// ReceiveFile receives a single file from t into destDir, mirroring zero-share's
// Receiver:
//  1. read MetaData, validate size against MaxSize
//  2. accept (auto or via onAccept) or reject
//  3. unwrap the PGP-wrapped AES key (when encrypted)
//  4. accumulate AES-GCM-decrypted chunks, ack each with EVENT_RECEIVED_CHUNK
//  5. finish when receivedSize >= declared size
func ReceiveFile(ctx context.Context, t Transport, decrypter *crypto.PGPDecrypter, opts ReceiveOptions, destDir string, onAccept func(*protobuf.MetaData) bool) (*FileResult, error) {
	msg, err := t.ReceiveMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("receive metadata: %w", err)
	}
	metaMD, ok := msg.Data.(*protobuf.Message_MetaData)
	if !ok {
		return nil, fmt.Errorf("expected MetaData, got %T", msg.Data)
	}
	m := metaMD.MetaData
	id := msg.Id

	// validate size
	if opts.MaxSize > 0 && m.Size > opts.MaxSize {
		_ = t.SendMessage(eventMsg(id, protobuf.ReceiveEvent_EVENT_VALIDATE_ERROR))
		return nil, fmt.Errorf("file size %d exceeds max %d", m.Size, opts.MaxSize)
	}

	// accept decision
	accept := opts.AutoAccept
	if !accept && onAccept != nil {
		accept = onAccept(m)
	}
	if !accept {
		_ = t.SendMessage(eventMsg(id, protobuf.ReceiveEvent_EVENT_RECEIVER_REJECT))
		return nil, fmt.Errorf("rejected %s", m.Name)
	}
	if err := t.SendMessage(eventMsg(id, protobuf.ReceiveEvent_EVENT_RECEIVER_ACCEPT)); err != nil {
		return nil, fmt.Errorf("send accept: %w", err)
	}

	// unwrap AES key
	var aesKey []byte
	if decrypter != nil && len(m.Key) > 0 {
		aesKey, err = UnwrapAESKey(decrypter, m.Key)
		if err != nil {
			return nil, fmt.Errorf("unwrap aes key: %w", err)
		}
	}

	// prepare destination
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir dest: %w", err)
	}
	dst := filepath.Join(destDir, safeFileName(m.Name))
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open dest: %w", err)
	}

	var written int64
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		cm, err := t.ReceiveMessage(ctx)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("receive chunk: %w", err)
		}
		chunkMD, ok := cm.Data.(*protobuf.Message_Chunk)
		if !ok {
			break // not a chunk: transfer ended
		}
		data := chunkMD.Chunk
		if aesKey != nil {
			data, err = DecryptChunk(aesKey, data)
			if err != nil {
				f.Close()
				_ = t.SendMessage(eventMsg(cm.Id, protobuf.ReceiveEvent_EVENT_VALIDATE_ERROR))
				return nil, fmt.Errorf("decrypt chunk: %w", err)
			}
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return nil, fmt.Errorf("write chunk: %w", err)
		}
		written += int64(len(data))
		if err := t.SendMessage(eventMsg(cm.Id, protobuf.ReceiveEvent_EVENT_RECEIVED_CHUNK)); err != nil {
			f.Close()
			return nil, fmt.Errorf("send ack: %w", err)
		}
		if m.Size > 0 && written >= int64(m.Size) {
			break
		}
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close dest: %w", err)
	}
	if m.Size > 0 && written != int64(m.Size) {
		os.Remove(dst)
		return nil, fmt.Errorf("incomplete: got %d bytes, declared %d", written, m.Size)
	}
	return &FileResult{Path: dst, Meta: m}, nil
}
