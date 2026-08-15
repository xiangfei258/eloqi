//go:build darwin && cgo

package darwin

import "github.com/xiangchang24/eloqi/internal/platform"

// Clipboard uses NSPasteboard's UTF-8 string representation.
type Clipboard struct{}

var _ platform.Clipboard = (*Clipboard)(nil)

func NewClipboard() (*Clipboard, error) {
	return &Clipboard{}, nil
}

func (c *Clipboard) Read() (string, error) {
	return nativeClipboardRead()
}

func (c *Clipboard) Write(text string) error {
	return nativeClipboardWrite(text)
}
