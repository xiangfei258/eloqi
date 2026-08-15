package windows

import (
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestWindowsVirtualKeyRoundTrip(t *testing.T) {
	t.Parallel()
	codes := []platform.KeyCode{
		platform.KeyTab, platform.KeyCapsLock, platform.KeyEscape, platform.KeyR,
		platform.KeyLeft, platform.KeyRight, platform.KeyUp, platform.KeyDown,
		platform.KeyHome, platform.KeyEnd, platform.KeyPageUp, platform.KeyPageDown,
		platform.KeyInsert, platform.KeyDelete,
		platform.KeyNum0, platform.KeyNum5, platform.KeyNum9,
		"F1", "F12", "F24",
	}
	for _, code := range codes {
		code := code
		t.Run(string(code), func(t *testing.T) {
			virtual, ok := windowsVirtualKey(code)
			if !ok {
				t.Fatalf("windowsVirtualKey(%q) was not mapped", code)
			}
			got, ok := windowsKeyCode(virtual)
			if !ok || got != code {
				t.Fatalf("round trip %q -> %#x -> %q, ok=%v", code, virtual, got, ok)
			}
		})
	}
	if _, ok := windowsVirtualKey("F25"); ok {
		t.Fatal("F25 must not be accepted")
	}
	if _, ok := windowsVirtualKey("A"); ok {
		t.Fatal("ordinary A must not be accepted")
	}
}

func TestNormalizeWindowsHookModifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		vk   uint16
		scan uint32
		flag uint32
		want uint16
	}{
		{"left control", vkControl, 0x1D, 0, vkLControl},
		{"right control", vkControl, 0x1D, lowLevelHookExtended, vkRControl},
		{"left alt", vkMenu, 0x38, 0, vkLMenu},
		{"right alt", vkMenu, 0x38, lowLevelHookExtended, vkRMenu},
		{"left shift", vkShift, 0x2A, 0, vkLShift},
		{"right shift", vkShift, 0x36, 0, vkRShift},
		{"ordinary", vkTab, 0x0F, 0, vkTab},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeWindowsHookKey(test.vk, test.scan, test.flag); got != test.want {
				t.Fatalf("got %#x, want %#x", got, test.want)
			}
		})
	}
}

func TestWindowsEdgeMachineRegularChord(t *testing.T) {
	t.Parallel()
	machine := newEdgeMachine()
	key := platform.Key{Mods: platform.ModCtrl, Code: "F1"}
	if err := machine.register(key); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1, 0)
	assertWindowsEvents(t, machine.edge(vkLControl, true, now))
	assertWindowsEvents(t, machine.edge(vkF1, true, now), platform.KeyEvent{Key: key, Pressed: true})
	// The hook and poller may both observe the same physical edge.
	assertWindowsEvents(t, machine.edge(vkF1, true, now))
	// Releasing the modifier first still closes the binding exactly once.
	assertWindowsEvents(t, machine.edge(vkLControl, false, now), platform.KeyEvent{Key: key, Pressed: false})
	assertWindowsEvents(t, machine.edge(vkF1, false, now))
}

func TestWindowsEdgeMachineTracksLeftAndRightModifiers(t *testing.T) {
	t.Parallel()
	machine := newEdgeMachine()
	key := platform.Key{Mods: platform.ModCtrl, Code: "F2"}
	if err := machine.register(key); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2, 0)
	assertWindowsEvents(t, machine.edge(vkLControl, true, now))
	assertWindowsEvents(t, machine.edge(vkRControl, true, now))
	assertWindowsEvents(t, machine.edge(vkF1+1, true, now), platform.KeyEvent{Key: key, Pressed: true})
	assertWindowsEvents(t, machine.edge(vkLControl, false, now))
	assertWindowsEvents(t, machine.edge(vkRControl, false, now), platform.KeyEvent{Key: key, Pressed: false})
}

func TestWindowsModifierOnlyObservationWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(3, 0)
	modifierOnly := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	tab := platform.Key{Mods: platform.ModAlt | platform.ModSuper, Code: platform.KeyTab}

	t.Run("larger chord cancels candidate", func(t *testing.T) {
		machine := newEdgeMachine()
		machine.settle = 10 * time.Millisecond
		if err := machine.register(modifierOnly); err != nil {
			t.Fatal(err)
		}
		if err := machine.register(tab); err != nil {
			t.Fatal(err)
		}
		assertWindowsEvents(t, machine.edge(vkLMenu, true, now))
		assertWindowsEvents(t, machine.edge(vkLWin, true, now))
		assertWindowsEvents(t, machine.edge(vkTab, true, now.Add(5*time.Millisecond)), platform.KeyEvent{Key: tab, Pressed: true})
		assertWindowsEvents(t, machine.commit(now.Add(20*time.Millisecond)))
	})

	t.Run("settled chord emits both edges", func(t *testing.T) {
		machine := newEdgeMachine()
		machine.settle = 10 * time.Millisecond
		if err := machine.register(modifierOnly); err != nil {
			t.Fatal(err)
		}
		assertWindowsEvents(t, machine.edge(vkLMenu, true, now))
		assertWindowsEvents(t, machine.edge(vkLWin, true, now))
		assertWindowsEvents(t, machine.commit(now.Add(10*time.Millisecond)), platform.KeyEvent{Key: modifierOnly, Pressed: true})
		// An unrelated key is observed but never consumed; it only invalidates
		// the active modifier-only chord.
		assertWindowsEvents(t, machine.edge(vkR, true, now.Add(11*time.Millisecond)), platform.KeyEvent{Key: modifierOnly, Pressed: false})
	})
}

func TestWindowsUnregisterClearsActiveEdgeWithoutSyntheticRelease(t *testing.T) {
	t.Parallel()
	machine := newEdgeMachine()
	key := platform.Key{Code: platform.KeyEscape}
	if err := machine.register(key); err != nil {
		t.Fatal(err)
	}
	assertWindowsEvents(t, machine.edge(vkEscape, true, time.Unix(4, 0)), platform.KeyEvent{Key: key, Pressed: true})
	machine.unregister(key)
	if _, registered := machine.registered[key]; registered {
		t.Fatal("binding remained registered")
	}
	if _, active := machine.activeCode[vkEscape]; active {
		t.Fatal("active physical edge survived unregister")
	}
}

func assertWindowsEvents(t *testing.T, got []platform.KeyEvent, want ...platform.KeyEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got events %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}
