package windows

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func windowsOverlayPresentation(state platform.OverlayState, message string) (string, uint32, error) {
	var label string
	var color uint32 // Win32 COLORREF: 0x00bbggrr
	switch state {
	case platform.OverlayConnecting:
		label, color = "Connecting", 0x006B5A45
	case platform.OverlayRecording:
		label, color = "Recording", 0x003535D6
	case platform.OverlayStopping:
		label, color = "Finishing", 0x00206FAE
	case platform.OverlayWaiting:
		label, color = "Recognizing", 0x00A06A32
	case platform.OverlayError:
		label, color = "Error", 0x002A2AB8
	default:
		return "", 0, fmt.Errorf("windows overlay: unknown state %q", state)
	}
	message = strings.TrimSpace(message)
	if message != "" {
		label += "  -  " + message
	}
	return label, color, nil
}

// windowsOverlayUTF16 always returns one NUL-terminated, non-empty buffer.
// Embedded NUL bytes are data from an ASR error rather than terminators, so
// replace them before passing the string into DrawTextW.
func windowsOverlayUTF16(text string) []uint16 {
	text = strings.ReplaceAll(text, "\x00", "\uFFFD")
	encoded := utf16.Encode([]rune(text))
	return append(encoded, 0)
}
