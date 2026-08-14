//go:build linux

// Package linux provides Linux implementations of the platform capability
// interfaces. Wayland and X11 backends are selected at runtime by probing the
// WAYLAND_DISPLAY and DISPLAY environment variables.
//
// Audio capture shells out to arecord (ALSA), which is display-server
// independent. Clipboard, autotype and hotkey each have Wayland and X11
// variants.
package linux
