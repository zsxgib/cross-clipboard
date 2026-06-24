package stream

import (
	"bufio"
	"fmt"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/ntsd/cross-clipboard/pkg/clipboard"
	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/devicemanager"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
)

// StreamHandler struct for stream handler
type StreamHandler struct {
	config           *config.Config
	clipboardManager *clipboard.ClipboardManager
	deviceManager    *devicemanager.DeviceManager
	logChan          chan string
	errorChan        chan error

	pgpDecrypter *crypto.PGPDecrypter

	// File transfer channel configuration. fileTempDir is the directory the
	// receiver drops incoming files into. onFileReceived is invoked once a
	// file has been fully received, validated, and written; it usually
	// puts the file on the OS clipboard and triggers Ctrl+V.
	fileTempDir     string
	onFileReceived  func(path string, meta *filetransfer.FileMeta)
}

// NewStreamHandler initial new stream handler
func NewStreamHandler(
	cfg *config.Config,
	cp *clipboard.ClipboardManager,
	deviceManager *devicemanager.DeviceManager,
	logChan chan string,
	errorChan chan error,
	pgpDecrypter *crypto.PGPDecrypter,
	fileTempDir string,
	onFileReceived func(path string, meta *filetransfer.FileMeta),
) *StreamHandler {
	s := &StreamHandler{
		config:          cfg,
		clipboardManager: cp,
		deviceManager:    deviceManager,
		logChan:          logChan,
		errorChan:        errorChan,
		pgpDecrypter:     pgpDecrypter,
		fileTempDir:      fileTempDir,
		onFileReceived:   onFileReceived,
	}
	go s.CreateWriteData()
	return s
}

// HandleStream handler when a peer connect this host
func (s *StreamHandler) HandleStream(stream network.Stream) {
	s.logChan <- fmt.Sprintf("peer %s connecting to this host", stream.Conn().RemotePeer())

	// If a concurrent outbound dial already added a device for this
	// peer, the inbound stream is the duplicate: close it and bail.
	peerID := stream.Conn().RemotePeer()
	if existing := s.deviceManager.GetDevice(peerID.String()); existing != nil && existing.Status == device.StatusConnected && existing.Stream != nil {
		s.logChan <- fmt.Sprintf("inbound from %s is duplicate of existing outbound; closing", peerID)
		_ = stream.Close()
		return
	}

	// Create a new peer
	dv := device.NewDevice(peer.AddrInfo{
		ID:    stream.Conn().RemotePeer(),
		Addrs: []multiaddr.Multiaddr{stream.Conn().RemoteMultiaddr()},
	}, stream)

	dv.Reader = bufio.NewReader(stream)
	dv.Writer = bufio.NewWriter(stream)
	s.deviceManager.AddDevice(dv)

	go s.CreateReadData(dv.Reader, dv)

	s.logChan <- fmt.Sprintf("peer %s connected to this host", stream.Conn().RemotePeer())
	// 'stream' will stay open until you close it (or the other side closes it).
}
