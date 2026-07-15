package crossclipboard

import (
	"context"
	"fmt"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/clipboardfile"
	"github.com/ntsd/cross-clipboard/pkg/device"
)

// selfSetWindow is how long a path placed on our own clipboard is ignored by
// the local watcher, to break the receive->set->watch->resend echo loop.
const selfSetWindow = 5 * time.Second

// StartFileClipboard starts the OS file-clipboard watcher (send side) and
// installs the receive hook (put incoming files on the OS clipboard + optional
// paste). It is a no-op when the OS toolchain is unavailable.
func (cc *CrossClipboard) StartFileClipboard(ctx context.Context) {
	fc := clipboardfile.New()
	if !fc.Available() {
		cc.LogChan <- "file clipboard unavailable on this host; file sync disabled"
		return
	}
	cc.fileClipboard = fc

	fileCtx, cancel := context.WithCancel(ctx)
	cc.fileCancel = cancel

	// receive hook: put the received file on the OS clipboard (+ paste),
	// guarded against the local watcher echoing it back to the peer.
	cc.SetFileReceivedHook(func(path string, _ interface{}) {
		cc.markSelfSet(path)
		if err := fc.SetFiles([]string{path}); err != nil {
			cc.ErrorChan <- fmt.Errorf("set file clipboard: %w", err)
			return
		}
		if cc.Config.AutoPaste {
			time.Sleep(150 * time.Millisecond)
			if err := fc.Paste(); err != nil {
				cc.ErrorChan <- fmt.Errorf("paste file: %w", err)
			}
		}
	})

	// send side: when a file is copied locally, send it to a connected peer.
	go func() {
		for paths := range fc.Watch(fileCtx) {
			if len(paths) == 0 {
				continue
			}
			paths = cc.filterSelfSet(paths)
			if len(paths) == 0 {
				continue
			}
			cc.sendFileCopies(ctx, paths)
		}
	}()
}

// sendFileCopies sends each path to the first connected trusted device.
func (cc *CrossClipboard) sendFileCopies(ctx context.Context, paths []string) {
	var dv *device.Device
	for _, d := range cc.DeviceManager.Devices {
		if d.Status == device.StatusConnected && d.PgpEncrypter != nil {
			dv = d
			break
		}
	}
	if dv == nil {
		cc.LogChan <- "file copied but no connected trusted device to send to"
		return
	}
	for _, p := range paths {
		go func(path string) {
			cc.LogChan <- fmt.Sprintf("sending copied file %s to %s", path, dv.AddressInfo.ID)
			if err := cc.SendFileToPeer(ctx, dv, path); err != nil {
				cc.ErrorChan <- fmt.Errorf("send file %s: %w", path, err)
			}
		}(p)
	}
}

// markSelfSet records a path as just placed on our own clipboard.
func (cc *CrossClipboard) markSelfSet(path string) {
	cc.selfSetMu.Lock()
	defer cc.selfSetMu.Unlock()
	cc.recentSelfSet[path] = time.Now()
}

// filterSelfSet drops paths we placed on our own clipboard within the window.
func (cc *CrossClipboard) filterSelfSet(paths []string) []string {
	cc.selfSetMu.Lock()
	defer cc.selfSetMu.Unlock()
	now := time.Now()
	for k, t := range cc.recentSelfSet {
		if now.Sub(t) > selfSetWindow {
			delete(cc.recentSelfSet, k)
		}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if t, hit := cc.recentSelfSet[p]; hit && now.Sub(t) <= selfSetWindow {
			continue
		}
		out = append(out, p)
	}
	return out
}
