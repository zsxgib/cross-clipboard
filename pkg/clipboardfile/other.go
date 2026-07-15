//go:build !linux && !windows

package clipboardfile

// unsupportedFileClipboard is a no-op used on platforms without a file
// clipboard integration (e.g. darwin).
type unsupportedFileClipboard struct{}

func New() FileClipboard { return &unsupportedFileClipboard{} }

func (unsupportedFileClipboard) Available() bool { return false }
func (u unsupportedFileClipboard) Watch(ctx interface{ Done() <-chan struct{} }) <-chan []string {
	ch := make(chan []string)
	close(ch)
	return ch
}
func (unsupportedFileClipboard) SetFiles(paths []string) error { return nil }
func (unsupportedFileClipboard) Paste() error                  { return nil }
