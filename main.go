package main

import (
	"runtime/debug"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"io"
	"strings"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/crossclipboard"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/clipboardfile"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("FATAL PANIC: %v\n%s", r, debug.Stack())
		}
		log.Printf("main() returning, keeping process alive for diagnostics")
		// Block forever so test harness can see logs and process state.
		select {}
	}()
	isTerminalMode := flag.Bool("t", false, "run in terminal mode")
	setFile := flag.String("set-file", "", "test helper: put this absolute file path on the OS clipboard as CF_HDROP after startup, then continue running. Used by the e2e test to simulate a user copying a file when SSH cannot reach the interactive Windows session.")
	triggerFile := flag.String("trigger-file", "", "test helper: poll this file every 500ms; when its content is a path, put it on the OS clipboard as CF_HDROP, then truncate the file. Lets an e2e test trigger a copy on a long-running daemon via a write-only side channel.")
	flag.Parse()

	// Optional: tee all log output to a file. Used by the e2e test to
	// capture Win daemon logs when the binary is launched via a
	// scheduled task that does not have a usable stderr handle.
	var logFileHandle *os.File
	if logFile := os.Getenv("CROSS_CLIPBOARD_LOG_FILE"); logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			logFileHandle = f
			log.SetOutput(io.MultiWriter(os.Stderr, f))
			log.Printf("main: log file = %s", logFile)
		}
	}
	defer func() {
		if logFileHandle != nil {
			logFileHandle.Close()
		}
	}()

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

	// Test helper: poll a trigger file for a path to put on the OS clipboard.
	if triggerFile != nil && *triggerFile != "" {
		go runTriggerWatcher(*triggerFile, crossClipboard.LogChan)
	}

	// Test helper: simulate a user copying a file by putting the given
	// path on the OS clipboard as a file drop. Lets e2e verify Win->Linux
	// when SSH cannot reach the interactive session.
	if setFile != nil && *setFile != "" {
		// PowerShell -ArgumentList double-escapes backslashes. Undo that.
		path := strings.ReplaceAll(*setFile, "\\\\", "\\")
		log.Printf("set-file: normalized path=%q", path)
		// Set synchronously BEFORE the main loop parks. Empirically
		// anything in a goroutine (go func, time.AfterFunc, time.Sleep)
		// while main is in `for { select {} }` polling libp2p host
		// channels is starved: we observed timers never fire and
		// goroutines never run their second line. Setting the clipboard
		// up front (within the first 200ms of startup) avoids the race
		// with the host channels.
		log.Printf("set-file: pre-loop Set, path=%q", path)
		fc := clipboardfile.New()
		if !fc.Available() {
			log.Printf("set-file: OS file clipboard unavailable")
		} else if err := fc.Set([]string{path}); err != nil {
			log.Printf("set-file: %v", err)
		} else {
			log.Printf("set-file: put %s on OS clipboard", path)
		}
	}

	_ = isTerminalMode // -t flag is accepted; default and -t both run log mode
	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, os.Interrupt)

	for {
		select {
		case l := <-crossClipboard.LogChan:
			log.Println("log: ", l)
		case err := <-crossClipboard.ErrorChan:
			var fatalErr *xerror.FatalError
			if errors.As(err, &fatalErr) {
				// Fatal errors are reported but the daemon keeps
				// running so the e2e test harness can inspect
				// state and the user can restart the connection.
				log.Printf("fatal error (keeping process alive): %v", fatalErr)
				continue
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
			log.Printf("got %s signal. cleanup + keep alive for diagnostics", exit)
			_ = crossClipboard.Stop()
			log.Printf("stop() returned, blocking")
			select {}
		}
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
	// Auto-paste via xdotool. On some X11 sessions (gdm, rootless,
	// gnome-shell with focus-stealing-prevention) the X server refuses
	// to deliver synthetic key events to the focused window, so this
	// keystroke is silently dropped and the user has to press Ctrl+V
	// themselves. We log the action and a hint so it's obvious when
	// the simulated paste is not effective.
	if err := fc.Paste(); err != nil {
		log.Printf("failed to simulate Ctrl+V for %s: %v", meta.Name, err)
	}
	log.Printf("(if the file did not appear in the focused window, press Ctrl+V manually \u2014 xdotool XTest may be blocked by your X server)")
}

// runTriggerWatcher polls a side-channel file for a path. When the file
// contains a non-empty path, the watcher puts that path on the OS
// clipboard as a file drop, then truncates the file. This lets an e2e
// test trigger a copy on a long-running Win daemon via a write-only
// side channel (the test never needs to drive the GUI session).
func runTriggerWatcher(triggerPath string, logChan chan string) {
	for {
		time.Sleep(500 * time.Millisecond)
		data, err := os.ReadFile(triggerPath)
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(data))
		if p == "" {
			continue
		}
		// Truncate first so concurrent reads don't see the same value.
		if err := os.WriteFile(triggerPath, nil, 0644); err != nil {
			logChan <- fmt.Sprintf("trigger: truncate %s failed: %v", triggerPath, err)
			continue
		}
		fc := clipboardfile.New()
		if !fc.Available() {
			logChan <- fmt.Sprintf("trigger: OS file clipboard unavailable")
			continue
		}
		if err := fc.Set([]string{p}); err != nil {
			logChan <- fmt.Sprintf("trigger: set %s failed: %v", p, err)
			continue
		}
		logChan <- fmt.Sprintf("trigger: put %s on OS clipboard", p)
	}
}
