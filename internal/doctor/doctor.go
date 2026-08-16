// Package doctor inspects the host environment and turns missing runtime
// capabilities into short, actionable user guidance.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xiangchang24/eloqi/internal/evdev"
	"github.com/xiangchang24/eloqi/internal/wayland"
)

const ydotoolProbeTimeout = 2 * time.Second

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
// GOOS/Getenv/LookPath/Glob/Open/RunCommand values use runtime.GOOS,
// os.Getenv, exec.LookPath, filepath.Glob, os.Open, and exec.CommandContext.
type Options struct {
	GOOS            string
	Getenv          func(string) string
	LookPath        func(string) (string, error)
	Glob            func(string) ([]string, error)
	Open            func(string) (io.ReadCloser, error)
	ReadFile        func(string) ([]byte, error)
	RunCommand      func(context.Context, string, ...string) error
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
	if options.RunCommand == nil {
		options.RunCommand = func(ctx context.Context, executable string, args ...string) error {
			command := exec.CommandContext(ctx, executable, args...)
			command.Stdout = io.Discard
			command.Stderr = io.Discard
			return command.Run()
		}
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
			checkWaylandAutoType(options),
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
		ydotoolDevice, probeErr := evdev.IsYdotoolVirtualDevice(path, readFile)
		// Keep this fail-closed decision aligned with the runtime hotkey
		// enumerator. A device whose origin cannot be identified must not make
		// doctor promise that a physical keyboard is usable.
		if probeErr != nil || ydotoolDevice {
			continue
		}
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
	return evdevFailure(fmt.Sprintf("none of the %d /dev/input/event* devices is a readable physical keyboard (ydotoold virtual input is ignored)", len(paths)))
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

// checkWaylandAutoType selects the backend used by the Linux implementation.
// GNOME Mutter and KDE KWin require the uinput-backed ydotool path; wlroots
// compositors retain the less-privileged wtype path.
func checkWaylandAutoType(options Options) Finding {
	desktop := strings.TrimSpace(options.Getenv("XDG_CURRENT_DESKTOP"))
	if desktop == "" {
		desktop = strings.TrimSpace(options.Getenv("XDG_SESSION_DESKTOP"))
	}
	if wayland.AutotypeBackendForDesktop(desktop) == wayland.AutotypeYdotool {
		return checkYdotoolAutoType(options)
	}
	return checkExecutable(
		options.LookPath,
		"autotype.wtype",
		"wtype",
		options.RequireAutoType,
		"Install wtype, or set output.auto_type = false and paste from the clipboard manually. On GNOME/KDE Wayland, use the ydotool backend instead.",
	)
}

func checkYdotoolAutoType(options Options) Finding {
	const installHint = "Install Ubuntu's ydotool package (`sudo apt install ydotool`), add your user to the input group (`sudo usermod -aG input \"$USER\"`), sign out and back in, then run `systemctl --user enable --now ydotool.service`."
	const daemonHint = "Run `systemctl --user enable --now ydotool.service`, then verify it with `ydotool debug`. If /dev/uinput is denied, add your user to the input group and sign out and back in. Set output.auto_type = false to use clipboard-only mode."

	path, err := options.LookPath("ydotool")
	if err != nil {
		return Finding{
			ID:       "autotype.ydotool",
			Status:   optionalStatus(options.RequireAutoType),
			Required: options.RequireAutoType,
			Message:  "ydotool was not found in PATH",
			Hint:     installHint,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), ydotoolProbeTimeout)
	defer cancel()
	err = options.RunCommand(ctx, path, "debug")
	if err == nil {
		return Finding{
			ID:       "autotype.ydotool",
			Status:   StatusOK,
			Required: options.RequireAutoType,
			Message:  fmt.Sprintf("ydotool at %s connected to ydotoold", path),
		}
	}
	message := fmt.Sprintf("found ydotool at %s, but it could not connect to ydotoold", path)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		message = fmt.Sprintf("ydotool at %s did not complete its ydotoold connection probe within %s", path, ydotoolProbeTimeout)
	}
	return Finding{
		ID:       "autotype.ydotool",
		Status:   optionalStatus(options.RequireAutoType),
		Required: options.RequireAutoType,
		Message:  message,
		Hint:     daemonHint,
	}
}

func optionalStatus(required bool) Status {
	if required {
		return StatusError
	}
	return StatusWarning
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
