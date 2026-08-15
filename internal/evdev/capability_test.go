package evdev

import (
	"errors"
	"strings"
	"testing"
)

func TestHasKeyboardCapability(t *testing.T) {
	for _, test := range []struct {
		name   string
		bitmap string
		want   bool
		bad    bool
	}{
		{name: "keyboard", bitmap: "0 0 80002\n", want: true},
		{name: "mouse buttons only", bitmap: "70000 0 0 0 0", want: false},
		{name: "escape without retry key", bitmap: "2", want: false},
		{name: "empty", bad: true},
		{name: "malformed", bitmap: "not-hex", bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := HasKeyboardCapability([]byte(test.bitmap))
			if test.bad {
				if err == nil {
					t.Fatalf("HasKeyboardCapability succeeded: %v", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("HasKeyboardCapability = (%v, %v), want (%v, nil)", got, err, test.want)
			}
		})
	}
}

func TestIsKeyboardDevice(t *testing.T) {
	got, err := IsKeyboardDevice("/dev/input/event12", func(path string) ([]byte, error) {
		if path != "/sys/class/input/event12/device/capabilities/key" {
			t.Fatalf("capability path = %q", path)
		}
		return []byte("80002"), nil
	})
	if err != nil || !got {
		t.Fatalf("IsKeyboardDevice = (%v, %v)", got, err)
	}

	want := errors.New("sysfs unavailable")
	_, err = IsKeyboardDevice("/dev/input/event2", func(string) ([]byte, error) { return nil, want })
	if err == nil || !errors.Is(err, want) || !strings.Contains(err.Error(), "event2") {
		t.Fatalf("IsKeyboardDevice error = %v", err)
	}
}
