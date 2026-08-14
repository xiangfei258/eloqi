package platform

// Clipboard reads and writes the system clipboard as plain text.
type Clipboard interface {
	// Read returns the current clipboard text.
	Read() (string, error)

	// Write replaces the clipboard contents with text.
	Write(text string) error
}
