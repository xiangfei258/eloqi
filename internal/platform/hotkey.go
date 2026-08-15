package platform

import (
	"strconv"
	"strings"
)

// Modifiers is a bitmask of modifier keys held together with a hotkey.
type Modifiers uint8

// The four modifier keys Eloqui recognizes. They can be combined, and a
// hotkey may also consist of modifiers alone (for example Ctrl+Alt).
const (
	ModCtrl  Modifiers = 1 << iota
	ModAlt             // Alt on Windows/Linux, Option on macOS
	ModSuper           // Super/Windows/Command
	ModShift
)

// modifierNames is ordered for a stable String representation.
var modifierNames = []struct {
	mod  Modifiers
	name string
}{
	{ModCtrl, "Ctrl"},
	{ModAlt, "Alt"},
	{ModSuper, "Super"},
	{ModShift, "Shift"},
}

// String renders the mask in canonical order, e.g. "Ctrl+Shift". It returns
// an empty string for the zero value.
func (m Modifiers) String() string {
	var b strings.Builder
	written := false
	for _, entry := range modifierNames {
		if m&entry.mod == 0 {
			continue
		}
		if written {
			b.WriteByte('+')
		}
		b.WriteString(entry.name)
		written = true
	}
	return b.String()
}

// KeyCode is a stable, platform-independent name for a non-modifier key.
type KeyCode string

// KeyNone marks a binding that consists of modifiers only.
const KeyNone KeyCode = ""

// Well-known key codes that are valid hotkey targets. Plain character keys
// (letters, digits, punctuation, space) are deliberately absent: a hotkey
// must not collide with ordinary typing.
const (
	// KeyEscape and KeyR are reserved auxiliary bindings used while a voice
	// session is active or in the error state. Configuration validation does
	// not accept them as primary user hotkeys.
	KeyEscape KeyCode = "Escape"
	KeyR      KeyCode = "R"

	KeyTab      KeyCode = "Tab"
	KeyCapsLock KeyCode = "CapsLock"

	KeyLeft  KeyCode = "Left"
	KeyRight KeyCode = "Right"
	KeyUp    KeyCode = "Up"
	KeyDown  KeyCode = "Down"

	KeyHome     KeyCode = "Home"
	KeyEnd      KeyCode = "End"
	KeyPageUp   KeyCode = "PageUp"
	KeyPageDown KeyCode = "PageDown"
	KeyInsert   KeyCode = "Insert"
	KeyDelete   KeyCode = "Delete"

	KeyNum0 KeyCode = "Num0"
	KeyNum1 KeyCode = "Num1"
	KeyNum2 KeyCode = "Num2"
	KeyNum3 KeyCode = "Num3"
	KeyNum4 KeyCode = "Num4"
	KeyNum5 KeyCode = "Num5"
	KeyNum6 KeyCode = "Num6"
	KeyNum7 KeyCode = "Num7"
	KeyNum8 KeyCode = "Num8"
	KeyNum9 KeyCode = "Num9"
)

// FunctionKey returns the KeyCode for the Fn key (F1..F24). It reports false
// when n is outside that range, since Eloqui only accepts F1 through F24.
func FunctionKey(n int) (KeyCode, bool) {
	if n < 1 || n > 24 {
		return KeyNone, false
	}
	return KeyCode("F" + strconv.Itoa(n)), true
}

// Key describes a complete hotkey binding: a set of modifiers plus an
// optional non-modifier key. A zero Code (KeyNone) means a modifier-only
// binding such as Alt+Super.
type Key struct {
	Mods Modifiers
	Code KeyCode
}

// String renders the binding, e.g. "Ctrl+F1" or "Alt+Super".
func (k Key) String() string {
	mods := k.Mods.String()
	if k.Code == KeyNone {
		return mods
	}
	if mods == "" {
		return string(k.Code)
	}
	return mods + "+" + string(k.Code)
}

// KeyEvent is a single edge transition on a registered hotkey.
type KeyEvent struct {
	Key Key
	// Pressed is true for the press edge and false for the release edge.
	Pressed bool
}

// Hotkey listens for global hotkey events and reports press/release edges.
//
// Registrations happen once at initialization (before the engine starts) so
// that no edge is lost to event-loop startup latency. Edges are delivered on
// the channel returned by Events and must never be replayed for a single
// physical transition.
type Hotkey interface {
	// Register binds key and starts reporting its edges.
	Register(key Key) error

	// Unregister removes a previously registered binding.
	Unregister(key Key) error

	// Events returns the channel that delivers press/release edge events.
	Events() <-chan KeyEvent

	// Close stops listening and releases the underlying provider.
	Close() error
}
