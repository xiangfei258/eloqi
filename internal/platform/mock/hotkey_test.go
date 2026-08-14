package mock

import (
	"errors"
	"testing"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestHotkeyRegisterAndEvents(t *testing.T) {
	h := NewHotkey()
	key := platform.Key{Mods: platform.ModCtrl, Code: platform.KeyTab}

	if err := h.Register(key); err != nil {
		t.Fatal(err)
	}
	if !h.Registered(key) {
		t.Fatal("Registered(key) = false, want true")
	}

	press := platform.KeyEvent{Key: key, Pressed: true}
	release := platform.KeyEvent{Key: key, Pressed: false}
	h.Emit(press)
	h.Emit(release)

	events := h.Events()
	if got := <-events; got != press {
		t.Fatalf("event = %#v, want %#v", got, press)
	}
	if got := <-events; got != release {
		t.Fatalf("event = %#v, want %#v", got, release)
	}
}

func TestHotkeyDuplicateRegister(t *testing.T) {
	h := NewHotkey()
	key := platform.Key{Mods: platform.ModAlt}
	if err := h.Register(key); err != nil {
		t.Fatal(err)
	}
	if err := h.Register(key); err == nil {
		t.Fatal("duplicate Register should fail")
	}
}

func TestHotkeyUnregister(t *testing.T) {
	h := NewHotkey()
	key := platform.Key{Code: platform.KeyCapsLock}
	if err := h.Register(key); err != nil {
		t.Fatal(err)
	}
	if err := h.Unregister(key); err != nil {
		t.Fatal(err)
	}
	if h.Registered(key) {
		t.Fatal("Registered(key) = true after Unregister, want false")
	}
}

func TestHotkeyInjectedErrors(t *testing.T) {
	f1, _ := platform.FunctionKey(1)
	key := platform.Key{Code: f1}

	t.Run("register", func(t *testing.T) {
		want := errors.New("register failed")
		h := NewHotkey()
		h.RegisterErr = want
		if err := h.Register(key); err != want {
			t.Fatalf("Register err = %v, want %v", err, want)
		}
	})
	t.Run("unregister", func(t *testing.T) {
		want := errors.New("unregister failed")
		h := NewHotkey()
		h.UnregisterErr = want
		if err := h.Unregister(key); err != want {
			t.Fatalf("Unregister err = %v, want %v", err, want)
		}
	})
}

func TestHotkeyClose(t *testing.T) {
	h := NewHotkey()
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if !h.Closed() {
		t.Fatal("Closed() = false, want true")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close should be idempotent, got %v", err)
	}

	// Registering after close fails.
	if err := h.Register(platform.Key{Code: platform.KeyTab}); err == nil {
		t.Fatal("Register after Close should fail")
	}
}
