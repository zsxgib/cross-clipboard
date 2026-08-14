package devicemanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
	"github.com/ntsd/go-utils/pkg/stringutil"
)

const devicesFileName = "devices.json"

func (dm *DeviceManager) Save() error {
	// Persist only devices with a completed handshake (non-empty name).
	// Entries saved from an interrupted handshake have no name and a nil
	// public key, and would fail to load on the next startup.
	devicesToSave := make(map[string]*device.Device, len(dm.Devices))
	for id, dv := range dm.Devices {
		if dv.Name != "" {
			devicesToSave[id] = dv
		}
	}

	b, err := json.MarshalIndent(devicesToSave, "", "  ")
	if err != nil {
		return xerror.NewRuntimeError("can not marshal devices").Wrap(err)
	}

	deviceFilePath := stringutil.JoinURL(dm.config.ConfigDirPath, devicesFileName)

	err = os.WriteFile(deviceFilePath, b, 0644)
	if err != nil {
		return xerror.NewRuntimeError("can not write devices file").Wrap(err)
	}

	return nil
}

func (dm *DeviceManager) Load() error {
	deviceFilePath := stringutil.JoinURL(dm.config.ConfigDirPath, devicesFileName)

	f, err := os.Open(deviceFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return xerror.NewRuntimeError("can not open devices file").Wrap(err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return xerror.NewRuntimeError("can not read devices file").Wrap(err)
	}

	// Treat an empty or whitespace-only file as no saved devices, so a
	// truncated/zero-byte devices.json (e.g. from an aborted write) does not
	// crash startup with an unmarshal error.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	var devices map[string]*device.Device
	err = json.Unmarshal(data, &devices)
	if err != nil {
		return xerror.NewRuntimeError("can not unmarshal devices json").Wrap(err)
	}
	// JSON "null" unmarshals into a nil map; normalize to an empty map so
	// later writes to dm.Devices do not panic on a nil map.
	if devices == nil {
		devices = make(map[string]*device.Device)
	}

	for id, dv := range devices {
		if dv.Status != device.StatusBlocked {
			dv.Status = device.StatusDisconnected
			err := dv.CreatePGPEncrypter()
			if err != nil {
				// Skip records whose public key is missing or unreadable
				// (e.g. persisted from an interrupted handshake). A broken
				// record must not block startup.
				delete(devices, id)
				continue
			}
		}
	}

	dm.Devices = devices
	dm.DevicesUpdated <- struct{}{}

	return nil
}
