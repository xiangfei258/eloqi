//go:build linux

package linux

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/xiangchang24/eloqi/internal/wayland"
)

type autotypeClipboard struct {
	writes []string
	err    error
}

func (c *autotypeClipboard) Read() (string, error) { return "", nil }

func (c *autotypeClipboard) Write(text string) error {
	c.writes = append(c.writes, text)
	return c.err
}

type commandCall struct {
	name string
	args []string
}

func recordingCommand(calls *[]commandCall) linuxCommandFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		*calls = append(*calls, commandCall{name: name, args: append([]string(nil), args...)})
		return exec.CommandContext(ctx, "true")
	}
}

func TestAutotypeSelectsPasteCommand(t *testing.T) {
	tests := []struct {
		name    string
		session string
		desktop string
		want    commandCall
	}{
		{
			name:    "ubuntu gnome uses ydotool",
			session: "wayland",
			desktop: "ubuntu:GNOME",
			want:    commandCall{name: "ydotool", args: []string{"key", "29:1", "47:1", "47:0", "29:0"}},
		},
		{
			name:    "kde uses ydotool",
			session: "wayland",
			desktop: "KDE",
			want:    commandCall{name: "ydotool", args: []string{"key", "29:1", "47:1", "47:0", "29:0"}},
		},
		{
			name:    "sway keeps wtype",
			session: "wayland",
			desktop: "sway",
			want:    commandCall{name: "wtype", args: []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}},
		},
		{
			name:    "x11 keeps xdotool",
			session: "x11",
			desktop: "GNOME",
			want:    commandCall{name: "xdotool", args: []string{"key", "ctrl+v"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []commandCall
			autotype := &Autotype{
				session: tt.session,
				desktop: tt.desktop,
				command: recordingCommand(&calls),
			}
			if err := autotype.simulatePaste(); err != nil {
				t.Fatal(err)
			}
			if want := []commandCall{tt.want}; !reflect.DeepEqual(calls, want) {
				t.Fatalf("command calls = %#v, want %#v", calls, want)
			}
		})
	}
}

func TestNewAutotypeUsesSessionDesktopFallback(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "   ")
	t.Setenv("XDG_SESSION_DESKTOP", "ubuntu")

	autotype, err := NewAutotype(nil)
	if err != nil {
		t.Fatal(err)
	}
	if autotype.session != "wayland" || autotype.desktop != "ubuntu" {
		t.Fatalf("NewAutotype session/desktop = %q/%q", autotype.session, autotype.desktop)
	}
}

func TestNewAutotypePrefersCurrentDesktop(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_CURRENT_DESKTOP", " sway ")
	t.Setenv("XDG_SESSION_DESKTOP", "ubuntu")

	autotype, err := NewAutotype(nil)
	if err != nil {
		t.Fatal(err)
	}
	if autotype.desktop != "sway" {
		t.Fatalf("NewAutotype desktop = %q, want sway", autotype.desktop)
	}
}

func TestNewAutotypeKeepsUnknownDesktopFallback(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "   ")
	t.Setenv("XDG_SESSION_DESKTOP", "\t")

	autotype, err := NewAutotype(nil)
	if err != nil {
		t.Fatal(err)
	}
	if autotype.desktop != "" {
		t.Fatalf("NewAutotype desktop = %q, want empty", autotype.desktop)
	}
	if got := wayland.AutotypeBackendForDesktop(autotype.desktop); got != wayland.AutotypeWtype {
		t.Fatalf("unknown desktop backend = %q, want %q", got, wayland.AutotypeWtype)
	}
}

func TestAutotypeWritesClipboardBeforeInjectingPaste(t *testing.T) {
	clipboard := &autotypeClipboard{}
	var calls []commandCall
	autotype := &Autotype{
		session:   "wayland",
		desktop:   "GNOME",
		clipboard: clipboard,
		command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if !reflect.DeepEqual(clipboard.writes, []string{"中文 🚀\nsecond line"}) {
				t.Fatalf("paste command was created before clipboard write: %#v", clipboard.writes)
			}
			return recordingCommand(&calls)(ctx, name, args...)
		},
	}
	if err := autotype.Type("中文 🚀\nsecond line"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].name != "ydotool" {
		t.Fatalf("command calls = %#v", calls)
	}
}

func TestAutotypeClipboardFailureSkipsPaste(t *testing.T) {
	wantErr := errors.New("clipboard unavailable")
	clipboard := &autotypeClipboard{err: wantErr}
	called := false
	autotype := &Autotype{
		session:   "wayland",
		desktop:   "GNOME",
		clipboard: clipboard,
		command: func(context.Context, string, ...string) *exec.Cmd {
			called = true
			return nil
		},
	}
	if err := autotype.Type("text"); !errors.Is(err, wantErr) {
		t.Fatalf("Type error = %v, want %v", err, wantErr)
	}
	if called {
		t.Fatal("paste command ran after clipboard failure")
	}
}

func TestAutotypeYdotoolFailureNamesBackend(t *testing.T) {
	autotype := &Autotype{
		session: "wayland",
		desktop: "GNOME",
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "false")
		},
	}
	err := autotype.simulatePaste()
	if err == nil || !strings.Contains(err.Error(), "simulate paste with ydotool") {
		t.Fatalf("simulatePaste error = %v", err)
	}
}

func TestAutotypeReturnsPasteFailureAfterClipboardWrite(t *testing.T) {
	clipboard := &autotypeClipboard{}
	autotype := &Autotype{
		session:   "wayland",
		desktop:   "GNOME",
		clipboard: clipboard,
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "false")
		},
	}
	err := autotype.Type("kept on clipboard")
	if err == nil || !strings.Contains(err.Error(), "ydotool") {
		t.Fatalf("Type error = %v", err)
	}
	if !reflect.DeepEqual(clipboard.writes, []string{"kept on clipboard"}) {
		t.Fatalf("clipboard writes = %#v", clipboard.writes)
	}
}

func TestAutotypeRejectsUnknownSession(t *testing.T) {
	autotype := &Autotype{session: "tty"}
	if err := autotype.simulatePaste(); err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Fatalf("simulatePaste error = %v", err)
	}
}
