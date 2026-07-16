package crossclipboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
	"github.com/ntsd/cross-clipboard/pkg/stream"
)

// FileProgress is pushed to FileProgressChan during file transfers so the TUI
// can display a live progress bar in the clipboard table.
type FileProgress struct {
	FileName  string // base filename
	Sent      int64  // bytes transferred
	Total     int64  // total file size in bytes
	Direction string // "send" or "recv"
	Done      bool   // transfer completed
	Err       string // non-empty on failure
	Speed     int64     // bytes per second (instantaneous)
	Time      time.Time // when this update was generated
}

// handleFileStream is the libp2p stream handler for FileProtocolID. The remote
// peer opened a stream to send a file; receive it and trigger the OS paste hook.
func (cc *CrossClipboard) handleFileStream(s network.Stream) {
	peerID := s.Conn().RemotePeer()
	cc.LogChan <- fmt.Sprintf("file stream from %s", peerID)

	dv := cc.DeviceManager.GetDevice(peerID.String())
	if dv == nil || dv.Status != device.StatusConnected {
		cc.ErrorChan <- fmt.Errorf("file stream from untrusted peer %s", peerID)
		s.Close()
		return
	}

	t := filetransfer.NewIOTransport(s, s)
	opts := filetransfer.ReceiveOptions{
		AutoAccept: cc.Config.FileAutoAccept,
		MaxSize:    cc.Config.MaxFileSize,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var lastRecv int64
	lastRecvTime := time.Now()
	var lastRecvTotal int64
	recvName := ""
	res, err := filetransfer.ReceiveFile(ctx, t, cc.pgpDecrypter, opts, cc.fileTempDir, nil, func(name string, received, total int64) {
		now := time.Now()
		elapsed := now.Sub(lastRecvTime).Seconds()
		var speed int64
		if elapsed > 0 {
			speed = int64(float64(received-lastRecv) / elapsed)
		}
		lastRecv = received
		lastRecvTime = now
		lastRecvTotal = total
		recvName = name
		cc.FileProgressChan <- FileProgress{
			FileName:  name,
			Sent:      received,
			Total:     total,
			Direction: "recv",
			Speed:     speed,
			Time:      now,
		}
	})
	if err != nil {
		cc.ErrorChan <- fmt.Errorf("receive file from %s: %w", peerID, err)
		cc.FileProgressChan <- FileProgress{FileName: recvName, Sent: lastRecv, Total: lastRecvTotal, Direction: "recv", Done: true, Err: err.Error(), Time: time.Now()}
		s.Close()
		return
	}
	cc.LogChan <- fmt.Sprintf("received file %s (%d bytes) from %s", res.Meta.GetName(), res.Meta.GetSize(), peerID)
	cc.FileProgressChan <- FileProgress{FileName: res.Meta.GetName(), Sent: res.Meta.GetSize(), Total: res.Meta.GetSize(), Direction: "recv", Done: true, Time: time.Now()}

	if cc.onFileReceived != nil {
		cc.onFileReceived(res.Path, res.Meta)
	}
	s.Close()
}

// SendFileToPeer opens a FileProtocolID stream to a trusted device and streams
// srcPath to it. Called by the OS file-clipboard watcher when a file is copied.
// If srcPath is a directory, each file inside is sent individually with its
// relative path so the receiver can recreate the directory structure.
func (cc *CrossClipboard) SendFileToPeer(ctx context.Context, dv *device.Device, srcPath string) error {
	if dv.Status != device.StatusConnected {
		return fmt.Errorf("device %s not connected", dv.AddressInfo.ID)
	}
	if dv.PgpEncrypter == nil {
		return fmt.Errorf("device %s not trusted (no pgp encrypter)", dv.AddressInfo.ID)
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return filepath.Walk(srcPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(srcPath, path)
			if err != nil {
				return err
			}
			return cc.sendFileToPeerStream(ctx, dv, path, relPath)
		})
	}
	return cc.sendFileToPeerStream(ctx, dv, srcPath, "")
}

// sendFileToPeerStream opens one libp2p stream and sends a single file over it.
func (cc *CrossClipboard) sendFileToPeerStream(ctx context.Context, dv *device.Device, srcPath string, relativePath string) error {
	s, err := cc.Host.NewStream(ctx, dv.AddressInfo.ID, stream.FileProtocolID)
	if err != nil {
		return fmt.Errorf("open file stream to %s: %w", dv.AddressInfo.ID, err)
	}
	defer s.Close()

	var enc *crypto.PGPEncrypter
	if cc.Config.FileEncrypt {
		enc = dv.PgpEncrypter
	}
	t := filetransfer.NewIOTransport(s, s)
	fname := filepath.Base(srcPath)
	var lastSent int64
	var lastTotal int64
	lastTime := time.Now()
	err = filetransfer.SendFile(ctx, t, srcPath, relativePath, enc, cc.Config.FileChunkSize, func(name string, sent, total int64) {
		lastTotal = total
		now := time.Now()
		elapsed := now.Sub(lastTime).Seconds()
		var speed int64
		if elapsed > 0 {
			speed = int64(float64(sent-lastSent) / elapsed)
		}
		lastSent = sent
		lastTime = now
		cc.LogChan <- fmt.Sprintf("sending %s: %d/%d bytes", name, sent, total)
		cc.FileProgressChan <- FileProgress{
			FileName:  name,
			Sent:      sent,
			Total:     total,
			Direction: "send",
			Speed:     speed,
			Time:      now,
		}
	})
	if err != nil {
		cc.FileProgressChan <- FileProgress{FileName: fname, Sent: lastSent, Total: lastTotal, Direction: "send", Done: true, Err: err.Error(), Time: time.Now()}
	} else {
		cc.FileProgressChan <- FileProgress{FileName: fname, Sent: lastTotal, Total: lastTotal, Direction: "send", Done: true, Time: time.Now()}
	}
	return err
}

// SetFileReceivedHook installs the callback invoked after a file is fully
// received. The hook typically writes the file to the OS clipboard and (if
// AutoPaste is set) simulates Ctrl+V.
func (cc *CrossClipboard) SetFileReceivedHook(hook func(path string, meta interface{})) {
	cc.onFileReceived = hook
}
