package clipboardfile

import (
	"reflect"
	"testing"
)

func TestParseURIList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"newlines", "\n\n", nil},
		{"single file", "file:///home/x/a.txt\n", []string{"/home/x/a.txt"}},
		{"multiple", "file:///a\nfile:///b\n", []string{"/a", "/b"}},
		{"mixed with spaces", "  file:///a  \n  file:///b  \n", []string{"/a", "/b"}},
		{"with comments", "# comment\nfile:///a\n", []string{"/a"}},
		{"rejects bare paths", "/etc/hostname\nfile:///a\n", []string{"/a"}},
		{"rejects http", "http://example.com/x\nfile:///a\n", []string{"/a"}},
		{"rejects text content", "hello world\nfile:///a\n", []string{"/a"}},
		{"decodes percent-encoded utf-8", "file:///home/x/%E4%B8%8B%E8%BD%BD/a.txt\n", []string{"/home/x/下载/a.txt"}},
		{"decodes percent-encoded space", "file:///home/x/My%20Files/a.txt\n", []string{"/home/x/My Files/a.txt"}},
		{"ignores bad percent encoding", "file:///home/x/%ZZ/bad\n", []string{"/home/x/%ZZ/bad"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseURIList(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSamePathSet(t *testing.T) {
	if !samePathSet([]string{"/a", "/b"}, []string{"/b", "/a"}) {
		t.Error("order should not matter")
	}
	if samePathSet([]string{"/a"}, []string{"/a", "/b"}) {
		t.Error("length differs should report different")
	}
	if samePathSet([]string{"/a"}, []string{"/x"}) {
		t.Error("content differs should report different")
	}
	if !samePathSet(nil, nil) {
		t.Error("both nil should be equal")
	}
}
