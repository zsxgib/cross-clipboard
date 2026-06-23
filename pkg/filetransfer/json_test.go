package filetransfer

import (
	"strings"
	"testing"
)

func TestMarshalUnmarshalFileMeta(t *testing.T) {
	m := &FileMeta{
		ID:     "0123456789abcdef0123456789abcdef",
		Name:   "report.pdf",
		Size:   12345,
		SHA256: "deadbeef" + strings.Repeat("0", 56),
		MTime:  1700000000,
	}
	b, err := MarshalFileMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalFileMeta(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Name != m.Name || got.Size != m.Size ||
		got.SHA256 != m.SHA256 || got.MTime != m.MTime {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestFileMetaRoundTripTrickyName(t *testing.T) {
	m := &FileMeta{ID: strings.Repeat("a", 32), Name: `weird "name"\with\backslash`, Size: 1, SHA256: strings.Repeat("b", 64), MTime: 0}
	b, err := MarshalFileMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalFileMeta(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != m.Name {
		t.Errorf("name roundtrip: got %q want %q", got.Name, m.Name)
	}
}

func TestNewID(t *testing.T) {
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Errorf("id length %d, want 32", len(id))
	}
	if id == "00000000000000000000000000000000" {
		t.Error("id should not be all zeros")
	}
}
