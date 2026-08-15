package windows

import (
	"encoding/binary"
	"testing"
)

func TestWindowsClipboardTextRoundTrip(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"",
		"中文语音输入",
		"emoji: 🎙️🚀",
		"first line\r\nsecond line\n",
	} {
		text := text
		t.Run(text, func(t *testing.T) {
			encoded, err := encodeWindowsClipboardText(text)
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) < 2 || binary.LittleEndian.Uint16(encoded[len(encoded)-2:]) != 0 {
				t.Fatalf("CF_UNICODETEXT is not NUL terminated: %x", encoded)
			}
			decoded, err := decodeWindowsClipboardText(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if decoded != text {
				t.Fatalf("decoded %q, want %q", decoded, text)
			}
		})
	}
}

func TestWindowsClipboardTextNULBoundary(t *testing.T) {
	t.Parallel()
	if _, err := encodeWindowsClipboardText("before\x00after"); err == nil {
		t.Fatal("embedded NUL must be rejected before CF_UNICODETEXT truncates it")
	}
	// Clipboard owners may allocate spare bytes after the first terminator.
	encoded := []byte{'o', 0, 'k', 0, 0, 0, 'x', 0}
	decoded, err := decodeWindowsClipboardText(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "ok" {
		t.Fatalf("decoded %q, want ok", decoded)
	}
	if _, err := decodeWindowsClipboardText([]byte{1}); err == nil {
		t.Fatal("odd-length UTF-16 data must fail")
	}
}
