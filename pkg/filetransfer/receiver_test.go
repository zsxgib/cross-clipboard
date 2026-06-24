package filetransfer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func mustMarshalMeta(t *testing.T, m *FileMeta) []byte {
	t.Helper()
	b, err := MarshalFileMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile0644(t *testing.T, p string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func shaOfBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestHandleFrameFileMeta_SHAReuse: dst already has the same content
// (same SHA) as the incoming FileMeta -> reuse the path, no -<id> suffix.
func TestHandleFrameFileMeta_SHAReuse(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello world")
	sha := shaOfBytes(content)
	prefix := sha[:16]

	// Receiver will compute dst as <dir>/<prefix>/foo.txt
	prefixDir := filepath.Join(dir, prefix)
	writeFile0644(t, filepath.Join(prefixDir, "foo.txt"), content)

	m := &FileMeta{
		ID:     "deadbeef1234567890abcdef00000000",
		Name:   "foo.txt",
		Size:   int64(len(content)),
		SHA256: sha,
	}

	r := NewReceiveState(dir)
	_, res := r.HandleFrame(0xF8, mustMarshalMeta(t, m))
	if res.Err != nil {
		t.Fatalf("FileMeta err: %v", res.Err)
	}
	want := filepath.Join(prefixDir, "foo.txt")
	if r.path != want {
		t.Errorf("path = %q, want %q (reuse, no suffix)", r.path, want)
	}
	if filepath.Base(r.path) != "foo.txt" {
		t.Errorf("expected original filename, got %q", filepath.Base(r.path))
	}
}

// TestHandleFrameFileMeta_SHAConflict: dst has DIFFERENT content -> must
// suffix -<id[:6]> and not clobber the existing file.
func TestHandleFrameFileMeta_SHAConflict(t *testing.T) {
	dir := t.TempDir()
	oldContent := []byte("OLD CONTENT")
	newContent := []byte("NEW CONTENT")
	// File on disk has OLD content with its own SHA, but the receiver
	// will mkdir based on the *incoming* meta's SHA.
	oldSHA := shaOfBytes(oldContent)
	newSHA := shaOfBytes(newContent)
	prefix := newSHA[:16] // receiver uses the INCOMING meta's SHA for the dir

	prefixDir := filepath.Join(dir, prefix)
	dst := filepath.Join(prefixDir, "foo.txt")
	writeFile0644(t, dst, oldContent) // content does NOT match newSHA

	m := &FileMeta{
		ID:     "deadbeef1234567890abcdef00000000",
		Name:   "foo.txt",
		Size:   int64(len(newContent)),
		SHA256: newSHA,
	}

	r := NewReceiveState(dir)
	_, res := r.HandleFrame(0xF8, mustMarshalMeta(t, m))
	if res.Err != nil {
		t.Fatalf("FileMeta err: %v", res.Err)
	}
	if r.path == dst {
		t.Fatalf("path = %q, expected suffixed path", r.path)
	}
	if filepath.Base(r.path) == "foo.txt" {
		t.Errorf("expected -<id> suffix, got plain %q", filepath.Base(r.path))
	}
	// Existing file must be untouched.
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(oldContent) {
		t.Errorf("existing file clobbered: %q", got)
	}
	_ = oldSHA
}

// TestHandleFrameFileMeta_EmptyDir: dst path exists but is a directory ->
// treat as a clash and suffix.
func TestHandleFrameFileMeta_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello")
	sha := shaOfBytes(content)
	prefix := sha[:16]

	prefixDir := filepath.Join(dir, prefix)
	if err := os.MkdirAll(prefixDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clash := filepath.Join(prefixDir, "foo.txt")
	if err := os.MkdirAll(clash, 0o755); err != nil {
		t.Fatal(err)
	}

	m := &FileMeta{
		ID:     "deadbeef1234567890abcdef00000000",
		Name:   "foo.txt",
		Size:   int64(len(content)),
		SHA256: sha,
	}
	r := NewReceiveState(dir)
	_, res := r.HandleFrame(0xF8, mustMarshalMeta(t, m))
	if res.Err != nil {
		t.Fatalf("FileMeta err: %v", res.Err)
	}
	if r.path == clash {
		t.Errorf("path = %q, expected suffixed when dst is a directory", r.path)
	}
}
