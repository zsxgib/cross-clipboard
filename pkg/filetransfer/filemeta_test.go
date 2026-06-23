package filetransfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComputeSHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ComputeSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" // sha256("hello")
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSafeFileName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"foo.txt", "foo.txt"},
		{"/etc/passwd", "passwd"},
		{"../escape", "escape"},
		{".", "file"},
		{"..", "file"},
		{"", "file"},
		{"nested/dir/x", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := SafeFileName(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDedup(t *testing.T) {
	d := NewDedup(50 * time.Millisecond)
	if d.Touch("abc") {
		t.Error("first touch should not be duplicate")
	}
	if !d.Touch("abc") {
		t.Error("immediate second touch should be duplicate")
	}
	if d.Touch("xyz") {
		t.Error("different hash should not be duplicate")
	}
	time.Sleep(70 * time.Millisecond)
	if d.Touch("abc") {
		t.Error("after window elapsed, touch should not be duplicate")
	}
}

func TestTempManagerPathFor(t *testing.T) {
	tm, err := NewTempManager(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p := tm.PathFor("deadbeef1234567890abcdef", "../../etc/passwd")
	if filepath.Base(p) != "passwd" {
		t.Errorf("base = %q, want passwd", filepath.Base(p))
	}
	if !filepath.IsAbs(p) {
		t.Errorf("not absolute: %s", p)
	}
	// must be under tm.Dir
	rel, err := filepath.Rel(tm.Dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "..") {
		t.Errorf("path %q escapes dir %q", p, tm.Dir)
	}
}

func TestTempManagerSweep(t *testing.T) {
	dir := t.TempDir()
	tm, _ := NewTempManager(dir, time.Millisecond)
	// create a file with old mtime
	old := filepath.Join(dir, "old")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(old, past, past)

	time.Sleep(20 * time.Millisecond)
	n, err := tm.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("sweep removed %d, want >= 1", n)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old dir should be removed, err = %v", err)
	}
}
