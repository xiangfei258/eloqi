//go:build linux

package linux

import (
	"errors"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// NewHotkey returns a Hotkey for the current display session. On Wayland it
// uses evdev (direct kernel input events); on X11 it uses native Xlib
// XGrabKey. An error is returned if the required access or libraries are not
// available.
func NewHotkey() (platform.Hotkey, error) {
	sess, err := sessionType()
	if err != nil {
		return nil, err
	}
	switch sess {
	case "wayland":
		return newEvdevHotkey()
	case "x11":
		return newX11Hotkey()
	default:
		return nil, errors.New("linux hotkey: unsupported session type")
	}
}
