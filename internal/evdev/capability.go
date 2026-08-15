// Package evdev contains Linux input-device capability parsing shared by the
// Wayland hotkey backend and the startup doctor.
package evdev

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

const (
	keyEscape = 1
	keyR      = 19
)

// KeyboardCapabilityPath maps /dev/input/eventN to its sysfs key capability
// bitmap. Linux exposes the lowest machine word last in this file.
func KeyboardCapabilityPath(devicePath string) (string, error) {
	base := path.Base(path.Clean(devicePath))
	if !strings.HasPrefix(base, "event") || len(base) == len("event") {
		return "", fmt.Errorf("evdev: invalid event device path %q", devicePath)
	}
	return path.Join("/sys/class/input", base, "device/capabilities/key"), nil
}

// HasKeyboardCapability reports whether a sysfs key bitmap contains both Esc
// and R. Those keys are present on ordinary keyboards and are required by
// Eloqui's cancellation/retry interaction; readable mice and sensors do not
// satisfy this probe.
func HasKeyboardCapability(bitmap []byte) (bool, error) {
	fields := strings.Fields(string(bitmap))
	if len(fields) == 0 {
		return false, fmt.Errorf("evdev: empty key capability bitmap")
	}
	lowestWord, err := strconv.ParseUint(fields[len(fields)-1], 16, 64)
	if err != nil {
		return false, fmt.Errorf("evdev: parse key capability bitmap: %w", err)
	}
	required := uint64(1<<keyEscape | 1<<keyR)
	return lowestWord&required == required, nil
}

// IsKeyboardDevice reads and evaluates one event device's sysfs bitmap.
func IsKeyboardDevice(devicePath string, readFile func(string) ([]byte, error)) (bool, error) {
	capabilityPath, err := KeyboardCapabilityPath(devicePath)
	if err != nil {
		return false, err
	}
	bitmap, err := readFile(capabilityPath)
	if err != nil {
		return false, fmt.Errorf("evdev: read keyboard capabilities for %s: %w", devicePath, err)
	}
	return HasKeyboardCapability(bitmap)
}
