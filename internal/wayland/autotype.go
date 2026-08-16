// Package wayland contains display-session decisions shared by Linux runtime
// capabilities and the cross-platform environment doctor.
package wayland

import "strings"

// AutotypeBackend identifies the command used to inject the paste shortcut in
// a Wayland session.
type AutotypeBackend string

const (
	// AutotypeWtype uses the virtual-keyboard Wayland protocol implemented by
	// wlroots compositors such as Sway.
	AutotypeWtype AutotypeBackend = "wtype"
	// AutotypeYdotool uses the kernel uinput path provided by ydotoold. It is
	// required on compositors such as GNOME Mutter and KDE KWin that do not
	// implement wtype's virtual-keyboard protocol.
	AutotypeYdotool AutotypeBackend = "ydotool"
)

// AutotypeBackendForDesktop selects the least-privileged known-working
// backend for XDG_CURRENT_DESKTOP/XDG_SESSION_DESKTOP. Unknown desktops keep
// the existing wtype path; doctor reports whether that command is installed.
func AutotypeBackendForDesktop(desktop string) AutotypeBackend {
	for _, token := range strings.FieldsFunc(strings.ToLower(desktop), func(r rune) bool {
		return r == ':' || r == ';' || r == ',' || r == '/' || r == ' ' || r == '\t'
	}) {
		switch token {
		case "gnome", "ubuntu", "kde", "plasma", "kwin":
			return AutotypeYdotool
		}
	}
	return AutotypeWtype
}
