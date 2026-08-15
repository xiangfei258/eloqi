//go:build darwin && cgo

package darwin

import (
	"fmt"

	"github.com/xiangchang24/eloqi/internal/platform"
)

type Autotype struct {
	clipboard platform.Clipboard
}

var _ platform.Autotype = (*Autotype)(nil)

func NewAutotype(clipboard platform.Clipboard) (*Autotype, error) {
	if clipboard == nil {
		return nil, fmt.Errorf("darwin autotype: clipboard is required")
	}
	return &Autotype{clipboard: clipboard}, nil
}

// Type preserves arbitrary Unicode by using NSPasteboard, then posts a native
// Command+V chord with Quartz Event Services.
func (a *Autotype) Type(text string) error {
	if err := a.clipboard.Write(text); err != nil {
		return fmt.Errorf("darwin autotype: write clipboard: %w", err)
	}
	return nativePostPaste()
}
