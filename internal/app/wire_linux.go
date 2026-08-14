//go:build linux

package app

import (
	"fmt"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/linux"
)

// newCapabilities creates the Linux platform implementations.
func newCapabilities() (*capabilities, error) {
	hk, err := linux.NewHotkey()
	if err != nil {
		return nil, fmt.Errorf("hotkey: %w", err)
	}

	cb, err := linux.NewClipboard()
	if err != nil {
		hk.Close()
		return nil, fmt.Errorf("clipboard: %w", err)
	}

	var at platform.Autotype
	autotype, err := linux.NewAutotype(cb)
	if err != nil {
		// Autotype is optional; clipboard-only mode is still usable.
		at = nil
	} else {
		at = autotype
	}

	return &capabilities{
		hotkey:      hk,
		newRecorder: func() platform.Recorder { return linux.NewRecorder() },
		clipboard:   cb,
		autotype:    at,
	}, nil
}
