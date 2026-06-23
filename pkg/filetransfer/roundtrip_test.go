package filetransfer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readFrame reads a single |int64 size|byte type|payload| frame.
func readFrame(r io.Reader) (byte, []byte, error) {
	var sizeBuf [8]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return 0, nil, err
	}
	var size int64
	for i := 0; i < 8; i++ {
		size |= int64(sizeBuf[i]) << (8 * i)
	}
	if size < 1 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	header := make([]byte, 1)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, size-1)
	if size > 1 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return header[0], payload, nil
}

func writeFrameTest(w *bufio.Writer, typeByte byte, payload []byte) error {
	size := int64(len(payload) + 1)
	var sizeBuf [8]byte
	for i := 0; i < 8; i++ {
		sizeBuf[i] = byte(size >> (8 * i))
	}
	if _, err := w.Write(sizeBuf[:]); err != nil {
		return err
	}
	if err := w.WriteByte(typeByte); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func TestRoundtripSmallFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	want := bytes.Repeat([]byte("Hello, world!\n"), 100) // ~1.4 KB
	if err := os.WriteFile(srcPath, want, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(want)
	wantSHA := hex.EncodeToString(sum[:])

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	sendBuf := bufio.NewWriter(pw)

	// drive sender
	meta, err := os.Stat(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	m := &FileMeta{
		ID:     "0123456789abcdef0123456789abcdef",
		Name:   "src.bin",
		Size:   meta.Size(),
		SHA256: wantSHA,
		MTime:  meta.ModTime().Unix(),
	}
	mb, _ := MarshalFileMeta(m)
	if err := writeFrameTest(sendBuf, DataTypeFileMeta, mb); err != nil {
		t.Fatal(err)
	}
	for off := 0; off < len(want); off += ChunkSize {
		end := off + ChunkSize
		if end > len(want) {
			end = len(want)
		}
		if err := writeFrameTest(sendBuf, DataTypeFileChunk, want[off:end]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFrameTest(sendBuf, DataTypeFileEnd, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	sendBuf.Flush()
	pw.Close()

	// drive receiver
	recvDir := filepath.Join(dir, "recv")
	rs := NewReceiveState(recvDir)
	for {
		tb, payload, rerr := readFrame(pr)
		if rerr == io.EOF {
			t.Fatal("EOF before completion")
		}
		if rerr != nil {
			t.Fatal(rerr)
		}
		done, res := rs.HandleFrame(tb, payload)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if done {
			if res.FinalPath == "" {
				t.Fatal("done but no FinalPath")
			}
			break
		}
	}

	got, err := os.ReadFile(rs.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("content mismatch: len got=%d want=%d", len(got), len(want))
	}
}

func TestRoundtripLargerFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "big.bin")
	// 200 KiB: exercises multi-chunk without overflowing os.Pipe.
	want := bytes.Repeat([]byte("ABCDEFGHIJ"), 20*1024)
	if err := os.WriteFile(srcPath, want, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(want)
	wantSHA := hex.EncodeToString(sum[:])

	// Use a bytes.Buffer as the wire so sender and receiver run sequentially
	// without the os.Pipe 64KiB backpressure deadlock. The protocol under
	// test is identical.
	var wire bytes.Buffer
	sendBuf := bufio.NewWriter(&wire)

	info, _ := os.Stat(srcPath)
	m := &FileMeta{
		ID:     "abcdefabcdefabcdefabcdefabcdefab",
		Name:   "big.bin",
		Size:   info.Size(),
		SHA256: wantSHA,
		MTime:  info.ModTime().Unix(),
	}
	mb, _ := MarshalFileMeta(m)
	if err := writeFrameTest(sendBuf, DataTypeFileMeta, mb); err != nil {
		t.Fatal(err)
	}
	for off := 0; off < len(want); off += ChunkSize {
		end := off + ChunkSize
		if end > len(want) {
			end = len(want)
		}
		if err := writeFrameTest(sendBuf, DataTypeFileChunk, want[off:end]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFrameTest(sendBuf, DataTypeFileEnd, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	sendBuf.Flush()

	rs := NewReceiveState(filepath.Join(dir, "recv"))
	rdr := bytes.NewReader(wire.Bytes())
	for {
		tb, payload, rerr := readFrame(rdr)
		if rerr == io.EOF {
			t.Fatal("EOF early")
		}
		done, res := rs.HandleFrame(tb, payload)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if done {
			break
		}
	}
	got, _ := os.ReadFile(rs.path)
	if !bytes.Equal(got, want) {
		t.Errorf("content mismatch: len got=%d want=%d", len(got), len(want))
	}
}

func TestRoundtripShaMismatch(t *testing.T) {
	dir := t.TempDir()
	pr, pw, _ := os.Pipe()
	defer pr.Close()
	defer pw.Close()
	sendBuf := bufio.NewWriter(pw)

	// claim a fake sha that won't match
	m := &FileMeta{
		ID:     "00000000000000000000000000000000",
		Name:   "bad.bin",
		Size:   5,
		SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		MTime:  0,
	}
	mb, _ := MarshalFileMeta(m)
	writeFrameTest(sendBuf, DataTypeFileMeta, mb)
	writeFrameTest(sendBuf, DataTypeFileChunk, []byte("hello"))
	writeFrameTest(sendBuf, DataTypeFileEnd, []byte("ok"))
	sendBuf.Flush()
	pw.Close()

	rs := NewReceiveState(filepath.Join(dir, "recv"))
	for {
		tb, payload, rerr := readFrame(pr)
		if rerr == io.EOF {
			t.Fatal("expected sha mismatch error")
		}
		_, res := rs.HandleFrame(tb, payload)
		if res.Err != nil {
			if !contains(res.Err.Error(), "sha256 mismatch") {
				t.Fatalf("expected sha256 mismatch, got %v", res.Err)
			}
			// file should be deleted
			if _, err := os.Stat(rs.path); !os.IsNotExist(err) {
				t.Errorf("expected file to be deleted after sha mismatch")
			}
			return
		}
	}
}

func TestDedupLoopPrevention(t *testing.T) {
	d := NewDedup(5 * time.Second)
	if d.Touch("aabb") {
		t.Error("first touch should not be dedup")
	}
	if !d.Touch("aabb") {
		t.Error("second touch within window should dedup")
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
