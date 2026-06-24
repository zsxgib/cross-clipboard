package crossclipboard

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	"github.com/ntsd/cross-clipboard/pkg/clipboard"
	"github.com/ntsd/cross-clipboard/pkg/clipboardfile"
	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/devicemanager"
	"github.com/ntsd/cross-clipboard/pkg/discovery"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
	"github.com/ntsd/cross-clipboard/pkg/stream"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
)

// CrossClipboard cross clipbaord struct
type CrossClipboard struct {
	Host   host.Host
	Config *config.Config

	ClipboardManager *clipboard.ClipboardManager
	DeviceManager    *devicemanager.DeviceManager

	streamHandler *stream.StreamHandler

	// fileTransfer bundles the OS-clipboard + temp dir + dedup state used
	// by the file copy/paste channel.
	fileTransfer *filetransfer.TempManager
	// fileReceivedHook is the optional OS-layer callback. main() wires it
	// up via SetFileReceivedHook; tests leave it nil.
	fileReceivedHook func(path string, meta *filetransfer.FileMeta)
	// fileWatcher is the OS-level file-URI clipboard watcher. nil when
	// pkg/clipboardfile reports the OS doesn't have xclip/xdotool or
	// PowerShell available.
	fileWatcher clipboardfile.FileClipboard

	// recentSelfSet dedups the Set->watcher echo loop. When we put a file
	// on the OS clipboard (via handleFileReceived), our own watcher will
	// see that same path on the next poll and try to dispatch it back to
	// the peer, starting a loop. Entries here expire after 5s.
	recentSelfSet map[string]time.Time
	selfSetMu     sync.Mutex

	LogChan   chan string
	ErrorChan chan error

	stopDiscovery chan struct{}
}

// NewCrossClipboard initial cross clipbaord
func NewCrossClipboard(cfg *config.Config) (*CrossClipboard, error) {
	cc := &CrossClipboard{
		Config:        cfg,
		LogChan:       make(chan string),
		ErrorChan:     make(chan error),
		stopDiscovery: make(chan struct{}),
		recentSelfSet: make(map[string]time.Time),
	}

	cc.ClipboardManager = clipboard.NewClipboardManager(cc.Config)
	cc.DeviceManager = devicemanager.NewDeviceManager(cc.Config)

	tempDir := cc.Config.FileTempDir
	if tempDir == "" {
		tempDir = filepath.Join(cc.Config.ConfigDirPath, "incoming")
	}
	tempMgr, tmErr := filetransfer.NewTempManager(tempDir, time.Duration(cc.Config.FileTempRetentionHours)*time.Hour)
	if tmErr != nil {
		return nil, xerror.NewFatalError("file transfer temp manager").Wrap(tmErr)
	}
	cc.fileTransfer = tempMgr

	// OS-level file URI clipboard watcher. nil when the OS toolchain
	// (xclip/xdotool on Linux, PowerShell on Windows) is missing.
	w := clipboardfile.New()
	if w.Available() {
		cc.fileWatcher = w
	} else {
		log.Printf("file clipboard watcher unavailable on this host; file sync disabled")
	}

	ctx := context.Background()

	// 0.0.0.0 will listen on any interface device.
	sourceMultiAddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", cc.Config.ListenHost, cc.Config.ListenPort))
	if err != nil {
		return nil, xerror.NewFatalError("error to multiaddr.NewMultiaddr").Wrap(err)
	}

	// libp2p.New constructs a new libp2p Host.
	host, err := libp2p.New(
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(cc.Config.ID),
	)
	if err != nil {
		return nil, xerror.NewFatalError("error to libp2p.New").Wrap(err)
	}
	cc.Host = host

	pgpDecrypter, err := crypto.NewPGPDecrypter(cfg.PGPPrivateKey)
	if err != nil {
		return nil, xerror.NewFatalError("error to crypto.NewPGPDecrypter").Wrap(err)
	}

	go func() {
		err := cc.DeviceManager.Load()
		if err != nil {
			cc.ErrorChan <- xerror.NewFatalError("can not load device from setting").Wrap(err)
		}

		streamHandler := stream.NewStreamHandler(
			cc.Config,
			cc.ClipboardManager,
			cc.DeviceManager,
			cc.LogChan,
			cc.ErrorChan,
			pgpDecrypter,
			cc.fileTransfer.Dir,
			cc.handleFileReceived,
		)
		cc.streamHandler = streamHandler

		// This function is called when a peer initiates a connection and starts a stream with this peer.
		cc.Host.SetStreamHandler(stream.PROTOCAL_ID, streamHandler.HandleStream)
		cc.LogChan <- fmt.Sprintf("[*] your multiaddress is: /ip4/%s/tcp/%v/p2p/%s", cc.Config.ListenHost, cc.Config.ListenPort, host.ID())

		peerInfoChan, err := discovery.InitMultiMDNS(cc.Host, cc.Config.GroupName, cc.LogChan)
		if err != nil {
			cc.ErrorChan <- xerror.NewFatalError("error to discovery.InitMultiMDNS").Wrap(err)
		}

	discoveryLoop:
		for {
			select {
			case peerInfo := <-peerInfoChan: // when discover a peer
				dv := cc.DeviceManager.GetDevice(peerInfo.ID.String())
				if dv != nil && dv.Status == device.StatusBlocked {
					cc.ErrorChan <- xerror.NewRuntimeErrorf("device %s is blocked", peerInfo.ID)
					continue
				}

				cc.LogChan <- fmt.Sprintf("connecting to peer: %s", peerInfo.ID)

				retry := 1
				for ; retry < 5; retry++ { // retry to connect
					if err := cc.Host.Connect(ctx, peerInfo); err != nil {
						cc.ErrorChan <- xerror.NewRuntimeErrorf(
							"error to connect to peer %s, retrying %d",
							peerInfo.ID,
							retry,
						).Wrap(err)
						time.Sleep(time.Duration(retry*10) * time.Second)
						continue
					}
					break
				}
				if retry == 5 {
					cc.ErrorChan <- xerror.NewRuntimeErrorf("error to connect to peer %s", peerInfo.ID)
					continue
				}

				// open a stream, this stream will be handled by handleStream other end
				stream, err := cc.Host.NewStream(ctx, peerInfo.ID, stream.PROTOCAL_ID)
				if err != nil {
					cc.ErrorChan <- xerror.NewRuntimeError("new stream error").Wrap(err)
					continue
				}

				// Check the latest device in the manager under the device
				// manager lock. If a concurrent inbound stream already
				// added a device for this peer id, reuse its stream and
				// PGP key instead of replacing with a new outbound
				// stream; this avoids the two-readers-one-peer deadlock
				// where one side writes the size header while the other
				// is reading the size header and they corrupt each
				// other's frame stream.
				existing := cc.DeviceManager.GetDevice(peerInfo.ID.String())
				if existing != nil && existing.Status == device.StatusConnected && existing.Stream != nil {
					cc.LogChan <- fmt.Sprintf("reusing inbound stream for %s, closing outbound", peerInfo.ID)
					_ = stream.Close()
					stream = existing.Stream
					dv = existing
				} else if dv == nil {
					dv = device.NewDevice(peerInfo, stream)
				} else {
					dv.AddressInfo = peerInfo
					dv.Stream = stream
					dv.Reader = bufio.NewReader(stream)
					dv.Writer = bufio.NewWriter(stream)
				}

				cc.DeviceManager.UpdateDevice(dv)
				if existing == nil || existing.Status != device.StatusConnected {
					go streamHandler.CreateReadData(dv.Reader, dv)
				}

				cc.LogChan <- fmt.Sprintf("connected to peer host: %s", peerInfo)
			case <-cc.stopDiscovery: // when stop discovery
				cc.LogChan <- "stop discovery peer"
				break discoveryLoop
			}
		}
	}()

	return cc, nil
}

func (cc *CrossClipboard) Stop() error {
	if cc.streamHandler != nil {
		for id, dv := range cc.DeviceManager.Devices {
			if dv.Status == device.StatusConnected {
				log.Printf("sending disconneced signal to peer %s \n", id)
				cc.streamHandler.SendSignal(dv, stream.SignalDisconnect)
			}
		}

		// sleep to wait sending disconnect signal
		time.Sleep(time.Second)

		for id, dv := range cc.DeviceManager.Devices {
			if dv.Status == device.StatusConnected {
				log.Printf("ending stream for peer %s \n", id)
				dv.Stream.Close()
			}
		}
	}

	cc.stopDiscovery <- struct{}{}

	if cc.fileWatcher != nil {
		// The watcher goroutine exits when its context is cancelled. We
		// can't reach the context from here, so we close the channel by
		// letting the process exit; in practice Stop() is followed by
		// os.Exit so this is a soft signal only.
	}

	err := cc.Host.Close()
	if err != nil {
		return xerror.NewFatalError("unable to close host").Wrap(err)
	}

	return nil
}

// handleFileReceived is called by the stream handler after a file is fully
// received, validated, and written to temp dir. It is the bridge to the OS
// clipboard + Ctrl+V step.
func (cc *CrossClipboard) handleFileReceived(path string, meta *filetransfer.FileMeta) {
	cc.LogChan <- fmt.Sprintf("file received: %s (%d bytes) at %s", meta.Name, meta.Size, path)
	// Record that we are about to put this path on the OS clipboard so
	// the watcher (which polls every 500ms) does not see our own Set
	// and echo the path back to the peer.
	cc.selfSetMu.Lock()
	cc.recentSelfSet[path] = time.Now()
	cc.selfSetMu.Unlock()
	cc.LogChan <- fmt.Sprintf("self-set guard: marked %s (5s)", path)
	// OS clipboard + paste wiring lives in pkg/clipboardfile. The wire
	// integration is intentionally a thin adapter so the package stays
	// importable from tests without bringing in OS subprocess calls.
	if cc.fileReceivedHook != nil {
		cc.fileReceivedHook(path, meta)
	}
}

// filterSelfSet strips paths we just put on the OS clipboard from a
// watcher emission. Anything in recentSelfSet newer than 5 seconds is
// dropped to prevent the Set->watcher echo loop.
func (cc *CrossClipboard) filterSelfSet(paths []string) []string {
	cc.selfSetMu.Lock()
	defer cc.selfSetMu.Unlock()
	now := time.Now()
	// Lazy GC of expired entries so the map does not grow unbounded.
	for k, t := range cc.recentSelfSet {
		if now.Sub(t) > 5*time.Second {
			delete(cc.recentSelfSet, k)
		}
	}
	if len(cc.recentSelfSet) == 0 {
		return paths
	}
	out := paths[:0]
	for _, p := range paths {
		if t, hit := cc.recentSelfSet[p]; hit && now.Sub(t) <= 5*time.Second {
			cc.LogChan <- fmt.Sprintf("self-set guard: dropping echo %s (age %v)", p, now.Sub(t))
			continue
		}
		out = append(out, p)
	}
	return out
}

// SetFileReceivedHook installs a callback that runs after a remote file has
// been fully received, validated, and written to the temp directory. The
// hook is responsible for putting the file on the OS clipboard and (if
// AutoPaste is enabled) simulating Ctrl+V so the focused application
// receives the file as a normal paste.
func (cc *CrossClipboard) SetFileReceivedHook(hook func(path string, meta *filetransfer.FileMeta)) {
	cc.fileReceivedHook = hook
}

// StartFileWatcher launches the goroutine that watches the OS clipboard
// for file URIs (set by the user's file manager "Copy" action) and
// pushes each new file to every connected peer. It is a no-op when the
// OS-level watcher reports unavailable (missing xclip/xdotool on Linux,
// or PowerShell on Windows).
func (cc *CrossClipboard) StartFileWatcher() {
	if cc.fileWatcher == nil {
		return
	}
	go cc.runFileWatcher()
}

// AutoPaste reports whether the receiver should simulate Ctrl+V after a
// file arrives. Honored by the fileReceivedHook installed in main().
func (cc *CrossClipboard) AutoPaste() bool {
	return cc.Config.AutoPaste
}

// runFileWatcher is the long-lived goroutine that bridges the OS clipboard
// file-URI watcher to the per-device file senders.
func (cc *CrossClipboard) runFileWatcher() {
	dedup := filetransfer.NewDedup(5 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := cc.fileWatcher.Watch(ctx)
	for paths := range ch {
		// Drop paths we just put on the OS clipboard ourselves; they are
		// not a user action and would cause a peer echo loop.
		paths = cc.filterSelfSet(paths)
		if len(paths) == 0 {
			continue
		}
		if len(paths) > 0 {
			cc.ClipboardManager.SetFileClipboardActive(true)
			go func() {
				time.Sleep(30 * time.Second)
				cc.ClipboardManager.SetFileClipboardActive(false)
			}()
		}
		cc.dispatchFileURIs(paths, dedup)
	}
}

// dispatchFileURIs fans a slice of file paths out to every connected peer.
// The 5s dedup window protects against the receive-then-paste cycle: when
// the receiver writes the file to its own clipboard and the watcher fires
// again, the second emit is suppressed.
func (cc *CrossClipboard) dispatchFileURIs(paths []string, dedup *filetransfer.Dedup) {
	cc.LogChan <- fmt.Sprintf("dispatchFileURIs: %d paths %v", len(paths), paths)
	if len(paths) == 0 {
		return
	}
	// Snapshot the connected peers under the device manager read lock
	// to avoid racing with peer connect/disconnect.
	peers := cc.snapshotConnectedPeers()
	cc.LogChan <- fmt.Sprintf("dispatchFileURIs: %d peers", len(peers))
	if len(peers) == 0 {
		// Wait up to 5s for the P2P handshake to complete. Handles the
		// race where the user copies a file right after launching the
		// binary, before mDNS discover + stream handshake finished.
		for i := 0; i < 25 && len(peers) == 0; i++ {
			time.Sleep(200 * time.Millisecond)
			peers = cc.snapshotConnectedPeers()
		}
		cc.LogChan <- fmt.Sprintf("dispatchFileURIs: after wait %d peers", len(peers))
		if len(peers) == 0 {
			return
		}
	}
	for _, srcPath := range paths {
		for _, dv := range peers {
			dv := dv
			cc.LogChan <- fmt.Sprintf("dispatchFileURIs: queuing %s -> %s status=%d", srcPath, shortID(dv.AddressInfo.ID.String()), dv.Status)
			go cc.sendOneFile(dv, srcPath, dedup)
		}
	}
}

func (cc *CrossClipboard) sendOneFile(dv *device.Device, srcPath string, dedup *filetransfer.Dedup) {
	if dv.Status != device.StatusConnected {
		return
	}
	if dv.PgpEncrypter == nil {
		// We need a PGP encrypter for any future path that wants to encrypt
		// the metadata; the file channel itself does not PGP-encrypt the
		// payload but the channel is a peer-trust channel and we want the
		// PGP key in place to be safe.
		cc.ErrorChan <- xerror.NewRuntimeErrorf("no pgp encrypter for %s, skipping file send", dv.AddressInfo.ID)
		return
	}
	progress := func(sent, total int64) {
		if total <= 0 {
			return
		}
		pct := sent * 100 / total
		cc.LogChan <- fmt.Sprintf("sending %s to %s: %d/%d bytes (%d%%)", filepath.Base(srcPath), shortID(dv.AddressInfo.ID.String()), sent, total, pct)
	}
	logf := func(s string) { cc.LogChan <- s }
	errf := func(e error) { cc.ErrorChan <- e }
	if err := filetransfer.SendFile(dv, srcPath, dedup, logf, errf, progress); err != nil {
		cc.ErrorChan <- xerror.NewRuntimeErrorf("file send to %s failed: %v", dv.AddressInfo.ID, err)
	}
}

func (cc *CrossClipboard) snapshotConnectedPeers() []*device.Device {
	cc.DeviceManager.RLock()
	defer cc.DeviceManager.RUnlock()
	out := make([]*device.Device, 0, len(cc.DeviceManager.Devices))
	for _, dv := range cc.DeviceManager.Devices {
		if dv.Status == device.StatusConnected {
			out = append(out, dv)
		}
	}
	return out
}

// shortID returns the first 8 chars of a peer ID for log lines.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
