//go:build linux

package linux

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestNotificationOverlayLifecycle(t *testing.T) {
	var calls [][]string
	o := newNotificationOverlay(func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	if err := o.Show(platform.OverlayRecording, "listening"); err != nil {
		t.Fatal(err)
	}
	if err := o.Hide(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("command calls = %d, want 3", len(calls))
	}
	if got := strings.Join(calls[0], " "); !strings.Contains(got, "--urgency=low") || !strings.Contains(got, "Eloqui - Recording") {
		t.Fatalf("show args = %q", got)
	}
	if err := o.Show(platform.OverlayError, "late"); err == nil {
		t.Fatal("Show after Close succeeded")
	}
}

func TestNotificationOverlayErrors(t *testing.T) {
	want := errors.New("notify failure")
	o := newNotificationOverlay(func(...string) error { return want })
	if err := o.Show(platform.OverlayError, "bad microphone"); !errors.Is(err, want) {
		t.Fatalf("Show error = %v", err)
	}
	if err := o.Hide(); !errors.Is(err, want) {
		t.Fatalf("Hide error = %v", err)
	}
	if err := o.Show("unknown", ""); err == nil {
		t.Fatal("invalid state accepted")
	}
}

func TestOverlayDisplayText(t *testing.T) {
	text, err := overlayDisplayText(platform.OverlayWaiting, "  one\n two  ")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Waiting for result - one two" {
		t.Fatalf("text = %q", text)
	}
	long := strings.Repeat("字", 100)
	text, err = overlayDisplayText(platform.OverlayRecording, long)
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(strings.TrimPrefix(text, "Recording - "))); got != 72 {
		t.Fatalf("truncated runes = %d, want 72", got)
	}
}

func TestX11OverlayLifecycle(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY is not set")
	}
	o, err := newX11Overlay()
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Show(platform.OverlayConnecting, "opening microphone"); err != nil {
		t.Fatal(err)
	}
	if err := o.Hide(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := o.Show(platform.OverlayRecording, "late"); err == nil {
		t.Fatal("Show after Close succeeded")
	}
}
