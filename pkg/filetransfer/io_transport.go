package filetransfer

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/ntsd/cross-clipboard/pkg/protobuf"
	"google.golang.org/protobuf/proto"
)

// DataTypeFileMessage is the type byte for a file-transfer frame, following the
// stream package's DataType* convention.
const DataTypeFileMessage byte = 0xF9

const sizeFieldLen = 8 // matches pkg/stream dataSizeLength (int64, little-endian)

// IOTransport implements Transport over a raw io.Reader/io.Writer pair. Each
// Message is framed as |size(8B LE)|type(1B)|protobuf.Message|, consistent with
// the stream package's wire format. It is libp2p-free so it can be unit-tested
// with io.Pipe and reused over any reliable stream.
type IOTransport struct {
	r  *bufio.Reader
	w  *bufio.Writer
	mu sync.Mutex // serialize concurrent writes
}

// NewIOTransport wraps an io.Reader/io.Writer pair into a Transport.
func NewIOTransport(r io.Reader, w io.Writer) *IOTransport {
	return &IOTransport{r: bufio.NewReader(r), w: bufio.NewWriter(w)}
}

// SendMessage marshals and frames a Message onto the writer.
func (t *IOTransport) SendMessage(msg *protobuf.Message) error {
	payload, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var sz [sizeFieldLen]byte
	binary.LittleEndian.PutUint64(sz[:], uint64(len(payload)+1)) // +1 for the type byte
	if _, err := t.w.Write(sz[:]); err != nil {
		return err
	}
	if err := t.w.WriteByte(DataTypeFileMessage); err != nil {
		return err
	}
	if _, err := t.w.Write(payload); err != nil {
		return err
	}
	return t.w.Flush()
}

// ReceiveMessage reads and decodes the next framed Message from the reader.
// Note: like the stream package's reader, this blocks on the underlying read
// until a full frame or an error arrives.
func (t *IOTransport) ReceiveMessage(ctx context.Context) (*protobuf.Message, error) {
	var sz [sizeFieldLen]byte
	if _, err := io.ReadFull(t.r, sz[:]); err != nil {
		return nil, err
	}
	size := int(binary.LittleEndian.Uint64(sz[:]))
	if size < 1 {
		return nil, fmt.Errorf("invalid frame size: %d", size)
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(t.r, frame); err != nil {
		return nil, err
	}
	// frame[0] is the type byte (DataTypeFileMessage); frame[1:] is the payload.
	msg := &protobuf.Message{}
	if err := proto.Unmarshal(frame[1:], msg); err != nil {
		return nil, err
	}
	return msg, nil
}
