package stream

import (
	"bufio"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/ntsd/cross-clipboard/pkg/clipboard"
	"github.com/ntsd/cross-clipboard/pkg/device"
	"github.com/ntsd/cross-clipboard/pkg/filetransfer"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
)

const limitDataSize = 1 << 27 // 128 MiB hard cap on a single frame
const fileFrameSizeLimit = 1 << 30 // 1 GiB hard cap for a single file frame

// CreateReadData craete a new read streaming for host or peer
func (s *StreamHandler) CreateReadData(reader *bufio.Reader, dv *device.Device) {
	s.logChan <- fmt.Sprintf("sending device info and public key to %s", dv.AddressInfo.ID)

	s.sendDeviceData(dv)

	// file receive state, lazily initialized on first FileMeta frame
	var fileState *filetransfer.ReceiveState
	if s.fileTempDir != "" {
		fileState = filetransfer.NewReceiveState(s.fileTempDir)
	}

	// loop for incoming message
disconnect:
	for {
		dataSize, err := readDataSize(reader)
		if err != nil {
			if err == network.ErrReset { // error stream reset because it unusual stream end
				s.errorChan <- xerror.NewRuntimeErrorf("peer %s stream reset", dv.AddressInfo.ID).Wrap(err)
				dv.Status = device.StatusDisconnected
				s.deviceManager.UpdateDevice(dv)
				break disconnect
			}

			s.errorChan <- xerror.NewRuntimeError("error reading data size").Wrap(err)
			dv.Status = device.StatusError
			s.deviceManager.UpdateDevice(dv)
			break disconnect
		}

		if dataSize <= 0 {
			s.errorChan <- xerror.NewRuntimeErrorf("data size < 0, size %d", dataSize)
			dv.Status = device.StatusError
			s.deviceManager.UpdateDevice(dv)
			break disconnect
		}

		// Read the data type first so we can route file frames to a path
		// that bypasses the clipboard size and PGP decryption.
		if dataSize > fileFrameSizeLimit {
			s.errorChan <- xerror.NewRuntimeErrorf("data size %d > file frame size limit %d", dataSize, fileFrameSizeLimit)
			dv.Status = device.StatusBlocked
			s.deviceManager.UpdateDevice(dv)
			break disconnect
		}
		header := make([]byte, 1)
		if _, err := io.ReadFull(reader, header); err != nil {
			s.errorChan <- xerror.NewRuntimeError("error reading data type").Wrap(err)
			dv.Status = device.StatusError
			s.deviceManager.UpdateDevice(dv)
			break disconnect
		}
		dataType := header[0]

		// File channel: handle without PGP and without the MaxSize cap.
		if isFileDataType(dataType) {
			if fileState == nil {
				s.errorChan <- xerror.NewRuntimeError("file receive not configured on this device")
				// drop the rest of the frame
				_, _ = reader.Discard(dataSize - 1)
				continue
			}
			payload := make([]byte, dataSize-1)
			if dataSize > 1 {
				if _, err := io.ReadFull(reader, payload); err != nil {
					s.errorChan <- xerror.NewRuntimeError("error reading file frame body").Wrap(err)
					dv.Status = device.StatusError
					s.deviceManager.UpdateDevice(dv)
					break disconnect
				}
			}
			done, res := fileState.HandleFrame(dataType, payload)
			if res.Err != nil {
				s.errorChan <- xerror.NewRuntimeErrorf("file receive from %s failed: %v", dv.AddressInfo.ID, res.Err)
				fileState = filetransfer.NewReceiveState(s.fileTempDir)
				continue
			}
			if done {
				s.logChan <- fmt.Sprintf("received file: %s size=%d sha=%s from %s", res.Meta.Name, res.Meta.Size, res.Meta.SHA256[:8], dv.AddressInfo.ID)
				if s.onFileReceived != nil && res.FinalPath != "" {
					s.onFileReceived(res.FinalPath, res.Meta)
				}
				fileState = filetransfer.NewReceiveState(s.fileTempDir)
			}
			continue
		}

		// Non-file frames are subject to the existing limit + PGP path.
		if dataSize > limitDataSize {
			s.errorChan <- xerror.NewRuntimeErrorf("data size %d > limit data size %d", dataSize, limitDataSize)
			dv.Status = device.StatusBlocked
			s.deviceManager.UpdateDevice(dv)
			break disconnect
		}

		// We already consumed the type byte. Read the rest and prepend it
		// for the existing decodeData which expects the type at index 0.
		rest := make([]byte, dataSize-1)
		if dataSize > 1 {
			if _, err := io.ReadFull(reader, rest); err != nil {
				s.errorChan <- xerror.NewRuntimeError("error reading frame body").Wrap(err)
				dv.Status = device.StatusError
				s.deviceManager.UpdateDevice(dv)
				break disconnect
			}
		}
		buffer := make([]byte, dataSize)
		buffer[0] = dataType
		copy(buffer[1:], rest)

		// skip clipboard size when data more than config max size
		if dataSize > s.config.MaxSize {
			s.errorChan <- xerror.NewRuntimeErrorf("data size %d > config max size %d", dataSize, s.config.MaxSize)
			continue
		}

		clipboardData, deviceData, signal, err := s.decodeData(buffer)
		if err != nil {
			s.errorChan <- xerror.NewRuntimeError("error decoding data").Wrap(err)
			dv.Status = device.StatusError
			s.deviceManager.UpdateDevice(dv)
			break disconnect
		}

		if signal != nil {
			s.logChan <- fmt.Sprintf("received signal %v, peer: %s", signal, dv.AddressInfo.ID)
			switch *signal {
			case SignalDisconnect:
				dv.Status = device.StatusDisconnected
				s.deviceManager.UpdateDevice(dv)
				break disconnect
			case SignalRequestDeviceData:
				s.sendDeviceData(dv)
			}
		}

		if clipboardData != nil {
			s.clipboardManager.WriteClipboard(clipboard.FromProtobuf(clipboardData, dv))
			s.logChan <- fmt.Sprintf("received clipboard data, peer: %s size: %d", dv.AddressInfo.ID, clipboardData.DataSize)
		}

		if deviceData != nil {
			s.logChan <- fmt.Sprintf("received device data, peer: %s", dv.AddressInfo.ID)

			s.logChan <- fmt.Sprintf("%s wanted to connect", deviceData.Name)
			dv.UpdateFromProtobuf(deviceData)

			if dv.PgpEncrypter == nil {
				if s.config.AutoTrust {
					if err := dv.Trust(); err != nil {
						s.errorChan <- xerror.NewRuntimeErrorf("auto-trust failed for %s: %v", deviceData.Name, err)
						dv.Status = device.StatusPending
					} else {
						s.logChan <- fmt.Sprintf("trusted %s by auto trust", deviceData.Name)
					}
				} else {
					dv.Status = device.StatusPending
				}
			} else {
				dv.Status = device.StatusConnected
			}

			s.deviceManager.UpdateDevice(dv)
		}
	}

	s.logChan <- fmt.Sprintf("ending read stream for peer: %s", dv.AddressInfo.ID)

	err := dv.Stream.Close()
	if err != nil {
		if err == network.ErrReset { // check stream already reset
			s.logChan <- fmt.Sprintf("peer %s stream already reset", dv.AddressInfo.ID)
		}
		s.errorChan <- fmt.Errorf("can not close stream for peer %s: %w", dv.AddressInfo.ID, err)
	}
}

func isFileDataType(t byte) bool {
	return t == byte(DataTypeFileMeta) ||
		t == byte(DataTypeFileChunk) ||
		t == byte(DataTypeFileEnd) ||
		t == byte(DataTypeFileError)
}
