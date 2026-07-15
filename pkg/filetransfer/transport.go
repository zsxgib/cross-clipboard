package filetransfer

import (
	"context"

	"github.com/ntsd/cross-clipboard/pkg/protobuf"
)

// Transport carries file-transfer Messages between two peers. It abstracts the
// libp2p stream framing (|size(4B)|type(1B)|payload|) so the sender/receiver
// can be unit-tested in memory without a real P2P connection.
type Transport interface {
	SendMessage(msg *protobuf.Message) error
	ReceiveMessage(ctx context.Context) (*protobuf.Message, error)
}
