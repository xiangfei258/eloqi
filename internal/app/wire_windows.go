//go:build windows

package app

import (
	"fmt"

	"github.com/xiangchang24/eloqi/internal/platform"
	platformwindows "github.com/xiangchang24/eloqi/internal/platform/windows"
)

func newCapabilities() (*capabilities, error) {
	clipboard, err := platformwindows.NewClipboard()
	if err != nil {
		return nil, fmt.Errorf("clipboard: %w", err)
	}

	var warnings []error
	var autotype platform.Autotype
	if implementation, err := platformwindows.NewAutotype(clipboard); err != nil {
		warnings = append(warnings, fmt.Errorf("autotype unavailable: %w", err))
	} else {
		autotype = implementation
	}
	var statusOverlay platform.Overlay
	if implementation, err := platformwindows.NewOverlay(); err != nil {
		warnings = append(warnings, fmt.Errorf("overlay unavailable: %w", err))
	} else {
		statusOverlay = implementation
	}

	return &capabilities{
		newHotkey: func() (platform.Hotkey, error) {
			hotkey, err := platformwindows.NewHotkey()
			if err != nil {
				return nil, fmt.Errorf("hotkey: %w", err)
			}
			return hotkey, nil
		},
		newRecorder: func() platform.Recorder { return platformwindows.NewRecorder() },
		clipboard:   clipboard,
		autotype:    autotype,
		overlay:     statusOverlay,
		warnings:    warnings,
	}, nil
}
