package crossclipboard

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
	"github.com/ntsd/cross-clipboard/pkg/stream"
)

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

	res, err := filetransfer.ReceiveFile(ctx, t, cc.pgpDecrypter, opts, cc.fileTempDir, nil)
	if err != nil {
		cc.ErrorChan <- fmt.Errorf("receive file from %s: %w", peerID, err)
		s.Close()
		return
	}
	cc.LogChan <- fmt.Sprintf("received file %s (%d bytes) from %s", res.Meta.GetName(), res.Meta.GetSize(), peerID)

	if cc.onFileReceived != nil {
		cc.onFileReceived(res.Path, res.Meta)
	}
	s.Close()
}

// SendFileToPeer opens a FileProtocolID stream to a trusted device and streams
// srcPath to it. Called by the OS file-clipboard watcher when a file is copied.
func (cc *CrossClipboard) SendFileToPeer(ctx context.Context, dv *device.Device, srcPath string) error {
	if dv.Status != device.StatusConnected {
		return fmt.Errorf("device %s not connected", dv.AddressInfo.ID)
	}
	if dv.PgpEncrypter == nil {
		return fmt.Errorf("device %s not trusted (no pgp encrypter)", dv.AddressInfo.ID)
	}

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
	return filetransfer.SendFile(ctx, t, srcPath, enc, cc.Config.FileChunkSize, func(sent, total int64) {
		cc.LogChan <- fmt.Sprintf("sending %s: %d/%d bytes", filepath.Base(srcPath), sent, total)
	})
}

// SetFileReceivedHook installs the callback invoked after a file is fully
// received. The hook typically writes the file to the OS clipboard and (if
// AutoPaste is set) simulates Ctrl+V.
func (cc *CrossClipboard) SetFileReceivedHook(hook func(path string, meta interface{})) {
	cc.onFileReceived = hook
}
