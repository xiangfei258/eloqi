// Package doctor inspects the host environment and turns missing runtime
// capabilities into short, actionable user guidance.
package doctor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xiangchang24/eloqi/internal/evdev"
)

// Status is the outcome of one environment check.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
)

// Finding describes one checked capability. Hint is populated whenever the
// user may need to take action.
type Finding struct {
	ID       string
	Status   Status
	Required bool
	Message  string
	Hint     string
}

// Report contains all findings for the selected operating system/session.
type Report struct {
	Findings []Finding
}

// OK reports whether every required check passed. Warnings do not make a
// report unhealthy.
func (r Report) OK() bool {
	for _, finding := range r.Findings {
		if finding.Status == StatusError {
			return false
		}
	}
	return true
}

// Error returns a combined error for failed required checks, or nil when the
// report is healthy.
func (r Report) Error() error {
	var failures []error
	for _, finding := range r.Findings {
		if finding.Status != StatusError {
			continue
		}
		message := finding.Message
		if finding.Hint != "" {
			message += "; " + finding.Hint
		}
		failures = append(failures, errors.New(message))
	}
	return errors.Join(failures...)
}

// WriteTo renders a compact report suitable for a CLI startup check.
func (r Report) WriteTo(w io.Writer) (int64, error) {
	var written int64
	for _, finding := range r.Findings {
		n, err := fmt.Fprintf(w, "[%s] %s: %s\n", finding.Status, finding.ID, finding.Message)
		written += int64(n)
		if err != nil {
			return written, err
		}
		if finding.Hint != "" {
			n, err := fmt.Fprintf(w, "  Fix: %s\n", finding.Hint)
			written += int64(n)
			if err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Options provides injectable host probes for deterministic tests. Empty
// GOOS/Getenv/LookPath/Glob/Open values use runtime.GOOS, os.Getenv,
// exec.LookPath, filepath.Glob, and os.Open.
type Options struct {
	GOOS            string
	Getenv          func(string) string
	LookPath        func(string) (string, error)
	Glob            func(string) ([]string, error)
	Open            func(string) (io.ReadCloser, error)
	ReadFile        func(string) ([]byte, error)
	RequireAutoType bool
}

// Check runs the checks appropriate for the configured operating system and,
// on Linux, the active Wayland/X11 session.
func Check(options Options) Report {
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.Glob == nil {
		options.Glob = filepath.Glob
	}
	if options.Open == nil {
		options.Open = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	}
	if options.ReadFile == nil {
		options.ReadFile = os.ReadFile
	}

	switch options.GOOS {
	case "linux":
		return checkLinux(options)
	case "darwin":
		return checkDarwin(options)
	case "windows":
		return checkWindows(options)
	default:
		return Report{Findings: []Finding{{
			ID:       "platform",
			Status:   StatusError,
			Required: true,
			Message:  fmt.Sprintf("Eloqui does not support %q", options.GOOS),
			Hint:     "Run Eloqui on Linux, macOS, or Windows.",
		}}}
	}
}

func checkLinux(options Options) Report {
	findings := []Finding{
		checkExecutable(options.LookPath, "audio.arecord", "arecord", true,
			"Install the ALSA utilities package (for example, `sudo apt install alsa-utils`)."),
	}

	wayland := strings.TrimSpace(options.Getenv("WAYLAND_DISPLAY")) != ""
	x11 := strings.TrimSpace(options.Getenv("DISPLAY")) != ""
	switch {
	case wayland:
		findings = append(findings,
			Finding{ID: "display", Status: StatusOK, Required: true, Message: "Wayland session detected"},
			checkEvdevAccess(options.Glob, options.Open, options.ReadFile),
			checkExecutable(options.LookPath, "clipboard.wl-copy", "wl-copy", true,
				"Install wl-clipboard (for example, `sudo apt install wl-clipboard`)."),
			checkExecutable(options.LookPath, "clipboard.wl-paste", "wl-paste", true,
				"Install wl-clipboard (for example, `sudo apt install wl-clipboard`)."),
			checkWtypeAutoType(options),
			checkExecutable(options.LookPath, "overlay.notify-send", "notify-send", false,
				"Install a notify-send provider (for example, `sudo apt install libnotify-bin`) to enable the GNOME overlay fallback."),
		)
	case x11:
		findings = append(findings,
			Finding{ID: "display", Status: StatusOK, Required: true, Message: "X11 session detected"},
			checkExecutable(options.LookPath, "clipboard.xclip", "xclip", true,
				"Install xclip (for example, `sudo apt install xclip`)."),
			checkExecutable(options.LookPath, "autotype.xdotool", "xdotool", options.RequireAutoType,
				"Install xdotool, or set output.auto_type = false and paste from the clipboard manually."),
		)
	default:
		findings = append(findings, Finding{
			ID:       "display",
			Status:   StatusError,
			Required: true,
			Message:  "no Wayland or X11 desktop session was detected",
			Hint:     "Start Eloqui from your graphical desktop session and preserve WAYLAND_DISPLAY or DISPLAY.",
		})
	}

	return Report{Findings: findings}
}

func checkEvdevAccess(
	glob func(string) ([]string, error),
	open func(string) (io.ReadCloser, error),
	readFile func(string) ([]byte, error),
) Finding {
	const pattern = "/dev/input/event*"
	paths, err := glob(pattern)
	if err != nil {
		return evdevFailure(fmt.Sprintf("could not inspect %s: %v", pattern, err))
	}
	for _, path := range paths {
		device, err := open(path)
		if err != nil {
			continue
		}
		_ = device.Close()
		keyboard, err := evdev.IsKeyboardDevice(path, readFile)
		if err != nil || !keyboard {
			continue
		}
		return Finding{
			ID:       "hotkey.evdev-access",
			Status:   StatusOK,
			Required: true,
			Message:  fmt.Sprintf("global hotkey input is readable at %s", path),
		}
	}
	if len(paths) == 0 {
		return evdevFailure("no /dev/input/event* devices were found for the Wayland hotkey backend")
	}
	return evdevFailure(fmt.Sprintf("none of the %d /dev/input/event* devices is both readable and keyboard-capable", len(paths)))
}

func evdevFailure(message string) Finding {
	return Finding{
		ID:       "hotkey.evdev-access",
		Status:   StatusError,
		Required: true,
		Message:  message,
		Hint:     "Add your user to the input group, then sign out and back in before starting Eloqui again.",
	}
}

func checkDarwin(_ Options) Report {
	return Report{Findings: []Finding{
		{
			ID:       "runtime.native",
			Status:   StatusOK,
			Required: true,
			Message:  "macOS backend uses native APIs; no external commands are required",
		},
		{
			ID:       "permission.microphone",
			Status:   StatusWarning,
			Required: false,
			Message:  "microphone permission must be granted to the terminal or app that launches Eloqui",
			Hint:     "Open System Settings > Privacy & Security > Microphone and enable the launching app.",
		},
		{
			ID:       "permission.accessibility",
			Status:   StatusWarning,
			Required: false,
			Message:  "global hotkeys and automatic typing need Accessibility permission",
			Hint:     "Open System Settings > Privacy & Security > Accessibility and enable the launching app.",
		},
	}}
}

func checkWindows(_ Options) Report {
	return Report{Findings: []Finding{
		{
			ID:       "runtime.native",
			Status:   StatusOK,
			Required: true,
			Message:  "Windows backend uses native APIs; no external commands are required",
		},
		{
			ID:       "permission.microphone",
			Status:   StatusWarning,
			Required: false,
			Message:  "Windows must allow desktop apps to access the microphone",
			Hint:     "Open Settings > Privacy & security > Microphone and enable desktop app access.",
		},
	}}
}

// checkWtypeAutoType reports whether wtype can actually inject keystrokes in
// the active Wayland session. wtype depends on the wlroots
// zwp_virtual_keyboard_manager_v1 protocol, which GNOME Mutter and KDE KWin do
// not implement; on those desktops the binary may be installed yet still fail
// at runtime, so doctor must not present it as healthy.
func checkWtypeAutoType(options Options) Finding {
	const hint = "This Wayland compositor does not implement wtype's virtual-keyboard protocol; set output.auto_type = false to use the clipboard, or sign in to an X11/Xorg session (which uses xdotool)."

	path, err := options.LookPath("wtype")
	if err != nil {
		status := StatusWarning
		if options.RequireAutoType {
			status = StatusError
		}
		return Finding{
			ID:       "autotype.wtype",
			Status:   status,
			Required: options.RequireAutoType,
			Message:  "wtype was not found in PATH",
			Hint:     hint,
		}
	}

	desktop := strings.ToLower(strings.TrimSpace(options.Getenv("XDG_CURRENT_DESKTOP")))
	unsupported := strings.Contains(desktop, "gnome") ||
		strings.Contains(desktop, "kde") ||
		strings.Contains(desktop, "kwin")
	if !unsupported {
		return Finding{
			ID:       "autotype.wtype",
			Status:   StatusOK,
			Required: options.RequireAutoType,
			Message:  fmt.Sprintf("found wtype at %s", path),
		}
	}

	status := StatusWarning
	if options.RequireAutoType {
		status = StatusError
	}
	return Finding{
		ID:       "autotype.wtype",
		Status:   status,
		Required: options.RequireAutoType,
		Message:  fmt.Sprintf("found wtype at %s, but %s Wayland does not implement the virtual-keyboard protocol wtype requires", path, desktop),
		Hint:     hint,
	}
}

func checkExecutable(lookPath func(string) (string, error), id, executable string, required bool, hint string) Finding {
	path, err := lookPath(executable)
	if err == nil {
		return Finding{
			ID:       id,
			Status:   StatusOK,
			Required: required,
			Message:  fmt.Sprintf("found %s at %s", executable, path),
		}
	}
	status := StatusWarning
	if required {
		status = StatusError
	}
	return Finding{
		ID:       id,
		Status:   status,
		Required: required,
		Message:  fmt.Sprintf("%s was not found in PATH", executable),
		Hint:     hint,
	}
}
