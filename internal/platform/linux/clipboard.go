//go:build linux

package linux

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// sessionType returns "wayland" when WAYLAND_DISPLAY is set, "x11" when
// DISPLAY is set, and an error otherwise.
func sessionType() (string, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland", nil
	}
	if os.Getenv("DISPLAY") != "" {
		return "x11", nil
	}
	return "", errors.New("linux: no display server detected (neither WAYLAND_DISPLAY nor DISPLAY is set)")
}

// Clipboard implements platform.Clipboard by shelling out to wl-copy/wl-paste
// (Wayland) or xclip (X11).
type Clipboard struct {
	session string
}

var _ platform.Clipboard = (*Clipboard)(nil)

// NewClipboard returns a Clipboard for the current display session.
func NewClipboard() (*Clipboard, error) {
	sess, err := sessionType()
	if err != nil {
		return nil, err
	}
	return &Clipboard{session: sess}, nil
}

// Read returns the current clipboard text.
func (c *Clipboard) Read() (string, error) {
	var cmd *exec.Cmd
	switch c.session {
	case "wayland":
		cmd = exec.Command("wl-paste", "--no-newline")
	case "x11":
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
	default:
		return "", errors.New("linux clipboard: unknown session type")
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Write replaces the clipboard contents with text.
func (c *Clipboard) Write(text string) error {
	var cmd *exec.Cmd
	switch c.session {
	case "wayland":
		cmd = exec.Command("wl-copy")
	case "x11":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return errors.New("linux clipboard: unknown session type")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
