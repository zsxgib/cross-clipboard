package crossclipboard

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/multiformats/go-multiaddr"
	"github.com/ntsd/cross-clipboard/pkg/clipboard"
	"github.com/ntsd/cross-clipboard/pkg/clipboardfile"
	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/devicemanager"
	"github.com/ntsd/cross-clipboard/pkg/discovery"
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

	// File transfer state.
	pgpDecrypter   *crypto.PGPDecrypter                // own PGP private key, to unwrap received AES keys
	fileTempDir    string                              // where received files are written
	onFileReceived func(path string, meta interface{}) // OS-clipboard paste hook

	// OS file clipboard (copy/paste of files). nil when unavailable.
	fileClipboard clipboardfile.FileClipboard
	fileCancel    context.CancelFunc
	recentSelfSet map[string]time.Time // paths we just put on our own clipboard (anti-echo)
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

	// resolve received-file temp dir
	fileTempDir := cc.Config.FileTempDir
	if fileTempDir == "" {
		fileTempDir = filepath.Join(cc.Config.ConfigDirPath, "incoming")
	}
	if err := os.MkdirAll(fileTempDir, 0o755); err != nil {
		return nil, xerror.NewFatalError("create file temp dir").Wrap(err)
	}
	cc.fileTempDir = fileTempDir

	ctx := context.Background()

	sourceMultiAddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", cc.Config.ListenHost, cc.Config.ListenPort))
	if err != nil {
		return nil, xerror.NewFatalError("error to multiaddr.NewMultiaddr").Wrap(err)
	}

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
	cc.pgpDecrypter = pgpDecrypter

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
		)
		cc.streamHandler = streamHandler

		cc.Host.SetStreamHandler(stream.PROTOCAL_ID, streamHandler.HandleStream)
		cc.Host.SetStreamHandler(stream.FileProtocolID, cc.handleFileStream)
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

				// Peer ID ordering to avoid TLS collision:
				// lower ID dials first, higher ID waits for incoming connection.
				if cc.Host.Network().Connectedness(peerInfo.ID) != network.Connected {
					if cc.Host.ID().String() > peerInfo.ID.String() {
						cc.LogChan <- fmt.Sprintf("waiting for peer %s to connect (higher peer ID)", peerInfo.ID)
						waitTimer := time.NewTimer(15 * time.Second)
						ticker := time.NewTicker(500 * time.Millisecond)
					waitPeer:
						for {
							select {
							case <-cc.stopDiscovery:
								ticker.Stop()
								waitTimer.Stop()
								continue discoveryLoop
							case <-waitTimer.C:
								ticker.Stop()
								cc.LogChan <- fmt.Sprintf("timeout waiting for %s, dialing anyway", peerInfo.ID)
								break waitPeer
							case <-ticker.C:
								if cc.Host.Network().Connectedness(peerInfo.ID) == network.Connected {
									ticker.Stop()
									waitTimer.Stop()
									continue discoveryLoop
								}
							}
						}
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
							time.Sleep(time.Duration(retry*2) * time.Second)
							continue
						}
						break
					}
					if retry == 5 {
						cc.ErrorChan <- xerror.NewRuntimeErrorf("error to connect to peer %s", peerInfo.ID)
						continue
					}
				} else if dv != nil && dv.Stream != nil {
					// Already connected with an active stream.
					continue
				}

				// open a stream, this stream will be handled by handleStream other end
				stream, err := cc.Host.NewStream(ctx, peerInfo.ID, stream.PROTOCAL_ID)
				if err != nil {
					cc.ErrorChan <- xerror.NewRuntimeError("new stream error").Wrap(err)
					continue
				}

				if dv == nil {
					dv = device.NewDevice(peerInfo, stream)
				} else {
					dv.AddressInfo = peerInfo
					dv.Stream = stream
					dv.Reader = bufio.NewReader(stream)
					dv.Writer = bufio.NewWriter(stream)
				}

				cc.DeviceManager.UpdateDevice(dv)
				go streamHandler.CreateReadData(dv.Reader, dv)

				cc.LogChan <- fmt.Sprintf("connected to peer host: %s", peerInfo)
			case <-cc.stopDiscovery:
				cc.LogChan <- "stop discovery peer"
				break discoveryLoop
			}
		}
	}()

	return cc, nil
}

func (cc *CrossClipboard) Stop() error {
	if cc.fileCancel != nil {
		cc.fileCancel()
	}
	if cc.streamHandler != nil {
		for id, dv := range cc.DeviceManager.Devices {
			if dv.Status == device.StatusConnected {
				log.Printf("sending disconneced signal to peer %s \n", id)
				cc.streamHandler.SendSignal(dv, stream.SignalDisconnect)
			}
		}

		time.Sleep(time.Second)

		for id, dv := range cc.DeviceManager.Devices {
			if dv.Status == device.StatusConnected {
				log.Printf("ending stream for peer %s \n", id)
				dv.Stream.Close()
			}
		}
	}

	cc.stopDiscovery <- struct{}{}

	err := cc.Host.Close()
	if err != nil {
		return xerror.NewFatalError("unable to close host").Wrap(err)
	}

	return nil
}
