package darwin

import (
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestDarwinVirtualKeyRoundTrip(t *testing.T) {
	t.Parallel()
	codes := []platform.KeyCode{
		platform.KeyTab, platform.KeyCapsLock, platform.KeyEscape, platform.KeyR,
		platform.KeyLeft, platform.KeyRight, platform.KeyUp, platform.KeyDown,
		platform.KeyHome, platform.KeyEnd, platform.KeyPageUp, platform.KeyPageDown,
		platform.KeyInsert, platform.KeyDelete,
		platform.KeyNum0, platform.KeyNum5, platform.KeyNum9,
		"F1", "F12", "F20",
	}
	for _, code := range codes {
		virtual, ok := darwinVirtualKey(code)
		if !ok {
			t.Fatalf("darwinVirtualKey(%q) was not mapped", code)
		}
		got, ok := darwinVirtualToCode(virtual)
		if !ok || got != code {
			t.Fatalf("round trip %q -> %#x -> %q, ok=%v", code, virtual, got, ok)
		}
	}
	// Apple's documented virtual-key table ends at F20. Returning an explicit
	// registration error is safer than inventing keycodes for F21-F24.
	if _, ok := darwinVirtualKey("F21"); ok {
		t.Fatal("F21 must not be mapped to an undocumented virtual keycode")
	}
}

func TestDarwinEdgeMachineChordAndModifierOnly(t *testing.T) {
	t.Parallel()
	now := time.Unix(5, 0)

	t.Run("ordinary chord", func(t *testing.T) {
		machine := newDarwinEdgeMachine()
		key := platform.Key{Mods: platform.ModSuper, Code: platform.KeyR}
		if err := machine.register(key); err != nil {
			t.Fatal(err)
		}
		assertDarwinEvents(t, machine.edge(0x37, true, now))
		assertDarwinEvents(t, machine.edge(0x0F, true, now), platform.KeyEvent{Key: key, Pressed: true})
		assertDarwinEvents(t, machine.edge(0x37, false, now), platform.KeyEvent{Key: key, Pressed: false})
	})

	t.Run("modifier only cancellation", func(t *testing.T) {
		machine := newDarwinEdgeMachine()
		machine.settle = 10 * time.Millisecond
		key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
		if err := machine.register(key); err != nil {
			t.Fatal(err)
		}
		assertDarwinEvents(t, machine.edge(0x3A, true, now))
		assertDarwinEvents(t, machine.edge(0x37, true, now))
		assertDarwinEvents(t, machine.edge(0x30, true, now.Add(5*time.Millisecond)))
		assertDarwinEvents(t, machine.commit(now.Add(20*time.Millisecond)))
	})

	t.Run("modifier only settled", func(t *testing.T) {
		machine := newDarwinEdgeMachine()
		machine.settle = 10 * time.Millisecond
		key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
		if err := machine.register(key); err != nil {
			t.Fatal(err)
		}
		assertDarwinEvents(t, machine.edge(0x3A, true, now))
		assertDarwinEvents(t, machine.edge(0x37, true, now))
		assertDarwinEvents(t, machine.commit(now.Add(10*time.Millisecond)), platform.KeyEvent{Key: key, Pressed: true})
		assertDarwinEvents(t, machine.edge(0x37, false, now.Add(11*time.Millisecond)), platform.KeyEvent{Key: key, Pressed: false})
	})
}

func TestDarwinUnregisterClearsActiveStateWhenEventsAreFull(t *testing.T) {
	t.Parallel()
	key := platform.Key{Mods: platform.ModSuper, Code: platform.KeyR}
	machine := newDarwinEdgeMachine()
	if err := machine.register(key); err != nil {
		t.Fatal(err)
	}
	machine.activeCode[0x0F] = key
	machine.activeMods[key] = true
	machine.pending[key] = time.Now().Add(time.Hour)

	// This models the Voice.Stop boundary: its Events consumer has already
	// exited and a final public event occupies the entire channel.
	events := make(chan platform.KeyEvent, 1)
	events <- platform.KeyEvent{Key: key, Pressed: true}
	returned := make(chan struct{})
	go func() {
		machine.unregister(key)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("unregister blocked behind a full public events channel")
	}
	if _, registered := machine.registered[key]; registered {
		t.Fatal("binding remained registered")
	}
	if len(machine.activeCode) != 0 || len(machine.activeMods) != 0 || len(machine.pending) != 0 {
		t.Fatalf("active state survived unregister: code=%v mods=%v pending=%v", machine.activeCode, machine.activeMods, machine.pending)
	}
}

func assertDarwinEvents(t *testing.T, got []platform.KeyEvent, want ...platform.KeyEvent) {
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
