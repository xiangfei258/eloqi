//go:build linux

package app

import (
	"fmt"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/linux"
)

// newCapabilities creates the Linux platform implementations.
func newCapabilities() (*capabilities, error) {
	cb, err := linux.NewClipboard()
	if err != nil {
		return nil, fmt.Errorf("clipboard: %w", err)
	}

	var at platform.Autotype
	var warnings []error
	autotype, err := linux.NewAutotype(cb)
	if err != nil {
		// Autotype is optional; clipboard-only mode is still usable.
		at = nil
		warnings = append(warnings, fmt.Errorf("autotype unavailable: %w", err))
	} else {
		at = autotype
	}

	var statusOverlay platform.Overlay
	statusOverlay, err = linux.NewOverlay()
	if err != nil {
		// The status capsule is helpful but must never make voice input unusable.
		warnings = append(warnings, fmt.Errorf("overlay unavailable: %w", err))
		statusOverlay = nil
	}

	return &capabilities{
		newHotkey: func() (platform.Hotkey, error) {
			hotkey, err := linux.NewHotkey()
			if err != nil {
				return nil, fmt.Errorf("hotkey: %w", err)
			}
			return hotkey, nil
		},
		newRecorder: func() platform.Recorder { return linux.NewRecorder() },
		clipboard:   cb,
		autotype:    at,
		overlay:     statusOverlay,
		warnings:    warnings,
	}, nil
}
