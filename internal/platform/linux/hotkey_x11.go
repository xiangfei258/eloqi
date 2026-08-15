//go:build linux

package linux

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
#include <X11/X.h>
#include <X11/XKBlib.h>
#include <X11/Xproto.h>
#include <string.h>

// XGrabKey reports conflicts (BadAccess) through Xlib's asynchronous error
// handler, not through its return value.  The Go event loop is the sole owner
// of this Display, so it can temporarily install a handler and force a round
// trip to turn that asynchronous error into a synchronous result.
static int eloqi_x11_grab_error;
static Display *eloqi_x11_grab_display;
static unsigned long eloqi_x11_grab_serial;
static XErrorHandler eloqi_x11_previous_error_handler;

static int eloqi_x11_grab_error_handler(Display *display, XErrorEvent *event) {
    if (display == eloqi_x11_grab_display &&
        event->serial == eloqi_x11_grab_serial &&
        event->request_code == X_GrabKey) {
        eloqi_x11_grab_error = event->error_code;
        return 0;
    }
    if (eloqi_x11_previous_error_handler != NULL &&
        eloqi_x11_previous_error_handler != eloqi_x11_grab_error_handler) {
        return eloqi_x11_previous_error_handler(display, event);
    }
    return 0;
}

static int eloqi_x11_checked_grab_key(Display *display, int keycode,
                                      unsigned int modifiers, Window root) {
    // Drain errors from older requests before installing the temporary,
    // process-wide Xlib handler.
    XSync(display, False);

    eloqi_x11_grab_error = 0;
    eloqi_x11_grab_display = display;
    eloqi_x11_grab_serial = NextRequest(display);
    XErrorHandler previous = XSetErrorHandler(eloqi_x11_grab_error_handler);
    eloqi_x11_previous_error_handler = previous;
    XGrabKey(display, keycode, modifiers, root, True,
             GrabModeAsync, GrabModeAsync);
    XSync(display, False);
    XSetErrorHandler(previous);
    eloqi_x11_grab_display = NULL;
    eloqi_x11_grab_serial = 0;
    eloqi_x11_previous_error_handler = NULL;
    return eloqi_x11_grab_error;
}

static int eloqi_x11_keymap_down(const char keys[32], unsigned int keycode) {
    if (keycode == 0 || keycode >= 256) {
        return 0;
    }
    return (((unsigned char)keys[keycode >> 3]) &
            (1U << (keycode & 7))) != 0;
}

static unsigned int eloqi_x11_query_modifier_mask(
        Display *display,
        unsigned int ctrl_l, unsigned int ctrl_r,
        unsigned int alt_l, unsigned int alt_r,
        unsigned int super_l, unsigned int super_r,
        unsigned int shift_l, unsigned int shift_r) {
    char keys[32];
    XQueryKeymap(display, keys);

    unsigned int state = 0;
    if (eloqi_x11_keymap_down(keys, ctrl_l) ||
        eloqi_x11_keymap_down(keys, ctrl_r)) {
        state |= ControlMask;
    }
    if (eloqi_x11_keymap_down(keys, alt_l) ||
        eloqi_x11_keymap_down(keys, alt_r)) {
        state |= Mod1Mask;
    }
    if (eloqi_x11_keymap_down(keys, super_l) ||
        eloqi_x11_keymap_down(keys, super_r)) {
        state |= Mod4Mask;
    }
    if (eloqi_x11_keymap_down(keys, shift_l) ||
        eloqi_x11_keymap_down(keys, shift_r)) {
        state |= ShiftMask;
    }
    return state;
}

static int event_type(XEvent *ev) {
    return ev->type;
}

static unsigned int event_keycode(XEvent *ev) {
    return ((XKeyEvent *)ev)->keycode;
}

static unsigned int event_state(XEvent *ev) {
    return ((XKeyEvent *)ev)->state;
}

static unsigned long event_time(XEvent *ev) {
    return ((XKeyEvent *)ev)->time;
}

static void init_key_event(XEvent *ev, int type, unsigned int keycode,
                           unsigned int state, unsigned long timestamp) {
    XKeyEvent *e = (XKeyEvent *)ev;
    memset(e, 0, sizeof(*e));
    e->type = type;
    e->keycode = keycode;
    e->state = state;
    e->time = timestamp;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// X11 modifier masks (from X11/X.h).
const (
	x11ShiftMask   = 1 << 0 // ShiftMask
	x11LockMask    = 1 << 1 // LockMask (usually CapsLock)
	x11ControlMask = 1 << 2 // ControlMask
	x11Mod1Mask    = 1 << 3 // Mod1Mask (usually Alt)
	x11Mod3Mask    = 1 << 4 // Mod3Mask
	x11Mod2Mask    = 1 << 5 // Mod2Mask (often NumLock)
	x11Mod4Mask    = 1 << 6 // Mod4Mask (usually Super)
	x11Mod5Mask    = 1 << 7 // Mod5Mask (often ScrollLock)
)

// x11TrackedMask contains modifiers Eloqui itself recognizes in a binding.
const x11TrackedMask = x11ShiftMask | x11ControlMask | x11Mod1Mask | x11Mod4Mask

// x11IgnoredMask contains lock-like modifiers that should not change the
// binding's meaning. XGrabKey needs every concrete variant of these masks.
const x11IgnoredMask = x11LockMask | x11Mod2Mask | x11Mod3Mask | x11Mod5Mask

// modifierOnlySettleDelay matches the evdev backend's pragmatic observation
// window for modifier-only chords.
const modifierOnlyX11SettleDelay = 150 * time.Millisecond

type grabKey struct {
	keycode C.KeyCode
	modmask uint
}

type x11CommandKind int

const (
	x11Register x11CommandKind = iota
	x11Unregister
	x11Close
)

type x11Command struct {
	kind x11CommandKind
	key  platform.Key
	resp chan error
}

type modKeysymEntry struct {
	mod    platform.Modifiers
	keysym [2]uint
	mask   uint
}

type x11ModifierLayout struct {
	byCode  map[uint]uint
	ordered [8]uint
}

var modKeysymTable = []modKeysymEntry{
	{platform.ModCtrl, [2]uint{0xFFE3, 0xFFE4}, x11ControlMask}, // Control_L/R
	{platform.ModAlt, [2]uint{0xFFE9, 0xFFEA}, x11Mod1Mask},     // Alt_L/R
	{platform.ModSuper, [2]uint{0xFFEB, 0xFFEC}, x11Mod4Mask},   // Super_L/R
	{platform.ModShift, [2]uint{0xFFE1, 0xFFE2}, x11ShiftMask},  // Shift_L/R
}

// x11Hotkey implements platform.Hotkey using Xlib XGrabKey. The eventLoop
// goroutine is the sole owner of its Display after construction; Register,
// Unregister and Close submit commands to it.
type x11Hotkey struct {
	display *C.Display
	root    C.Window

	detectableRepeat bool
	closed           atomic.Bool
	events           chan platform.KeyEvent
	dispatcher       *x11EventDispatcher
	commands         chan x11Command
	stop             chan struct{}
	done             chan struct{}
	wg               sync.WaitGroup
	eventsOnce       sync.Once
}

// x11KeyEdge is a cgo-free representation of a key event. Keeping the repeat
// filter independent of XEvent makes its ordering guarantees unit-testable.
type x11KeyEdge struct {
	pressed   bool
	keycode   uint
	state     uint
	timestamp uint64
}

// x11RepeatFilter handles the legacy X11 auto-repeat representation: a
// KeyRelease immediately followed by a KeyPress with the same keycode and
// timestamp. The release must be deferred until the next event is known, or a
// false release edge would escape before the pair can be recognized.
type x11RepeatFilter struct {
	enabled bool
	pending *x11KeyEdge
}

// x11ModifierTracker combines X11's pre-edge modifier mask with physical
// keycode references. X11 exposes left and right variants through one mask,
// so clearing that mask solely from a release event would incorrectly drop
// (for example) Ctrl while the other Ctrl key remains held.
type x11ModifierTracker struct {
	state uint
	down  map[uint]uint
	refs  map[uint]int
}

func (t *x11ModifierTracker) apply(
	edge x11KeyEdge,
	modifierCodes map[uint]uint,
	maskStillDown func(uint) bool,
) uint {
	if t.down == nil {
		t.down = make(map[uint]uint)
	}
	if t.refs == nil {
		t.refs = make(map[uint]int)
	}

	state := edge.state & x11TrackedMask
	mask, isModifier := modifierCodes[edge.keycode]
	if isModifier {
		if edge.pressed {
			if _, alreadyDown := t.down[edge.keycode]; !alreadyDown {
				t.down[edge.keycode] = mask
				t.refs[mask]++
			}
			state |= mask
		} else {
			if heldMask, wasDown := t.down[edge.keycode]; wasDown {
				delete(t.down, edge.keycode)
				if t.refs[heldMask] > 0 {
					t.refs[heldMask]--
				}
			}

			stillDown := t.refs[mask] > 0
			if maskStillDown != nil {
				stillDown = maskStillDown(mask)
				if !stillDown {
					// The server snapshot is authoritative and also repairs stale
					// observed references if a release edge was lost.
					for keycode, heldMask := range t.down {
						if heldMask == mask {
							delete(t.down, keycode)
						}
					}
					t.refs[mask] = 0
				}
			}
			if stillDown {
				state |= mask
			} else {
				state &^= mask
			}
		}
	}

	// Known physical references override a stale pre-edge state bit. This is
	// particularly important when one side of a modifier pair is released.
	for heldMask, refs := range t.refs {
		if refs > 0 {
			state |= heldMask
		}
	}
	t.state = state
	return state
}

func (t *x11ModifierTracker) reconcile(snapshot uint) uint {
	snapshot &= x11TrackedMask
	for keycode, mask := range t.down {
		if snapshot&mask == 0 {
			delete(t.down, keycode)
		}
	}
	for mask := range t.refs {
		if snapshot&mask == 0 {
			t.refs[mask] = 0
		}
	}
	t.state = snapshot
	return snapshot
}

func (f *x11RepeatFilter) push(edge x11KeyEdge) []x11KeyEdge {
	if !f.enabled {
		return []x11KeyEdge{edge}
	}

	out := make([]x11KeyEdge, 0, 2)
	if f.pending != nil {
		previous := *f.pending
		f.pending = nil
		if !previous.pressed && edge.pressed &&
			previous.keycode == edge.keycode && previous.timestamp == edge.timestamp {
			return out
		}
		out = append(out, previous)
	}

	if !edge.pressed {
		deferred := edge
		f.pending = &deferred
		return out
	}
	return append(out, edge)
}

func (f *x11RepeatFilter) flush() []x11KeyEdge {
	if f.pending == nil {
		return nil
	}
	edge := *f.pending
	f.pending = nil
	return []x11KeyEdge{edge}
}

var _ platform.Hotkey = (*x11Hotkey)(nil)

// XSetErrorHandler and the C trap state are process-global, even when callers
// use different Display connections. Serialize checked grabs so concurrent
// x11Hotkey instances cannot replace each other's temporary handler.
var x11GrabErrorTrapMu sync.Mutex

var (
	x11InitThreadsOnce sync.Once
	x11ThreadsReady    bool
)

func ensureX11Threads() bool {
	x11InitThreadsOnce.Do(func() {
		x11ThreadsReady = C.XInitThreads() != 0
	})
	return x11ThreadsReady
}

// newX11Hotkey opens the X display, enables detectable auto-repeat and starts
// the single-owner event loop.
func newX11Hotkey() (*x11Hotkey, error) {
	if !ensureX11Threads() {
		return nil, fmt.Errorf("x11 hotkey: Xlib thread initialization failed")
	}

	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return nil, fmt.Errorf("x11 hotkey: cannot open display (is DISPLAY set?)")
	}

	// Detectable auto-repeat suppresses synthetic release/press pairs while a
	// key is held. It is supported by all mainstream X servers; if unavailable,
	// eventLoop's timestamp fallback still filters the common same-tick pair.
	var supported C.Bool
	detectableRepeat := C.XkbSetDetectableAutoRepeat(dpy, 1, &supported) != 0 && supported != 0

	h := &x11Hotkey{
		display:          dpy,
		root:             C.XDefaultRootWindow(dpy),
		detectableRepeat: detectableRepeat,
		events:           make(chan platform.KeyEvent, 64),
		commands:         make(chan x11Command, 16),
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
	}
	h.dispatcher = newX11EventDispatcher(h.events)
	h.wg.Add(1)
	go h.eventLoop()
	return h, nil
}

func (h *x11Hotkey) modifierLayout() x11ModifierLayout {
	layout := x11ModifierLayout{byCode: make(map[uint]uint, len(modKeysymTable)*2)}
	index := 0
	for _, entry := range modKeysymTable {
		for _, keysym := range entry.keysym {
			keycode := uint(C.XKeysymToKeycode(h.display, C.KeySym(keysym)))
			layout.ordered[index] = keycode
			index++
			if keycode != 0 {
				layout.byCode[keycode] = entry.mask
			}
		}
	}
	return layout
}

func (h *x11Hotkey) queryModifierState(layout x11ModifierLayout) uint {
	codes := layout.ordered
	return uint(C.eloqi_x11_query_modifier_mask(
		h.display,
		C.uint(codes[0]), C.uint(codes[1]),
		C.uint(codes[2]), C.uint(codes[3]),
		C.uint(codes[4]), C.uint(codes[5]),
		C.uint(codes[6]), C.uint(codes[7]),
	)) & x11TrackedMask
}

// Register grabs a key combination globally.
func (h *x11Hotkey) Register(key platform.Key) error {
	return h.submit(x11Command{kind: x11Register, key: key})
}

// Unregister removes a previously registered key.
func (h *x11Hotkey) Unregister(key platform.Key) error {
	return h.submit(x11Command{kind: x11Unregister, key: key})
}

// Events returns the channel delivering hotkey edge events.
func (h *x11Hotkey) Events() <-chan platform.KeyEvent {
	return h.events
}

// Close ungrabs all keys, stops the event loop and closes the display.
func (h *x11Hotkey) Close() error {
	if !h.closed.CompareAndSwap(false, true) {
		h.wg.Wait()
		if h.dispatcher != nil {
			h.dispatcher.close()
		} else {
			h.eventsOnce.Do(func() { close(h.events) })
		}
		return nil
	}
	// Stop timers and legacy fallback emitters before asking the Xlib owner to
	// release its grabs. The dispatcher keeps public Events backpressure out of
	// this command path.
	close(h.stop)
	err := h.submit(x11Command{kind: x11Close})
	h.wg.Wait()
	if h.dispatcher != nil {
		h.dispatcher.close()
	} else {
		h.eventsOnce.Do(func() { close(h.events) })
	}
	return err
}

func (h *x11Hotkey) submit(cmd x11Command) error {
	if h.closed.Load() && cmd.kind != x11Close {
		return fmt.Errorf("x11 hotkey: closed")
	}
	cmd.resp = make(chan error, 1)
	select {
	case h.commands <- cmd:
		return waitX11CommandResponse(cmd.resp, h.done)
	case <-h.done:
		return fmt.Errorf("x11 hotkey: event loop stopped")
	}
}

func waitX11CommandResponse(resp <-chan error, done <-chan struct{}) error {
	select {
	case err := <-resp:
		return err
	case <-done:
		// A successfully handled close command sends its buffered response and
		// then exits the loop. If both channels are ready, prefer that response
		// over reporting a spurious event-loop failure.
		select {
		case err := <-resp:
			return err
		default:
			return fmt.Errorf("x11 hotkey: event loop stopped")
		}
	}
}

// eventLoop owns all mutable grab state and all post-construction Xlib calls.
func (h *x11Hotkey) eventLoop() {
	defer h.wg.Done()
	defer close(h.done)
	if h.dispatcher != nil {
		defer h.dispatcher.close()
	}

	grabs := make(map[grabKey]platform.Key)
	ownedGrabs := make(map[platform.Key][]grabKey)
	activeCode := make(map[uint]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	modifierLayout := h.modifierLayout()
	modifierCodes := modifierLayout.byCode
	physicalCodes := make(map[platform.Key]map[uint]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 16)
	repeatFilter := x11RepeatFilter{enabled: !h.detectableRepeat}
	modifierState := &x11ModifierTracker{}
	queryModifierState := func() uint {
		return h.queryModifierState(modifierLayout)
	}
	dispatchEdge := func(edge x11KeyEdge) {
		h.handleXKeyEdge(
			edge, grabs, activeCode, activeModOnly, modifierCodes, physicalCodes,
			pending, committed, modifierState, func(mask uint) bool {
				return queryModifierState()&mask != 0
			},
		)
	}
	reconcileModifierState := func() {
		if len(activeCode) == 0 && len(activeModOnly) == 0 && len(pending) == 0 {
			return
		}
		state := modifierState.reconcile(queryModifierState())
		h.invalidateModifierOnlyOnChange(state, activeModOnly, pending)
		h.releaseActiveOnChange(state, activeCode, activeModOnly)
	}

	stopTimers := func() {
		for _, timer := range pending {
			timer.Stop()
		}
	}
	defer stopTimers()

	handleCommand := func(cmd x11Command) bool {
		switch cmd.kind {
		case x11Register:
			err := h.grabBinding(cmd.key, grabs, ownedGrabs, physicalCodes)
			cmd.resp <- err
		case x11Unregister:
			for _, gk := range ownedGrabs[cmd.key] {
				h.ungrabOne(gk)
				delete(grabs, gk)
			}
			// Make Unregister observable across other X11 client connections
			// before reporting success.
			C.XSync(h.display, 0)
			delete(ownedGrabs, cmd.key)
			delete(physicalCodes, cmd.key)
			clearX11BindingState(cmd.key, activeCode, activeModOnly, pending)
			cmd.resp <- nil
		case x11Close:
			for gk := range grabs {
				h.ungrabOne(gk)
			}
			cmd.resp <- nil
			stopTimers()
			C.XCloseDisplay(h.display)
			return true
		}
		return false
	}

	for {
		// Command requests have priority so Register/Close cannot be starved by
		// a dense event stream.
		select {
		case cmd := <-h.commands:
			if handleCommand(cmd) {
				return
			}
			continue
		default:
		}

		var timerFired platform.Key
		select {
		case cmd := <-h.commands:
			if handleCommand(cmd) {
				return
			}
			continue
		case key := <-committed:
			timerFired = key
		case <-time.After(15 * time.Millisecond):
		}

		if timerFired != (platform.Key{}) {
			reconcileModifierState()
			timer, waiting := pending[timerFired]
			if waiting {
				timer.Stop()
				delete(pending, timerFired)
				if !activeModOnly[timerFired] && modsToX11Mask(timerFired.Mods) == modifierState.state {
					activeModOnly[timerFired] = true
					h.emit(platform.KeyEvent{Key: timerFired, Pressed: true})
				}
			}
			continue
		}

		if C.XPending(h.display) == 0 {
			for _, edge := range repeatFilter.flush() {
				dispatchEdge(edge)
			}
			reconcileModifierState()
			continue
		}
		var ev C.XEvent
		C.XNextEvent(h.display, &ev)
		edge, ok := x11KeyEdgeFromEvent(&ev)
		if !ok {
			continue
		}
		for _, filtered := range repeatFilter.push(edge) {
			dispatchEdge(filtered)
		}
	}
}

// grabBinding installs all XGrabKey variants for key. It rolls back grabs it
// added if any request fails.
func (h *x11Hotkey) grabBinding(
	key platform.Key,
	grabs map[grabKey]platform.Key,
	owned map[platform.Key][]grabKey,
	physicalCodes map[platform.Key]map[uint]bool,
) error {
	if _, exists := owned[key]; exists {
		return fmt.Errorf("x11 hotkey: already registered: %s", key)
	}

	base := modsToX11Mask(key.Mods)
	var toGrab []grabKey
	physical := make(map[uint]bool)

	if key.Code == platform.KeyNone {
		for _, entry := range modKeysymTable {
			if key.Mods&entry.mod == 0 {
				continue
			}
			otherMask := base &^ entry.mask
			for _, ksym := range entry.keysym {
				kc := C.XKeysymToKeycode(h.display, C.KeySym(ksym))
				if kc == 0 {
					continue
				}
				toGrab = append(toGrab, grabKey{keycode: kc, modmask: otherMask})
				physical[uint(kc)] = true
			}
		}
		if len(toGrab) == 0 {
			return fmt.Errorf("x11 hotkey: no keycodes for modifier combo %s", key)
		}
	} else {
		ksym := keyCodeToX11Keysym(key.Code)
		if ksym == 0 {
			return fmt.Errorf("x11 hotkey: unknown key code %q", key.Code)
		}
		kc := C.XKeysymToKeycode(h.display, C.KeySym(ksym))
		if kc == 0 {
			return fmt.Errorf("x11 hotkey: no keycode for keysym %d", ksym)
		}
		toGrab = append(toGrab, grabKey{keycode: kc, modmask: base})
		physical[uint(kc)] = true
	}

	candidates := make([]grabKey, 0, len(toGrab)*16)
	seenCandidates := make(map[grabKey]bool)
	for _, gk := range toGrab {
		for _, extra := range expandIgnoredModifiers(gk.modmask) {
			variant := grabKey{keycode: gk.keycode, modmask: extra}
			if seenCandidates[variant] {
				continue
			}
			seenCandidates[variant] = true
			candidates = append(candidates, variant)
		}
	}

	added, err := acquireX11Grabs(candidates, grabs, h.grabOneChecked, h.ungrabOne)
	if err != nil {
		// Ensure all rollback requests reach the server before Register returns.
		C.XSync(h.display, 0)
		return fmt.Errorf("x11 hotkey: cannot grab %s: %w", key, err)
	}

	for _, gk := range added {
		grabs[gk] = key
	}
	owned[key] = append([]grabKey(nil), added...)

	physicalCodes[key] = physical
	C.XSelectInput(h.display, h.root, C.KeyPressMask|C.KeyReleaseMask)
	return nil
}

func (h *x11Hotkey) grabOneChecked(gk grabKey) error {
	x11GrabErrorTrapMu.Lock()
	defer x11GrabErrorTrapMu.Unlock()

	if errorCode := C.eloqi_x11_checked_grab_key(
		h.display, C.int(gk.keycode), C.uint(gk.modmask), h.root,
	); errorCode != 0 {
		return fmt.Errorf("X11 error %d", int(errorCode))
	}
	return nil
}

// acquireX11Grabs applies a set of passive grabs transactionally. It performs
// all in-process conflict checks before touching Xlib, and rolls back every
// successfully installed grab if a later server request fails.
func acquireX11Grabs(
	candidates []grabKey,
	existing map[grabKey]platform.Key,
	grab func(grabKey) error,
	ungrab func(grabKey),
) ([]grabKey, error) {
	seen := make(map[grabKey]bool, len(candidates))
	for _, candidate := range candidates {
		if _, duplicate := existing[candidate]; duplicate {
			return nil, fmt.Errorf("grab already owned")
		}
		if seen[candidate] {
			return nil, fmt.Errorf("duplicate grab candidate")
		}
		seen[candidate] = true
	}

	added := make([]grabKey, 0, len(candidates))
	for _, candidate := range candidates {
		if err := grab(candidate); err != nil {
			for i := len(added) - 1; i >= 0; i-- {
				ungrab(added[i])
			}
			return nil, err
		}
		added = append(added, candidate)
	}
	return added, nil
}

func (h *x11Hotkey) ungrabOne(gk grabKey) {
	C.XUngrabKey(h.display, C.int(gk.keycode), C.uint(gk.modmask), h.root)
}

func x11KeyEdgeFromEvent(ev *C.XEvent) (x11KeyEdge, bool) {
	edge := x11KeyEdge{
		keycode:   uint(C.event_keycode(ev)),
		state:     uint(C.event_state(ev)),
		timestamp: uint64(C.event_time(ev)),
	}
	switch C.event_type(ev) {
	case C.KeyPress:
		edge.pressed = true
	case C.KeyRelease:
		edge.pressed = false
	default:
		return x11KeyEdge{}, false
	}
	return edge, true
}

// clearX11BindingState is the delivery boundary for Unregister. It discards
// provider-owned active state without publishing a release: Voice.Stop calls
// Unregister after its Events consumer has exited, and a synthetic event could
// otherwise wedge the X11 owner loop behind a full channel before cmd.resp.
func clearX11BindingState(
	key platform.Key,
	activeCode map[uint]platform.Key,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
) {
	if timer := pending[key]; timer != nil {
		timer.Stop()
	}
	delete(pending, key)
	delete(activeModOnly, key)
	for physical, binding := range activeCode {
		if binding == key {
			delete(activeCode, physical)
		}
	}
}

// handleXKeyEdge converts X11 key edges while preserving the binding
// associated with the physical press. Release lookup therefore does not
// depend on the modifier state remaining unchanged.
func (h *x11Hotkey) handleXKeyEdge(
	edge x11KeyEdge,
	grabs map[grabKey]platform.Key,
	activeCode map[uint]platform.Key,
	activeModOnly map[platform.Key]bool,
	modifierCodes map[uint]uint,
	physicalCodes map[platform.Key]map[uint]bool,
	pending map[platform.Key]*time.Timer,
	committed chan platform.Key,
	modifierState *x11ModifierTracker,
	modifierMaskStillDown func(uint) bool,
) {
	pressed := edge.pressed
	kc := edge.keycode

	// XKeyEvent.state describes the state before this edge. The tracker applies
	// the edge and, on release, consults a physical keymap snapshot so left and
	// right variants sharing one semantic mask remain exact.
	state := modifierState.apply(edge, modifierCodes, modifierMaskStillDown)

	h.invalidateModifierOnlyOnChange(state, activeModOnly, pending)
	h.releaseActiveOnChange(state, activeCode, activeModOnly)

	if !pressed {
		if binding, active := activeCode[kc]; active {
			delete(activeCode, kc)
			h.emit(platform.KeyEvent{Key: binding, Pressed: false})
			return
		}
		h.cancelModifierOnlyCandidate(kc, pending, physicalCodes)
		return
	}

	lookupState := state
	if ownMask, isModifier := modifierCodes[kc]; isModifier {
		// Xlib reports pre-event state. For a modifier-only grab the grabbed
		// key's own modifier bit is present on release but absent on press; the
		// stored grab always uses the *other* modifiers as its mask.
		lookupState &^= ownMask
	}
	binding, matched := grabs[grabKey{keycode: C.KeyCode(kc), modmask: lookupState}]
	if !matched {
		// A non-modifier key after a committed modifier-only chord invalidates
		// that chord (for example Alt+Super followed quickly by Tab).
		if _, isModifier := modifierCodes[kc]; !isModifier {
			h.releaseAllModifierOnly(activeModOnly, pending)
		}
		return
	}

	if binding.Code == platform.KeyNone {
		if activeModOnly[binding] {
			return
		}
		if _, waiting := pending[binding]; !waiting {
			pending[binding] = time.AfterFunc(modifierOnlyX11SettleDelay, func() {
				select {
				case committed <- binding:
				case <-h.stop:
				}
			})
		}
		return
	}

	if _, active := activeCode[kc]; active {
		return
	}
	activeCode[kc] = binding
	h.emit(platform.KeyEvent{Key: binding, Pressed: true})
}

func (h *x11Hotkey) releaseActiveOnChange(
	state uint,
	activeCode map[uint]platform.Key,
	activeModOnly map[platform.Key]bool,
) {
	for physical, binding := range activeCode {
		if modsToX11Mask(binding.Mods) != state {
			delete(activeCode, physical)
			h.emit(platform.KeyEvent{Key: binding, Pressed: false})
		}
	}
	for key := range activeModOnly {
		if modsToX11Mask(key.Mods) != state {
			delete(activeModOnly, key)
			h.emit(platform.KeyEvent{Key: key, Pressed: false})
		}
	}
}

func (h *x11Hotkey) invalidateModifierOnlyOnChange(
	state uint,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
) {
	for key := range activeModOnly {
		if modsToX11Mask(key.Mods) == state {
			continue
		}
		delete(activeModOnly, key)
		h.emit(platform.KeyEvent{Key: key, Pressed: false})
	}
	for key, timer := range pending {
		if modsToX11Mask(key.Mods) != state {
			timer.Stop()
			delete(pending, key)
		}
	}
}

func (h *x11Hotkey) cancelModifierOnlyCandidate(
	kc uint,
	pending map[platform.Key]*time.Timer,
	physicalCodes map[platform.Key]map[uint]bool,
) {
	for key, codes := range physicalCodes {
		if !codes[kc] {
			continue
		}
		if timer := pending[key]; timer != nil {
			timer.Stop()
			delete(pending, key)
		}
	}
}

func (h *x11Hotkey) releaseAllModifierOnly(
	active map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
) {
	for key, timer := range pending {
		timer.Stop()
		delete(pending, key)
	}
	for key := range active {
		delete(active, key)
		h.emit(platform.KeyEvent{Key: key, Pressed: false})
	}
}

func (h *x11Hotkey) emit(ev platform.KeyEvent) {
	if h.dispatcher != nil {
		h.dispatcher.enqueue(ev)
		return
	}
	select {
	case h.events <- ev:
	case <-h.stop:
	}
}

// expandIgnoredModifiers returns every subset of lock-like modifier masks,
// combined with base. XGrabKey treats unspecified extra modifiers strictly, so
// CapsLock, NumLock and ScrollLock states each need their own passive grab.
func expandIgnoredModifiers(base uint) []uint {
	var out []uint
	for extra := uint(0); extra <= x11IgnoredMask; extra++ {
		if extra&^x11IgnoredMask != 0 {
			continue
		}
		out = append(out, base|extra)
	}
	return out
}

// modsToX11Mask converts platform.Modifiers to an X11 modifier mask.
func modsToX11Mask(mods platform.Modifiers) uint {
	var m uint
	for _, entry := range modKeysymTable {
		if mods&entry.mod != 0 {
			m |= entry.mask
		}
	}
	return m
}

// keyCodeToX11Keysym converts a platform.KeyCode to the corresponding X11
// keysym value.
func keyCodeToX11Keysym(code platform.KeyCode) uint {
	switch code {
	case platform.KeyEscape:
		return 0xFF1B // XK_Escape
	case platform.KeyR:
		return 0x0072 // XK_r
	case platform.KeyTab:
		return 0xFF09 // XK_Tab
	case platform.KeyCapsLock:
		return 0xFFE5 // XK_Caps_Lock
	case platform.KeyLeft:
		return 0xFF51 // XK_Left
	case platform.KeyRight:
		return 0xFF53 // XK_Right
	case platform.KeyUp:
		return 0xFF52 // XK_Up
	case platform.KeyDown:
		return 0xFF54 // XK_Down
	case platform.KeyHome:
		return 0xFF50 // XK_Home
	case platform.KeyEnd:
		return 0xFF57 // XK_End
	case platform.KeyPageUp:
		return 0xFF55 // XK_Prior
	case platform.KeyPageDown:
		return 0xFF56 // XK_Next
	case platform.KeyInsert:
		return 0xFF63 // XK_Insert
	case platform.KeyDelete:
		return 0xFFFF // XK_Delete
	case platform.KeyNum0:
		return 0xFFB0 // XK_KP_0
	case platform.KeyNum1:
		return 0xFFB1 // XK_KP_1
	case platform.KeyNum2:
		return 0xFFB2 // XK_KP_2
	case platform.KeyNum3:
		return 0xFFB3 // XK_KP_3
	case platform.KeyNum4:
		return 0xFFB4 // XK_KP_4
	case platform.KeyNum5:
		return 0xFFB5 // XK_KP_5
	case platform.KeyNum6:
		return 0xFFB6 // XK_KP_6
	case platform.KeyNum7:
		return 0xFFB7 // XK_KP_7
	case platform.KeyNum8:
		return 0xFFB8 // XK_KP_8
	case platform.KeyNum9:
		return 0xFFB9 // XK_KP_9
	}
	s := string(code)
	if len(s) >= 2 && s[0] == 'F' {
		n := 0
		for _, ch := range s[1:] {
			if ch < '0' || ch > '9' {
				return 0
			}
			n = n*10 + int(ch-'0')
		}
		if n >= 1 && n <= 24 {
			return 0xFFBE + uint(n-1)
		}
	}
	return 0
}
