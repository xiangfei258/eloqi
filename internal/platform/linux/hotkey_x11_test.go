//go:build linux

package linux

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestX11IgnoredModifierExpansion(t *testing.T) {
	variants := expandIgnoredModifiers(x11ControlMask)
	if len(variants) != 16 {
		t.Fatalf("variant count = %d, want 16", len(variants))
	}
	seen := make(map[uint]bool)
	for _, mask := range variants {
		if mask&^uint(x11ControlMask|x11IgnoredMask) != 0 {
			t.Fatalf("unexpected modifier bits: %#x", mask)
		}
		seen[mask] = true
	}
	for extra := uint(0); extra <= x11IgnoredMask; extra++ {
		if extra&^x11IgnoredMask != 0 {
			continue
		}
		if !seen[x11ControlMask|extra] {
			t.Fatalf("missing lock modifier variant %#x", extra)
		}
	}
}

type x11LogicHarness struct {
	h              *x11Hotkey
	grabs          map[grabKey]platform.Key
	activeCode     map[uint]platform.Key
	activeModOnly  map[platform.Key]bool
	modifierCodes  map[uint]uint
	physicalCodes  map[platform.Key]map[uint]bool
	pending        map[platform.Key]*time.Timer
	committed      chan platform.Key
	modifierState  *x11ModifierTracker
	modifierSample uint
}

func newX11LogicHarness(modifierCodes map[uint]uint) *x11LogicHarness {
	return &x11LogicHarness{
		h: &x11Hotkey{
			events: make(chan platform.KeyEvent, 16),
			stop:   make(chan struct{}),
		},
		grabs:         make(map[grabKey]platform.Key),
		activeCode:    make(map[uint]platform.Key),
		activeModOnly: make(map[platform.Key]bool),
		modifierCodes: modifierCodes,
		physicalCodes: make(map[platform.Key]map[uint]bool),
		pending:       make(map[platform.Key]*time.Timer),
		committed:     make(chan platform.Key, 4),
		modifierState: &x11ModifierTracker{},
	}
}

func (s *x11LogicHarness) edge(edge x11KeyEdge) {
	s.h.handleXKeyEdge(
		edge, s.grabs, s.activeCode, s.activeModOnly, s.modifierCodes,
		s.physicalCodes, s.pending, s.committed, s.modifierState,
		func(mask uint) bool { return s.modifierSample&mask != 0 },
	)
}

func (s *x11LogicHarness) stopTimers() {
	for _, timer := range s.pending {
		timer.Stop()
	}
}

func expectX11Event(t *testing.T, events <-chan platform.KeyEvent) platform.KeyEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for X11 hotkey event")
		return platform.KeyEvent{}
	}
}

func expectNoX11Event(t *testing.T, events <-chan platform.KeyEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected X11 hotkey event: %+v", event)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestX11RegularBindingReleasesWhenModifierReleasesFirst(t *testing.T) {
	const (
		leftCtrl = 37
		tab      = 23
	)
	key := platform.Key{Mods: platform.ModCtrl, Code: platform.KeyTab}
	s := newX11LogicHarness(map[uint]uint{leftCtrl: x11ControlMask})
	s.grabs[grabKey{keycode: tab, modmask: x11ControlMask}] = key

	// The Ctrl press happened before the passive Tab grab and was therefore not
	// observed. XKeyEvent.state still establishes the initial Ctrl state.
	s.edge(x11KeyEdge{pressed: true, keycode: tab, state: x11ControlMask})
	if event := expectX11Event(t, s.h.events); !event.Pressed || event.Key != key {
		t.Fatalf("press = %+v, want pressed %+v", event, key)
	}

	// A physical snapshot after the release confirms that no Ctrl remains.
	s.modifierSample = 0
	s.edge(x11KeyEdge{pressed: false, keycode: leftCtrl, state: x11ControlMask})
	if event := expectX11Event(t, s.h.events); event.Pressed || event.Key != key {
		t.Fatalf("release = %+v, want released %+v", event, key)
	}

	// The later Tab release must not emit a duplicate edge.
	s.edge(x11KeyEdge{pressed: false, keycode: tab})
	expectNoX11Event(t, s.h.events)
}

func TestX11PreheldLeftAndRightModifierUsesPhysicalSnapshot(t *testing.T) {
	const (
		leftCtrl  = 37
		rightCtrl = 105
		tab       = 23
	)
	key := platform.Key{Mods: platform.ModCtrl, Code: platform.KeyTab}
	s := newX11LogicHarness(map[uint]uint{
		leftCtrl: x11ControlMask, rightCtrl: x11ControlMask,
	})
	s.grabs[grabKey{keycode: tab, modmask: x11ControlMask}] = key

	s.edge(x11KeyEdge{pressed: true, keycode: tab, state: x11ControlMask})
	expectX11Event(t, s.h.events)

	// Both Ctrl presses preceded passive-grab activation. Releasing the left
	// side must preserve Ctrl because the keymap still reports the right side.
	s.modifierSample = x11ControlMask
	s.edge(x11KeyEdge{pressed: false, keycode: leftCtrl, state: x11ControlMask})
	expectNoX11Event(t, s.h.events)

	s.modifierSample = 0
	s.edge(x11KeyEdge{pressed: false, keycode: rightCtrl, state: x11ControlMask})
	if event := expectX11Event(t, s.h.events); event.Pressed || event.Key != key {
		t.Fatalf("last-side release = %+v, want released %+v", event, key)
	}
}

func TestX11ObservedLeftAndRightModifierReferences(t *testing.T) {
	const (
		leftCtrl  = 37
		rightCtrl = 105
	)
	codes := map[uint]uint{leftCtrl: x11ControlMask, rightCtrl: x11ControlMask}
	tracker := &x11ModifierTracker{}

	tracker.apply(x11KeyEdge{pressed: true, keycode: leftCtrl}, codes, nil)
	tracker.apply(x11KeyEdge{pressed: true, keycode: rightCtrl, state: x11ControlMask}, codes, nil)
	if state := tracker.apply(
		x11KeyEdge{pressed: false, keycode: leftCtrl, state: x11ControlMask}, codes, nil,
	); state != x11ControlMask {
		t.Fatalf("state after left release = %#x, want Ctrl", state)
	}
	if state := tracker.apply(
		x11KeyEdge{pressed: false, keycode: rightCtrl, state: x11ControlMask}, codes, nil,
	); state != 0 {
		t.Fatalf("state after both releases = %#x, want 0", state)
	}
}

func TestX11ModifierOnlyCandidateCancelledByExtraModifier(t *testing.T) {
	const (
		leftAlt   = 64
		leftSuper = 133
		leftCtrl  = 37
	)
	key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	s := newX11LogicHarness(map[uint]uint{
		leftAlt: x11Mod1Mask, leftSuper: x11Mod4Mask, leftCtrl: x11ControlMask,
	})
	s.grabs[grabKey{keycode: leftSuper, modmask: x11Mod1Mask}] = key
	s.physicalCodes[key] = map[uint]bool{leftAlt: true, leftSuper: true}
	defer s.stopTimers()

	s.edge(x11KeyEdge{pressed: true, keycode: leftSuper, state: x11Mod1Mask})
	if len(s.pending) != 1 {
		t.Fatalf("pending candidates = %d, want 1", len(s.pending))
	}

	s.edge(x11KeyEdge{
		pressed: true,
		keycode: leftCtrl,
		state:   x11Mod1Mask | x11Mod4Mask,
	})
	if len(s.pending) != 0 {
		t.Fatal("Alt+Super candidate survived an additional Ctrl press")
	}
	expectNoX11Event(t, s.h.events)
}

func TestX11ModifierPollingReleasesWithoutAnotherEvent(t *testing.T) {
	key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	s := newX11LogicHarness(nil)
	s.activeModOnly[key] = true
	s.modifierState.state = x11Mod1Mask | x11Mod4Mask

	state := s.modifierState.reconcile(x11Mod1Mask)
	s.h.invalidateModifierOnlyOnChange(state, s.activeModOnly, s.pending)
	s.h.releaseActiveOnChange(state, s.activeCode, s.activeModOnly)

	if event := expectX11Event(t, s.h.events); event.Pressed || event.Key != key {
		t.Fatalf("polled release = %+v, want released %+v", event, key)
	}
}

func TestX11RepeatFilterSuppressesLegacyPair(t *testing.T) {
	filter := x11RepeatFilter{enabled: true}
	release := x11KeyEdge{keycode: 42, timestamp: 100, pressed: false}
	press := x11KeyEdge{keycode: 42, timestamp: 100, pressed: true}

	if got := filter.push(release); len(got) != 0 {
		t.Fatalf("release was emitted before repeat pair was known: %#v", got)
	}
	if got := filter.push(press); len(got) != 0 {
		t.Fatalf("legacy repeat pair emitted edges: %#v", got)
	}
	if got := filter.flush(); len(got) != 0 {
		t.Fatalf("suppressed repeat left a deferred edge: %#v", got)
	}
}

func TestX11RepeatFilterPreservesRealEdgesInOrder(t *testing.T) {
	filter := x11RepeatFilter{enabled: true}
	release := x11KeyEdge{keycode: 42, timestamp: 100, pressed: false}
	otherPress := x11KeyEdge{keycode: 43, timestamp: 101, pressed: true}

	if got := filter.push(release); len(got) != 0 {
		t.Fatalf("release was not deferred: %#v", got)
	}
	if got := filter.push(otherPress); !reflect.DeepEqual(got, []x11KeyEdge{release, otherPress}) {
		t.Fatalf("edges = %#v, want release then unrelated press", got)
	}

	finalRelease := x11KeyEdge{keycode: 43, timestamp: 102, pressed: false}
	filter.push(finalRelease)
	if got := filter.flush(); !reflect.DeepEqual(got, []x11KeyEdge{finalRelease}) {
		t.Fatalf("flushed edges = %#v, want final release", got)
	}
}

func TestX11RepeatFilterDisabledDoesNotDelayRelease(t *testing.T) {
	filter := x11RepeatFilter{enabled: false}
	release := x11KeyEdge{keycode: 42, timestamp: 100, pressed: false}
	if got := filter.push(release); !reflect.DeepEqual(got, []x11KeyEdge{release}) {
		t.Fatalf("edges = %#v, want immediate release", got)
	}
}

func TestWaitX11CommandResponsePrefersBufferedResponseAfterDone(t *testing.T) {
	for range 1000 {
		resp := make(chan error, 1)
		done := make(chan struct{})
		resp <- nil
		close(done)
		if err := waitX11CommandResponse(resp, done); err != nil {
			t.Fatalf("response/done race returned %v, want nil", err)
		}
	}
}

func TestX11CloseWaitsAndClosesEventsAfterSubmitFailure(t *testing.T) {
	h := &x11Hotkey{
		events:   make(chan platform.KeyEvent),
		commands: make(chan x11Command, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	commandReceived := make(chan struct{})
	allowLoopExit := make(chan struct{})
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		<-h.commands
		close(commandReceived)
		close(h.done)
		<-allowLoopExit
	}()

	result := make(chan error, 1)
	go func() { result <- h.Close() }()
	<-commandReceived

	select {
	case err := <-result:
		t.Fatalf("Close returned before the event loop exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case _, open := <-h.events:
		if !open {
			t.Fatal("events closed before the event loop exited")
		}
	default:
	}

	close(allowLoopExit)
	if err := <-result; err == nil {
		t.Fatal("Close returned nil after the loop stopped without a response")
	}
	if _, open := <-h.events; open {
		t.Fatal("events remained open after Close returned")
	}
}

func TestX11CloseHandlesResponseDoneRace(t *testing.T) {
	h := &x11Hotkey{
		events:   make(chan platform.KeyEvent),
		commands: make(chan x11Command, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		cmd := <-h.commands
		cmd.resp <- nil
		close(h.done)
	}()

	if err := h.Close(); err != nil {
		t.Fatalf("Close returned %v, want nil", err)
	}
	if _, open := <-h.events; open {
		t.Fatal("events remained open after successful Close")
	}
}

func TestAcquireX11GrabsRollsBackInReverseOrder(t *testing.T) {
	candidates := []grabKey{
		{keycode: 10, modmask: 1},
		{keycode: 11, modmask: 2},
		{keycode: 12, modmask: 3},
	}
	var grabbed []grabKey
	var ungrabbed []grabKey
	wantErr := errors.New("BadAccess")

	added, err := acquireX11Grabs(
		candidates,
		map[grabKey]platform.Key{},
		func(candidate grabKey) error {
			if candidate == candidates[2] {
				return wantErr
			}
			grabbed = append(grabbed, candidate)
			return nil
		},
		func(candidate grabKey) { ungrabbed = append(ungrabbed, candidate) },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if added != nil {
		t.Fatalf("added = %#v, want nil after rollback", added)
	}
	if !reflect.DeepEqual(grabbed, candidates[:2]) {
		t.Fatalf("grabbed = %#v, want %#v", grabbed, candidates[:2])
	}
	wantUngrabbed := []grabKey{candidates[1], candidates[0]}
	if !reflect.DeepEqual(ungrabbed, wantUngrabbed) {
		t.Fatalf("ungrabbed = %#v, want %#v", ungrabbed, wantUngrabbed)
	}
}

func TestAcquireX11GrabsPreflightsOwnershipConflict(t *testing.T) {
	candidate := grabKey{keycode: 10, modmask: 1}
	grabCalled := false
	_, err := acquireX11Grabs(
		[]grabKey{candidate},
		map[grabKey]platform.Key{candidate: {Code: platform.KeyTab}},
		func(grabKey) error {
			grabCalled = true
			return nil
		},
		func(grabKey) {},
	)
	if err == nil {
		t.Fatal("ownership conflict returned nil error")
	}
	if grabCalled {
		t.Fatal("server grab attempted before ownership preflight completed")
	}
}

func TestX11EmitUnblocksWhenStopCloses(t *testing.T) {
	h := &x11Hotkey{
		events: make(chan platform.KeyEvent, 1),
		stop:   make(chan struct{}),
	}
	h.events <- platform.KeyEvent{}
	returned := make(chan struct{})
	go func() {
		h.emit(platform.KeyEvent{Pressed: true})
		close(returned)
	}()

	select {
	case <-returned:
		t.Fatal("emit returned while the events channel was full")
	case <-time.After(20 * time.Millisecond):
	}

	close(h.stop)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("emit remained blocked after stop closed")
	}
}

func TestX11RegisterReportsServerGrabConflict(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("requires an X11 display")
	}

	first, err := newX11Hotkey()
	if err != nil {
		t.Fatalf("open first X11 hotkey: %v", err)
	}
	defer first.Close()
	second, err := newX11Hotkey()
	if err != nil {
		t.Fatalf("open second X11 hotkey: %v", err)
	}
	defer second.Close()

	key := platform.Key{Code: platform.KeyTab}
	type registerResult struct {
		hotkey *x11Hotkey
		err    error
	}
	results := make(chan registerResult, 2)
	for _, hotkey := range []*x11Hotkey{first, second} {
		go func() {
			results <- registerResult{hotkey: hotkey, err: hotkey.Register(key)}
		}()
	}

	resultA, resultB := <-results, <-results
	if (resultA.err == nil) == (resultB.err == nil) {
		t.Fatalf("concurrent Register errors = (%v, %v), want exactly one conflict", resultA.err, resultB.err)
	}
	winner, loser := resultA, resultB
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if err := winner.hotkey.Unregister(key); err != nil {
		t.Fatalf("winner Unregister: %v", err)
	}
	if err := loser.hotkey.Register(key); err != nil {
		t.Fatalf("loser Register after conflict rollback: %v", err)
	}
}
