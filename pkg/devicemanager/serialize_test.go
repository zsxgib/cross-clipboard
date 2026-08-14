package devicemanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/device"
)

func writeDevicesFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, devicesFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write devices file: %v", err)
	}
}

func newTestManager(dir string) *DeviceManager {
	dm := NewDeviceManager(&config.Config{ConfigDirPath: dir})
	// Load() sends on the unbuffered DevicesUpdated channel when the file is
	// non-empty; consume it so Load does not block.
	go func() {
		for range dm.DevicesUpdated {
		}
	}()
	return dm
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		fileExist bool
		content   string
		wantErr   bool
		wantLen   int
	}{
		{name: "file not exist", fileExist: false, wantErr: false, wantLen: 0},
		{name: "empty file", fileExist: true, content: "", wantErr: false, wantLen: 0},
		{name: "whitespace only", fileExist: true, content: " \n\t", wantErr: false, wantLen: 0},
		{name: "null json", fileExist: true, content: "null", wantErr: false, wantLen: 0},
		{name: "empty object", fileExist: true, content: "{}", wantErr: false, wantLen: 0},
		{name: "blocked device", fileExist: true, content: `{"id":{"os":"windows","name":"pc","status":"blocked"}}`, wantErr: false, wantLen: 1},
		{name: "broken public key", fileExist: true, content: `{"id":{"os":"windows","name":"pc","publicKey":"aGVsbG8=","status":""}}`, wantErr: false, wantLen: 0},
		{name: "invalid json", fileExist: true, content: "{invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.fileExist {
				writeDevicesFile(t, dir, tt.content)
			}

			dm := newTestManager(dir)
			err := dm.Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if dm.Devices == nil {
				t.Fatalf("Load() devices = nil, want non-nil map")
			}
			if got := len(dm.Devices); got != tt.wantLen {
				t.Fatalf("Load() devices len = %d, want %d", got, tt.wantLen)
			}
		})
	}
}

func TestSaveSkipsIncompleteHandshake(t *testing.T) {
	dir := t.TempDir()
	dm := newTestManager(dir)

	// A completed device (has name) plus an interrupted-handshake record
	// (empty name, nil public key).
	dm.Devices["complete"] = &device.Device{Name: "pc", OS: "windows"}
	dm.Devices["incomplete"] = &device.Device{}

	if err := dm.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, devicesFileName))
	if err != nil {
		t.Fatalf("read devices file: %v", err)
	}
	var saved map[string]*device.Device
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved file: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved devices = %d, want 1", len(saved))
	}
	if _, ok := saved["incomplete"]; ok {
		t.Fatalf("incomplete-handshake device should not be persisted")
	}
}
