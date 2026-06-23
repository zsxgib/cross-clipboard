package stream

import "github.com/libp2p/go-libp2p/core/protocol"

type (
	DataType byte
	Signal   byte
)

const (
	PROTOCAL_ID protocol.ID = protocol.ID("/cross-clipboard/0.0.1")

	// data type is the first byte after data size to identify the message type
	DataTypeDevice    DataType = 0xFF // use for device data
	DataTypeClipboard DataType = 0xFE // use for clipboard data

	// File transfer channel (cross-device file copy/paste).
	// Payload of DataTypeFileMeta is a FileMeta proto: {id, name, size, sha256, mtime}.
	// Payload of DataTypeFileChunk is a single raw chunk (<=64KiB) of the file
	// body. After the receiver has accumulated `size` bytes the transfer ends.
	DataTypeFileMeta  DataType = 0xF8 // file metadata header
	DataTypeFileChunk DataType = 0xF7 // file body chunk
	DataTypeFileEnd   DataType = 0xF6 // file transfer finished OK
	DataTypeFileError DataType = 0xF5 // file transfer aborted (payload = reason string)

	// signal is the first byte after data size to identify the signal type
	SignalDisconnect        Signal = 0xFD // ending exit signal
	SignalRequestDeviceData Signal = 0xFC // request device data signal
)
