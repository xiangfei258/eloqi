//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const desktopCommandTimeout = 3 * time.Second

type linuxCommandFactory func(context.Context, string, ...string) *exec.Cmd

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
	timeout time.Duration
	command linuxCommandFactory
}

var _ platform.Clipboard = (*Clipboard)(nil)

// NewClipboard returns a Clipboard for the current display session.
func NewClipboard() (*Clipboard, error) {
	sess, err := sessionType()
	if err != nil {
		return nil, err
	}
	return &Clipboard{session: sess, timeout: desktopCommandTimeout, command: exec.CommandContext}, nil
}

// Read returns the current clipboard text.
func (c *Clipboard) Read() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.commandTimeout())
	defer cancel()
	var cmd *exec.Cmd
	switch c.session {
	case "wayland":
		cmd = c.commandFactory()(ctx, "wl-paste", "--no-newline")
	case "x11":
		cmd = c.commandFactory()(ctx, "xclip", "-selection", "clipboard", "-o")
	default:
		return "", errors.New("linux clipboard: unknown session type")
	}
	out, err := cmd.Output()
	if err != nil {
		return "", desktopCommandError("read clipboard", c.commandTimeout(), ctx, err)
	}
	return string(out), nil
}

// Write replaces the clipboard contents with text.
func (c *Clipboard) Write(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.commandTimeout())
	defer cancel()
	var cmd *exec.Cmd
	switch c.session {
	case "wayland":
		cmd = c.commandFactory()(ctx, "wl-copy")
	case "x11":
		cmd = c.commandFactory()(ctx, "xclip", "-selection", "clipboard")
	default:
		return errors.New("linux clipboard: unknown session type")
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return desktopCommandError("write clipboard", c.commandTimeout(), ctx, err)
	}
	return nil
}

func (c *Clipboard) commandTimeout() time.Duration {
	if c.timeout <= 0 {
		return desktopCommandTimeout
	}
	return c.timeout
}

func (c *Clipboard) commandFactory() linuxCommandFactory {
	if c.command == nil {
		return exec.CommandContext
	}
	return c.command
}

func desktopCommandError(operation string, timeout time.Duration, ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("linux: %s timed out after %s: %w", operation, timeout, ctx.Err())
	}
	return fmt.Errorf("linux: %s: %w", operation, err)
}
