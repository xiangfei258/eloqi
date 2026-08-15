package windows

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestWindowsOverlayPresentation(t *testing.T) {
	t.Parallel()
	states := []platform.OverlayState{
		platform.OverlayConnecting,
		platform.OverlayRecording,
		platform.OverlayStopping,
		platform.OverlayWaiting,
		platform.OverlayError,
	}
	for _, state := range states {
		text, color, err := windowsOverlayPresentation(state, "  detail  ")
		if err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
		if color == 0 || !strings.Contains(text, "detail") {
			t.Fatalf("state %q produced text=%q color=%#x", state, text, color)
		}
	}
	if _, _, err := windowsOverlayPresentation("bogus", ""); err == nil {
		t.Fatal("unknown state should fail")
	}
}

func TestWindowsOverlayUTF16SanitizesEmbeddedNUL(t *testing.T) {
	t.Parallel()
	encoded := windowsOverlayUTF16("识别\x00失败 😀\r\n")
	if len(encoded) == 0 || encoded[len(encoded)-1] != 0 {
		t.Fatalf("UTF-16 buffer is not NUL terminated: %#v", encoded)
	}
	for index, unit := range encoded[:len(encoded)-1] {
		if unit == 0 {
			t.Fatalf("embedded NUL survived at UTF-16 index %d", index)
		}
	}
	if got, want := string(utf16.Decode(encoded[:len(encoded)-1])), "识别\uFFFD失败 😀\r\n"; got != want {
		t.Fatalf("decoded text = %q, want %q", got, want)
	}
	if empty := windowsOverlayUTF16(""); len(empty) != 1 || empty[0] != 0 {
		t.Fatalf("empty text buffer = %#v, want one terminator", empty)
	}
}
