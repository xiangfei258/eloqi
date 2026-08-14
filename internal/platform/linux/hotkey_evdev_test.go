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
	activeCode := make(map[uint16]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 1)

	mods |= platform.ModCtrl
	h.handleRawEvent(rawEvent{code: 59, value: 1}, &mods, activeCode, activeModOnly, pending, committed)
	h.handleRawEvent(rawEvent{code: 15, value: 1}, &mods, activeCode, activeModOnly, pending, committed)

	press := expectEvent(t, h.events)
	if !press.Pressed || press.Key != key {
		t.Fatalf("press = %+v, want pressed %+v", press, key)
	}

	// Releasing Ctrl before F1 must still produce exactly one release edge.
	h.handleRawEvent(rawEvent{code: 29, value: 0}, &mods, activeCode, activeModOnly, pending, committed)
	release := expectEvent(t, h.events)
	if release.Pressed || release.Key != key {
		t.Fatalf("release = %+v, want released %+v", release, key)
	}

	// The later F1 release must not produce a second release.
	h.handleRawEvent(rawEvent{code: 15, value: 0}, &mods, activeCode, activeModOnly, pending, committed)
	expectNoEvent(t, h.events)
}

func TestEvdevModifierOnlyCandidateCancelledByNextKey(t *testing.T) {
	key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	h := newTestEvdevHotkey(key)

	var mods platform.Modifiers
	activeCode := make(map[uint16]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 1)

	mods |= platform.ModAlt
	h.handleRawEvent(rawEvent{code: 56, value: 1}, &mods, activeCode, activeModOnly, pending, committed)
	mods |= platform.ModSuper
	h.handleRawEvent(rawEvent{code: 125, value: 1}, &mods, activeCode, activeModOnly, pending, committed)

	if len(pending) != 1 {
		t.Fatalf("pending candidates = %d, want 1", len(pending))
	}

	// Tab arriving inside the settle window cancels the chord before any edge.
	h.handleRawEvent(rawEvent{code: 15, value: 1}, &mods, activeCode, activeModOnly, pending, committed)
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
