package devicemanager

import (
	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/device"
)

type DeviceManager struct {
	Devices        map[string]*device.Device
	DevicesUpdated chan struct{}

	config *config.Config
}

func NewDeviceManager(cfg *config.Config) *DeviceManager {
	return &DeviceManager{
		Devices:        make(map[string]*device.Device),
		DevicesUpdated: make(chan struct{}),
		config:         cfg,
	}
}

func (dm *DeviceManager) AddDevice(device *device.Device) {
	dm.Devices[device.AddressInfo.ID.String()] = device
	dm.DevicesUpdated <- struct{}{}
}

func (dm *DeviceManager) RemoveDevice(device *device.Device) {
	// Flush and close ignore error
	device.Writer.Flush()
	device.Stream.Close()
	delete(dm.Devices, device.AddressInfo.ID.String())
	dm.DevicesUpdated <- struct{}{}
}

func (dm *DeviceManager) GetDevice(id string) *device.Device {
	return dm.Devices[id]
}

func (dm *DeviceManager) UpdateDevice(dv *device.Device) {
	dm.Devices[dv.AddressInfo.ID.String()] = dv

	// Clean up stale entries: when a device connects with a known identity,
	// remove old entries for the same physical machine (by name+OS) and also
	// remove entries with empty name from failed handshakes.
	if dv.Status == device.StatusConnected && dv.Name != "" {
		for id, other := range dm.Devices {
			if id == dv.AddressInfo.ID.String() {
				continue
			}
			isStale := other.Status == device.StatusDisconnected ||
				other.Status == device.StatusError ||
				other.Status == ""
			if !isStale {
				continue
			}
			if (other.Name == dv.Name && other.OS == dv.OS) || other.Name == "" {
				delete(dm.Devices, id)
			}
		}
	}

	dm.DevicesUpdated <- struct{}{}
	dm.Save()
}
