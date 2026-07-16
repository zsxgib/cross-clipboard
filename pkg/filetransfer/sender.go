package filetransfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/protobuf"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
)

// ProgressFunc is called periodically during a send with bytes sent and the
// total size. It mirrors zero-share's progress/bitrate UI updates.
type ProgressFunc func(sent, total int64)

// SendFile streams srcPath to the peer over t, mirroring zero-share's Sender:
//  1. send MetaData (with the PGP-wrapped AES key when encrypting)
//  2. wait for EVENT_RECEIVER_ACCEPT
//  3. send chunks (AES-GCM encrypted), waiting for EVENT_RECEIVED_CHUNK after each (stop-and-wait)
//  4. abort on EVENT_RECEIVER_REJECT or EVENT_VALIDATE_ERROR
//
// encrypter wraps the AES key with the peer's PGP public key; nil disables
// application-layer encryption (relying on libp2p transport encryption only),
// matching zero-share's isEncrypt=false path.
func SendFile(ctx context.Context, t Transport, srcPath string, encrypter *crypto.PGPEncrypter, chunkSize int, onProgress ProgressFunc) error {
	if chunkSize <= 0 {
		chunkSize = ChunkSize
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory: %s", srcPath)
	}

	var aesKey, wrappedKey []byte
	if encrypter != nil {
		aesKey, err = GenerateAESKey()
		if err != nil {
			return err
		}
		wrappedKey, err = WrapAESKey(encrypter, aesKey)
		if err != nil {
			return err
		}
	}

	id := newID()
	meta := &protobuf.MetaData{
		Name: filepath.Base(srcPath),
		Size: info.Size(),
		Type: mimeType(srcPath),
		Key:  wrappedKey,
	}

	// 1) send metadata
	if err := t.SendMessage(&protobuf.Message{Id: id, Data: &protobuf.Message_MetaData{MetaData: meta}}); err != nil {
		return xerror.NewRuntimeError("send metadata").Wrap(err)
	}

	// 2) wait for ACCEPT
	ack, err := t.ReceiveMessage(ctx)
	if err != nil {
		return xerror.NewRuntimeError("await accept").Wrap(err)
	}
	ev, _ := ackEvent(ack)
	if ev != protobuf.ReceiveEvent_EVENT_RECEIVER_ACCEPT {
		if ev == protobuf.ReceiveEvent_EVENT_RECEIVER_REJECT {
			return fmt.Errorf("receiver rejected %s", meta.Name)
		}
		return fmt.Errorf("unexpected event waiting for accept: %v", ev)
	}

	// 3) stream chunks, stop-and-wait
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	buf := make([]byte, chunkSize)
	var sent int64
	last := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			payload := buf[:n]
			if aesKey != nil {
				payload, err = EncryptChunk(aesKey, payload)
				if err != nil {
					return err
				}
			}
			if err := t.SendMessage(&protobuf.Message{Id: id, Data: &protobuf.Message_Chunk{Chunk: payload}}); err != nil {
				return xerror.NewRuntimeError("send chunk").Wrap(err)
			}
			ack, err := t.ReceiveMessage(ctx)
			if err != nil {
				return xerror.NewRuntimeError("await chunk ack").Wrap(err)
			}
			aev, _ := ackEvent(ack)
			switch aev {
			case protobuf.ReceiveEvent_EVENT_RECEIVED_CHUNK:
				// proceed to next chunk
			case protobuf.ReceiveEvent_EVENT_VALIDATE_ERROR:
				return fmt.Errorf("receiver validate error")
			case protobuf.ReceiveEvent_EVENT_RECEIVER_REJECT:
				return fmt.Errorf("receiver rejected during transfer")
			default:
				return fmt.Errorf("unexpected event after chunk: %v", aev)
			}
			sent += int64(n)
			if onProgress != nil && (sent == info.Size() || time.Since(last) > 200*time.Millisecond) {
				onProgress(sent, info.Size())
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("read source: %w", rerr)
		}
	}
	if onProgress != nil {
		onProgress(sent, info.Size())
	}
	return nil
}
