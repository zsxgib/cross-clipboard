package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/crossclipboard"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/clipboardfile"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
	"github.com/ntsd/cross-clipboard/ui"
)

func main() {
	isTerminalMode := flag.Bool("t", false, "run in terminal mode")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	crossClipboard, err := crossclipboard.NewCrossClipboard(cfg)
	if err != nil {
		log.Fatal(err)
	}

	// File clipboard bridge: when a remote file arrives, put it on the OS
	// clipboard as a file URI list, then simulate Ctrl+V if AutoPaste is on.
	crossClipboard.SetFileReceivedHook(func(path string, meta *filetransfer.FileMeta) {
		handleIncomingFile(path, meta, crossClipboard.AutoPaste())
	})
	crossClipboard.StartFileWatcher()

	if isTerminalMode != nil && *isTerminalMode {
		exitSignal := make(chan os.Signal, 1)
		signal.Notify(exitSignal, os.Interrupt)

		for {
			select {
			case l := <-crossClipboard.LogChan:
				log.Println("log: ", l)
			case err := <-crossClipboard.ErrorChan:
				var fatalErr *xerror.FatalError
				if errors.As(err, &fatalErr) {
					log.Fatal(fmt.Errorf("fatal error: %w", fatalErr))
				}
				log.Println(fmt.Errorf("runtime error: %w", err))
			case <-crossClipboard.ClipboardManager.ClipboardsHistoryUpdated:
				// log.Printf("clipboard history updated, history size %d", len(crossClipboard.ClipboardManager.ClipboardsHistory))
			case <-crossClipboard.DeviceManager.DevicesUpdated:
				for _, dv := range crossClipboard.DeviceManager.Devices {
					if dv.Status == device.StatusPending {
						fmt.Printf("device %s wanted to connect (Y/n)", dv.Name)
						var input string
						fmt.Scanln(&input)
						if input == "n" {
							dv.Block()
						} else {
							err = dv.Trust()
							if err != nil {
								log.Println(fmt.Errorf("can not trust device: %w", err))
							}
						}
						crossClipboard.DeviceManager.UpdateDevice(dv)
					}
				}
			case exit := <-exitSignal:
				log.Printf("got %s signal. aborting...\n", exit)
				err := crossClipboard.Stop()
				if err != nil {
					log.Panicln(fmt.Errorf("error to graceful eixt: %w", err))
				}
				os.Exit(0)
			}
		}
	} else {
		view := ui.NewView(crossClipboard)
		view.Start()
	}
}

func handleIncomingFile(path string, meta *filetransfer.FileMeta, autoPaste bool) {
	fc := clipboardfile.New()
	if !fc.Available() {
		log.Printf("file received but OS file clipboard unavailable: %s at %s", meta.Name, path)
		return
	}
	if err := fc.Set([]string{path}); err != nil {
		log.Printf("failed to put %s on OS clipboard: %v", meta.Name, err)
		return
	}
	log.Printf("file ready on clipboard: %s (%d bytes) at %s", meta.Name, meta.Size, path)
	if !autoPaste {
		log.Printf("auto-paste disabled; leaving %s on the clipboard", meta.Name)
		return
	}
	if err := fc.Paste(); err != nil {
		log.Printf("failed to simulate Ctrl+V for %s: %v", meta.Name, err)
	}
}
