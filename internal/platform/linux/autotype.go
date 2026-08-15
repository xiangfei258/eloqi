//go:build linux

package linux

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// Autotype implements platform.Autotype by writing text to the clipboard and
// then simulating a paste keystroke (Ctrl+V). On Wayland it uses wtype; on
// X11 it uses xdotool.
type Autotype struct {
	session   string
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
		session: sess, clipboard: cb, timeout: desktopCommandTimeout, command: exec.CommandContext,
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
	switch a.session {
	case "wayland":
		// wtype: -M press modifier, -k tap key, -m release modifier
		cmd = command(ctx, "wtype", "-M", "ctrl", "-k", "v", "-m", "ctrl")
	case "x11":
		cmd = command(ctx, "xdotool", "key", "ctrl+v")
	default:
		return errors.New("linux autotype: unknown session type")
	}
	if err := cmd.Run(); err != nil {
		return desktopCommandError("simulate paste", timeout, ctx, err)
	}
	return nil
}
