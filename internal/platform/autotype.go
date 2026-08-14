package platform

// Autotype injects text into the currently focused window as if the user had
// typed it. Implementations typically write the text to the clipboard and
// then simulate a paste keystroke, so the exact mechanism varies by platform.
type Autotype interface {
	// Type injects text into the focused window.
	Type(text string) error
}
