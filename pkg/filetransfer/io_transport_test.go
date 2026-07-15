package filetransfer

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/protobuf"
)

func TestIOTransportRoundTrip(t *testing.T) {
	pr, pw := io.Pipe()
	// writer side wraps pw; reader side wraps pr.
	wt := NewIOTransport(nil, pw) // send-only
	rt := NewIOTransport(pr, nil) // receive-only

	msgs := []*protobuf.Message{
		{Id: "a", Data: &protobuf.Message_MetaData{MetaData: &protobuf.MetaData{Name: "f.bin", Size: 42, Type: "application/octet-stream", Key: []byte("k")}}},
		{Id: "a", Data: &protobuf.Message_Chunk{Chunk: []byte("hello")}},
		{Id: "a", Data: &protobuf.Message_ReceiveEvent{ReceiveEvent: protobuf.ReceiveEvent_EVENT_RECEIVED_CHUNK}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for _, m := range msgs {
			if err := wt.SendMessage(m); err != nil {
				t.Errorf("send: %v", err)
				return
			}
		}
	}()

	for _, want := range msgs {
		got, err := rt.ReceiveMessage(ctx)
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		if got.Id != want.Id {
			t.Fatalf("id mismatch: %s != %s", got.Id, want.Id)
		}
		switch w := want.Data.(type) {
		case *protobuf.Message_MetaData:
			g, ok := got.Data.(*protobuf.Message_MetaData)
			if !ok {
				t.Fatalf("want MetaData, got %T", got.Data)
			}
			if g.MetaData.Name != w.MetaData.Name || g.MetaData.Size != w.MetaData.Size {
				t.Fatalf("metadata mismatch: %+v != %+v", g.MetaData, w.MetaData)
			}
		case *protobuf.Message_Chunk:
			g, ok := got.Data.(*protobuf.Message_Chunk)
			if !ok {
				t.Fatalf("want Chunk, got %T", got.Data)
			}
			if !bytes.Equal(g.Chunk, w.Chunk) {
				t.Fatalf("chunk mismatch")
			}
		case *protobuf.Message_ReceiveEvent:
			g, ok := got.Data.(*protobuf.Message_ReceiveEvent)
			if !ok {
				t.Fatalf("want ReceiveEvent, got %T", got.Data)
			}
			if g.ReceiveEvent != w.ReceiveEvent {
				t.Fatalf("event mismatch: %v != %v", g.ReceiveEvent, w.ReceiveEvent)
			}
		}
	}
}
