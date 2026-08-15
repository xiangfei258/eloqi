//go:build linux

package linux

import (
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func newTestEvdevHotkey(keys ...platform.Key) *evdevHotkey {
	h := &evdevHotkey{
		registered: make(map[platform.Key]bool),
		events:     make(chan platform.KeyEvent, 64),
		done:       make(chan struct{}),
	}
	for _, key := range keys {
		h.registered[key] = true
	}
	return h
}

func TestEvdevReleaseSurvivesModifierReleasedFirst(t *testing.T) {
	key := platform.Key{Mods: platform.ModCtrl, Code: "F1"}
	h := newTestEvdevHotkey(key)

	var mods platform.Modifiers
	refs := make(map[platform.Modifiers]int)
	activeCode := make(map[uint16]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 1)

	// Press left Ctrl (code 29) so both the modifier bit and its refcount are
	// established through the real code path, then press F1.
	h.handleRawEvent(rawEvent{code: 29, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)
	h.handleRawEvent(rawEvent{code: 59, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)

	press := expectEvent(t, h.events)
	if !press.Pressed || press.Key != key {
		t.Fatalf("press = %+v, want pressed %+v", press, key)
	}

	// Releasing Ctrl before F1 must still produce exactly one release edge.
	h.handleRawEvent(rawEvent{code: 29, value: 0}, &mods, refs, activeCode, activeModOnly, pending, committed)
	release := expectEvent(t, h.events)
	if release.Pressed || release.Key != key {
		t.Fatalf("release = %+v, want released %+v", release, key)
	}

	// The later F1 release must not produce a second release.
	h.handleRawEvent(rawEvent{code: 59, value: 0}, &mods, refs, activeCode, activeModOnly, pending, committed)
	expectNoEvent(t, h.events)
}

func TestEvdevUnregisteredAuxiliaryReleaseDoesNotPoisonNextPress(t *testing.T) {
	key := platform.Key{Code: platform.KeyR}
	h := newTestEvdevHotkey(key)

	var mods platform.Modifiers
	refs := make(map[platform.Modifiers]int)
	activeCode := make(map[uint16]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 1)

	h.handleRawEvent(rawEvent{code: 19, value: keyPress}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if event := expectEvent(t, h.events); !event.Pressed || event.Key != key {
		t.Fatalf("first press = %+v, want pressed %+v", event, key)
	}
	if err := h.Unregister(key); err != nil {
		t.Fatal(err)
	}
	h.handleRawEvent(rawEvent{code: 19, value: keyRelease}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if len(activeCode) != 0 {
		t.Fatalf("active physical keys after unregistered release = %#v", activeCode)
	}
	expectNoEvent(t, h.events)

	if err := h.Register(key); err != nil {
		t.Fatal(err)
	}
	h.handleRawEvent(rawEvent{code: 19, value: keyPress}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if event := expectEvent(t, h.events); !event.Pressed || event.Key != key {
		t.Fatalf("second press = %+v, want pressed %+v", event, key)
	}
}

func TestEvdevUnregisterPurgesPendingAndActiveProcessorState(t *testing.T) {
	key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	h := newTestEvdevHotkey(key)
	activeCode := map[uint16]platform.Key{19: key, 59: {Code: "F1"}}
	activeModOnly := map[platform.Key]bool{key: true}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	pending := map[platform.Key]*time.Timer{key: timer}

	if err := h.applyRegistrationCommand(
		evdevRegistrationCommand{key: key},
		activeCode,
		activeModOnly,
		pending,
	); err != nil {
		t.Fatal(err)
	}
	if h.registered[key] || activeModOnly[key] || pending[key] != nil {
		t.Fatalf("unregister left stale state: registered=%v active=%v pending=%v", h.registered[key], activeModOnly[key], pending[key])
	}
	if _, exists := activeCode[19]; exists {
		t.Fatalf("unregister left active physical binding: %#v", activeCode)
	}
	if _, exists := activeCode[59]; !exists {
		t.Fatalf("unregister removed unrelated active binding: %#v", activeCode)
	}
	if err := h.applyRegistrationCommand(
		evdevRegistrationCommand{key: key, register: true},
		activeCode,
		activeModOnly,
		pending,
	); err != nil {
		t.Fatal(err)
	}
	if !h.registered[key] {
		t.Fatal("binding was not re-registered")
	}
}

func TestEvdevFullPublicQueueDoesNotBlockControlPlaneOrClose(t *testing.T) {
	dispatcher := newEvdevEventDispatcher(1)
	h := &evdevHotkey{
		registered: make(map[platform.Key]bool),
		events:     dispatcher.events,
		rawCh:      make(chan rawEvent, 1),
		commands:   make(chan evdevRegistrationCommand),
		dispatcher: dispatcher,
		done:       make(chan struct{}),
	}
	h.wg.Add(1)
	go h.processEvents()

	// Leave the public channel full and queue more edges behind it. Registration
	// commands must still be processed by the independent state owner.
	for i := 0; i < 8; i++ {
		h.emit(platform.KeyEvent{Key: platform.Key{Code: platform.KeyR}, Pressed: i%2 == 0})
	}
	key := platform.Key{Code: platform.KeyR}
	if err := h.Register(key); err != nil {
		t.Fatal(err)
	}
	if err := h.Unregister(key); err != nil {
		t.Fatal(err)
	}

	closed := make(chan struct{})
	go func() {
		_ = h.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind a full public event queue")
	}
}

func TestEvdevModifierOnlyCandidateCancelledByNextKey(t *testing.T) {
	key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	h := newTestEvdevHotkey(key)

	var mods platform.Modifiers
	refs := make(map[platform.Modifiers]int)
	activeCode := make(map[uint16]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 1)

	mods |= platform.ModAlt
	h.handleRawEvent(rawEvent{code: 56, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)
	mods |= platform.ModSuper
	h.handleRawEvent(rawEvent{code: 125, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)

	if len(pending) != 1 {
		t.Fatalf("pending candidates = %d, want 1", len(pending))
	}

	// Tab arriving inside the settle window cancels the chord before any edge.
	h.handleRawEvent(rawEvent{code: 15, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if len(pending) != 0 {
		t.Fatal("candidate should be cancelled by Tab")
	}
	expectNoEvent(t, h.events)

	// Ensure the timer does not fire into the event channel later.
	time.Sleep(modifierOnlySettleDelay + 30*time.Millisecond)
	expectNoEvent(t, h.events)
}

func expectEvent(t *testing.T, ch <-chan platform.KeyEvent) platform.KeyEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hotkey event")
		return platform.KeyEvent{}
	}
}

func expectNoEvent(t *testing.T, ch <-chan platform.KeyEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestEvdevLeftAndRightModifierShareRefCount(t *testing.T) {
	h := newTestEvdevHotkey()

	var mods platform.Modifiers
	refs := make(map[platform.Modifiers]int)
	activeCode := make(map[uint16]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 1)

	// Left Ctrl then right Ctrl: the semantic Ctrl bit remains set.
	h.handleRawEvent(rawEvent{code: 29, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)
	h.handleRawEvent(rawEvent{code: 97, value: 1}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if mods&platform.ModCtrl == 0 {
		t.Fatal("Ctrl unexpectedly cleared while right Ctrl is held")
	}

	// Releasing only left Ctrl must leave Ctrl active.
	h.handleRawEvent(rawEvent{code: 29, value: 0}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if mods&platform.ModCtrl == 0 {
		t.Fatal("Ctrl cleared while right Ctrl is still held")
	}

	// Releasing right Ctrl finally clears it.
	h.handleRawEvent(rawEvent{code: 97, value: 0}, &mods, refs, activeCode, activeModOnly, pending, committed)
	if mods&platform.ModCtrl != 0 {
		t.Fatal("Ctrl remained set after both physical keys were released")
	}
}
