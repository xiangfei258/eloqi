//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/wayland"
)

// Autotype implements platform.Autotype by writing text to the clipboard and
// then simulating a paste keystroke (Ctrl+V). Wayland uses ydotool on
// GNOME/KDE and wtype on compositors that implement the virtual-keyboard
// protocol; X11 uses xdotool.
type Autotype struct {
	session   string
	desktop   string
	clipboard platform.Clipboard
	timeout   time.Duration
	command   linuxCommandFactory
}

var _ platform.Autotype = (*Autotype)(nil)

// NewAutotype returns an Autotype bound to the given clipboard and display
// session.
func NewAutotype(cb platform.Clipboard) (*Autotype, error) {
	sess, err := sessionType()
	if err != nil {
		return nil, err
	}
	return &Autotype{
		session:   sess,
		desktop:   firstNonEmpty(os.Getenv("XDG_CURRENT_DESKTOP"), os.Getenv("XDG_SESSION_DESKTOP")),
		clipboard: cb,
		timeout:   desktopCommandTimeout,
		command:   exec.CommandContext,
	}, nil
}

// Type writes text to the clipboard and simulates a paste keystroke so the
// text appears in the focused window.
func (a *Autotype) Type(text string) error {
	if a.clipboard != nil {
		if err := a.clipboard.Write(text); err != nil {
			return err
		}
	}
	return a.simulatePaste()
}

// simulatePaste sends a Ctrl+V keystroke to the focused window.
func (a *Autotype) simulatePaste() error {
	timeout := a.timeout
	if timeout <= 0 {
		timeout = desktopCommandTimeout
	}
	command := a.command
	if command == nil {
		command = exec.CommandContext
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	var operation string
	switch a.session {
	case "wayland":
		switch wayland.AutotypeBackendForDesktop(a.desktop) {
		case wayland.AutotypeYdotool:
			// Linux input-event codes: KEY_LEFTCTRL=29 and KEY_V=47. Send
			// every edge in one command so a single paste is generated.
			cmd = command(ctx, "ydotool", "key", "29:1", "47:1", "47:0", "29:0")
			operation = "simulate paste with ydotool"
		default:
			// wtype: -M press modifier, -k tap key, -m release modifier.
			cmd = command(ctx, "wtype", "-M", "ctrl", "-k", "v", "-m", "ctrl")
			operation = "simulate paste with wtype"
		}
	case "x11":
		cmd = command(ctx, "xdotool", "key", "ctrl+v")
		operation = "simulate paste with xdotool"
	default:
		return errors.New("linux autotype: unknown session type")
	}
	if err := cmd.Run(); err != nil {
		return desktopCommandError(operation, timeout, ctx, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
