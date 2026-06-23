package devicemanager

import (
	"sync"

	"github.com/ntsd/cross-clipboard/pkg/config"
	"github.com/ntsd/cross-clipboard/pkg/device"
)

type DeviceManager struct {
	Devices        map[string]*device.Device
	DevicesUpdated chan struct{}
	config        *config.Config
	mu            sync.RWMutex
}

func NewDeviceManager(cfg *config.Config) *DeviceManager {
	return &DeviceManager{
		Devices:        make(map[string]*device.Device),
		DevicesUpdated: make(chan struct{}),
		config:         cfg,
	}
}

// RLock acquires a read lock on the device map so callers can safely
// snapshot the connected peer list while peers are being added/updated.
func (dm *DeviceManager) RLock()   { dm.mu.RLock() }
func (dm *DeviceManager) RUnlock() { dm.mu.RUnlock() }

func (dm *DeviceManager) AddDevice(device *device.Device) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.Devices[device.AddressInfo.ID.String()] = device
	dm.DevicesUpdated <- struct{}{}
}

func (dm *DeviceManager) RemoveDevice(device *device.Device) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	// Flush and close ignore error
	device.Writer.Flush()
	device.Stream.Close()
	delete(dm.Devices, device.AddressInfo.ID.String())
	dm.DevicesUpdated <- struct{}{}
}

func (dm *DeviceManager) GetDevice(id string) *device.Device {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.Devices[id]
}

func (dm *DeviceManager) UpdateDevice(device *device.Device) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.Devices[device.AddressInfo.ID.String()] = device
	dm.DevicesUpdated <- struct{}{}
	dm.Save()
}
