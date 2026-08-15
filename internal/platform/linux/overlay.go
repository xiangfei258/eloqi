//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const overlayNotificationKey = "eloqi-recording-state"

const overlayCommandTimeout = 1500 * time.Millisecond

// NewOverlay selects a native X11 capsule or a desktop notification fallback
// at runtime. Wayland compositors do not share a portable layer-shell API, so
// the freedesktop notification service is the reliable cross-desktop path.
func NewOverlay() (platform.Overlay, error) {
	if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") != "" {
		return newX11Overlay()
	}
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return nil, fmt.Errorf("linux overlay: notify-send not found; install libnotify-bin: %w", err)
	}
	return newNotificationOverlay(func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), overlayCommandTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, path, args...).Run(); err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("notify-send timed out after %s: %w", overlayCommandTimeout, ctx.Err())
			}
			return err
		}
		return nil
	}), nil
}

type notificationOverlay struct {
	mu     sync.Mutex
	run    func(args ...string) error
	closed bool
}

var _ platform.Overlay = (*notificationOverlay)(nil)

func newNotificationOverlay(run func(args ...string) error) *notificationOverlay {
	return &notificationOverlay{run: run}
}

func (o *notificationOverlay) Show(state platform.OverlayState, message string) error {
	title, err := overlayTitle(state)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("linux overlay: closed")
	}

	urgency := "low"
	if state == platform.OverlayError {
		urgency = "normal"
	}
	args := []string{
		"--app-name=Eloqui",
		"--urgency=" + urgency,
		"--expire-time=0",
		"--hint=string:x-canonical-private-synchronous:" + overlayNotificationKey,
		"Eloqui - " + title,
		strings.TrimSpace(message),
	}
	if err := o.run(args...); err != nil {
		return fmt.Errorf("linux overlay: show notification: %w", err)
	}
	return nil
}

func (o *notificationOverlay) Hide() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	return o.hideLocked()
}

func (o *notificationOverlay) hideLocked() error {
	// Replacing the persistent notification with a one-millisecond empty one
	// works on freedesktop servers that support the synchronous hint, including
	// GNOME's notification service.
	args := []string{
		"--app-name=Eloqui",
		"--expire-time=1",
		"--hint=string:x-canonical-private-synchronous:" + overlayNotificationKey,
		"Eloqui",
		"",
	}
	if err := o.run(args...); err != nil {
		return fmt.Errorf("linux overlay: hide notification: %w", err)
	}
	return nil
}

func (o *notificationOverlay) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	err := o.hideLocked()
	o.closed = true
	return err
}

func overlayTitle(state platform.OverlayState) (string, error) {
	switch state {
	case platform.OverlayConnecting:
		return "Connecting", nil
	case platform.OverlayRecording:
		return "Recording", nil
	case platform.OverlayStopping:
		return "Stopping", nil
	case platform.OverlayWaiting:
		return "Waiting for result", nil
	case platform.OverlayError:
		return "Error", nil
	default:
		return "", fmt.Errorf("linux overlay: invalid state %q", state)
	}
}

func overlayDisplayText(state platform.OverlayState, message string) (string, error) {
	title, err := overlayTitle(state)
	if err != nil {
		return "", err
	}
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return title, nil
	}
	runes := []rune(message)
	if len(runes) > 72 {
		message = string(runes[:71]) + "…"
	}
	return title + " - " + message, nil
}
