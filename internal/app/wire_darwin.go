//go:build darwin

package app

import (
	"fmt"

	"github.com/xiangchang24/eloqi/internal/platform"
	platformdarwin "github.com/xiangchang24/eloqi/internal/platform/darwin"
)

func newCapabilities() (*capabilities, error) {
	clipboard, err := platformdarwin.NewClipboard()
	if err != nil {
		return nil, fmt.Errorf("clipboard: %w", err)
	}

	var warnings []error
	var autotype platform.Autotype
	if implementation, err := platformdarwin.NewAutotype(clipboard); err != nil {
		warnings = append(warnings, fmt.Errorf("autotype unavailable: %w", err))
	} else {
		autotype = implementation
	}
	var statusOverlay platform.Overlay
	if implementation, err := platformdarwin.NewOverlay(); err != nil {
		warnings = append(warnings, fmt.Errorf("overlay unavailable: %w", err))
	} else {
		statusOverlay = implementation
	}

	return &capabilities{
		newHotkey: func() (platform.Hotkey, error) {
			hotkey, err := platformdarwin.NewHotkey()
			if err != nil {
				return nil, fmt.Errorf("hotkey: %w", err)
			}
			return hotkey, nil
		},
		newRecorder: func() platform.Recorder { return platformdarwin.NewRecorder() },
		clipboard:   clipboard,
		autotype:    autotype,
		overlay:     statusOverlay,
		warnings:    warnings,
	}, nil
}
