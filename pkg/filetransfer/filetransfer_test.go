package filetransfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/protobuf"
)

// pipeTransport is an in-memory Transport backed by two crossed channels,
// simulating a bidirectional peer connection for unit tests.
type pipeTransport struct {
	out chan<- *protobuf.Message
	in  <-chan *protobuf.Message
}

func (p pipeTransport) SendMessage(msg *protobuf.Message) error {
	p.out <- msg
	return nil
}

func (p pipeTransport) ReceiveMessage(ctx context.Context) (*protobuf.Message, error) {
	select {
	case m := <-p.in:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newPipe() (Transport, Transport) {
	s2r := make(chan *protobuf.Message, 8)
	r2s := make(chan *protobuf.Message, 8)
	return pipeTransport{out: s2r, in: r2s}, pipeTransport{out: r2s, in: s2r}
}

// testPGP generates a PGP keypair and returns (peerEncrypter, localDecrypter).
func testPGP(t *testing.T) (*crypto.PGPEncrypter, *crypto.PGPDecrypter) {
	t.Helper()
	armored, err := crypto.GeneratePGPKey("test")
	if err != nil {
		t.Fatalf("generate pgp key: %v", err)
	}
	priv, err := crypto.UnmarshalPGPKey(armored, nil)
	if err != nil {
		t.Fatalf("unmarshal pgp key: %v", err)
	}
	pubBytes, err := priv.GetPublicKey()
	if err != nil {
		t.Fatalf("get public key: %v", err)
	}
	pub, err := crypto.ByteToPGPKey(pubBytes)
	if err != nil {
		t.Fatalf("byte to pgp key: %v", err)
	}
	enc, err := crypto.NewPGPEncrypter(pub)
	if err != nil {
		t.Fatalf("new pgp encrypter: %v", err)
	}
	dec, err := crypto.NewPGPDecrypter(priv)
	if err != nil {
		t.Fatalf("new pgp decrypter: %v", err)
	}
	return enc, dec
}

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "src-*.bin")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f.Close()
	return f.Name()
}

// runRoundTrip sends src via sender and receives via receiver, returning the
// received file path. receiver runs in a goroutine.
func runRoundTrip(t *testing.T, src string, relativePath string, enc *crypto.PGPEncrypter, dec *crypto.PGPDecrypter, opts ReceiveOptions, onAccept func(*protobuf.MetaData) bool) string {
	t.Helper()
	senderT, receiverT := newPipe()
	destDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type res struct {
		r   *FileResult
		err error
	}
	rc := make(chan res, 1)
	go func() {
		r, err := ReceiveFile(ctx, receiverT, dec, opts, destDir, onAccept)
		rc <- res{r, err}
	}()

	if err := SendFile(ctx, senderT, src, relativePath, enc, ChunkSize, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := <-rc
	if got.err != nil {
		t.Fatalf("receive: %v", got.err)
	}
	return got.r.Path
}

func TestAESGCMRoundTrip(t *testing.T) {
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatal(err)
	}
	plain := randBytes(t, 4096)
	ct, err := EncryptChunk(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptChunk(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, got) {
		t.Fatal("aes round trip mismatch")
	}
}

func TestAESGCMTamper(t *testing.T) {
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatal(err)
	}
	ct, err := EncryptChunk(key, randBytes(t, 256))
	if err != nil {
		t.Fatal(err)
	}
	ct[12] ^= 0xff // flip a ciphertext byte (after the 12-byte IV)
	if _, err := DecryptChunk(key, ct); err == nil {
		t.Fatal("expected aes-gcm auth failure, got nil")
	}
}

func TestPGPKeyWrapUnwrap(t *testing.T) {
	enc, dec := testPGP(t)
	aesKey, err := GenerateAESKey()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := WrapAESKey(enc, aesKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapAESKey(dec, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aesKey, got) {
		t.Fatal("pgp wrap/unwrap mismatch")
	}
}

func TestSendReceiveEncrypted(t *testing.T) {
	enc, dec := testPGP(t)
	src := writeTempFile(t, randBytes(t, 100*1024)) // 4 chunks
	dst := runRoundTrip(t, src, "", enc, dec, DefaultReceiveOptions(), nil)

	orig, _ := os.ReadFile(src)
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(orig, got) {
		t.Fatal("encrypted transfer content mismatch")
	}
	if filepath.Base(dst) != filepath.Base(src) {
		t.Fatalf("name mismatch: %s != %s", filepath.Base(dst), filepath.Base(src))
	}
}

func TestSendReceivePlain(t *testing.T) {
	src := writeTempFile(t, randBytes(t, 5000)) // 1 chunk
	dst := runRoundTrip(t, src, "", nil, nil, DefaultReceiveOptions(), nil)
	orig, _ := os.ReadFile(src)
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(orig, got) {
		t.Fatal("plain transfer content mismatch")
	}
}

func TestSendReceiveReject(t *testing.T) {
	enc, dec := testPGP(t)
	src := writeTempFile(t, randBytes(t, 1000))
	senderT, receiverT := newPipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rc := make(chan error, 1)
	go func() {
		_, err := ReceiveFile(ctx, receiverT, dec, ReceiveOptions{AutoAccept: false, MaxSize: 1 << 30}, t.TempDir(), func(*protobuf.MetaData) bool { return false })
		rc <- err
	}()
	err := SendFile(ctx, senderT, src, "", enc, ChunkSize, nil)
	if err == nil {
		t.Fatal("expected sender to fail on reject")
	}
	if recvErr := <-rc; recvErr == nil {
		t.Fatal("expected receiver to report rejection")
	}
}

func TestSendReceiveMaxSize(t *testing.T) {
	enc, dec := testPGP(t)
	src := writeTempFile(t, randBytes(t, 100*1024)) // 100KB
	senderT, receiverT := newPipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rc := make(chan error, 1)
	go func() {
		_, err := ReceiveFile(ctx, receiverT, dec, ReceiveOptions{AutoAccept: true, MaxSize: 1024}, t.TempDir(), nil) // 1KB max
		rc <- err
	}()
	err := SendFile(ctx, senderT, src, "", enc, ChunkSize, nil)
	if err == nil {
		t.Fatal("expected sender to fail on size validation")
	}
	if recvErr := <-rc; recvErr == nil {
		t.Fatal("expected receiver to reject oversized file")
	}
}

func TestSafeFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"file.txt", "file.txt"},
		{"../../etc/passwd", "passwd"},
		{"/abs/path/to/file.bin", "file.bin"},
		{"..", "file"},
		{"", "file"},
	}
	for _, c := range cases {
		if got := safeFileName(c.in); got != c.want {
			t.Errorf("safeFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSafeFilePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{".", ""},
		{"file.txt", "file.txt"},
		{"subdir/file.txt", "subdir/file.txt"},
		{"subdir/deep/file.txt", "subdir/deep/file.txt"},
		{"../escape.txt", ""},
		{"..", ""},
		{"foo/../bar.txt", "bar.txt"},
		{"/abs/path.txt", ""},
		{"./file.txt", "file.txt"},
	}
	for _, c := range cases {
		if got := safeFilePath(c.in); got != c.want {
			t.Errorf("safeFilePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSendReceiveWithRelativePath(t *testing.T) {
	enc, dec := testPGP(t)
	src := writeTempFile(t, randBytes(t, 5000))
	relPath := "subdir/deep/file.bin"
	dst := runRoundTrip(t, src, relPath, enc, dec, DefaultReceiveOptions(), nil)

	orig, _ := os.ReadFile(src)
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(orig, got) {
		t.Fatal("relative-path transfer content mismatch")
	}
	if filepath.Base(dst) != "file.bin" {
		t.Fatalf("base name mismatch: %s", filepath.Base(dst))
	}
	if filepath.Base(filepath.Dir(dst)) != "deep" {
		t.Fatalf("parent dir mismatch: %s", filepath.Base(filepath.Dir(dst)))
	}
	if filepath.Base(filepath.Dir(filepath.Dir(dst))) != "subdir" {
		t.Fatalf("grandparent dir mismatch: %s", filepath.Base(filepath.Dir(filepath.Dir(dst))))
	}
}
