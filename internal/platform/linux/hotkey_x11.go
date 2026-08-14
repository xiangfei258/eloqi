//go:build linux

package linux

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
#include <X11/X.h>
#include <X11/XKBlib.h>
#include <string.h>

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

var modKeysymTable = []modKeysymEntry{
	{platform.ModCtrl, [2]uint{0xFFE3, 0xFFE4}, x11ControlMask}, // Control_L/R
	{platform.ModAlt, [2]uint{0xFFE9, 0xFFEA}, x11Mod1Mask},     // Alt_L/R
	{platform.ModSuper, [2]uint{0xFFEB, 0xFFEC}, x11Mod4Mask},   // Super_L/R
	{platform.ModShift, [2]uint{0xFFE1, 0xFFE2}, x11ShiftMask},  // Shift_L/R
}

// x11Hotkey implements platform.Hotkey using Xlib XGrabKey. The eventLoop
// goroutine is the sole owner of the Display after construction; Register,
// Unregister and Close submit commands to it. This avoids concurrent Xlib use
// without requiring XInitThreads.
type x11Hotkey struct {
	display *C.Display
	root    C.Window

	closed   atomic.Bool
	events   chan platform.KeyEvent
	commands chan x11Command
	done     chan struct{}
	wg       sync.WaitGroup
}

var _ platform.Hotkey = (*x11Hotkey)(nil)

// newX11Hotkey opens the X display, enables detectable auto-repeat and starts
// the single-owner event loop.
func newX11Hotkey() (*x11Hotkey, error) {
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return nil, fmt.Errorf("x11 hotkey: cannot open display (is DISPLAY set?)")
	}

	// Detectable auto-repeat suppresses synthetic release/press pairs while a
	// key is held. It is supported by all mainstream X servers; if unavailable,
	// eventLoop's timestamp fallback still filters the common same-tick pair.
	var supported C.Bool
	C.XkbSetDetectableAutoRepeat(dpy, 1, &supported)

	h := &x11Hotkey{
		display:  dpy,
		root:     C.XDefaultRootWindow(dpy),
		events:   make(chan platform.KeyEvent, 64),
		commands: make(chan x11Command, 16),
		done:     make(chan struct{}),
	}
	h.wg.Add(1)
	go h.eventLoop()
	return h, nil
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
		return nil
	}
	if err := h.submit(x11Command{kind: x11Close}); err != nil {
		return err
	}
	h.wg.Wait()
	close(h.events)
	return nil
}

func (h *x11Hotkey) submit(cmd x11Command) error {
	if h.closed.Load() && cmd.kind != x11Close {
		return fmt.Errorf("x11 hotkey: closed")
	}
	cmd.resp = make(chan error, 1)
	select {
	case h.commands <- cmd:
		select {
		case err := <-cmd.resp:
			return err
		case <-h.done:
			return fmt.Errorf("x11 hotkey: event loop stopped")
		}
	case <-h.done:
		return fmt.Errorf("x11 hotkey: event loop stopped")
	}
}

// eventLoop owns all mutable grab state and all post-construction Xlib calls.
func (h *x11Hotkey) eventLoop() {
	defer h.wg.Done()
	defer close(h.done)

	grabs := make(map[grabKey]platform.Key)
	ownedGrabs := make(map[platform.Key][]grabKey)
	activeCode := make(map[C.KeyCode]platform.Key)
	activeModOnly := make(map[platform.Key]bool)
	modifierCodes := make(map[C.KeyCode]uint)
	physicalCodes := make(map[platform.Key]map[C.KeyCode]bool)
	pending := make(map[platform.Key]*time.Timer)
	committed := make(chan platform.Key, 16)
	detectableFallback := make(map[C.KeyCode]C.ulong) // keycode -> last release time
	currentState := uint(0)

	stopTimers := func() {
		for _, timer := range pending {
			timer.Stop()
		}
	}
	defer stopTimers()

	for {
		// Command requests have priority so Register/Close cannot be starved by
		// a dense event stream.
		select {
		case cmd := <-h.commands:
			switch cmd.kind {
			case x11Register:
				err := h.grabBinding(cmd.key, grabs, ownedGrabs, modifierCodes, physicalCodes)
				cmd.resp <- err
			case x11Unregister:
				for _, gk := range ownedGrabs[cmd.key] {
					h.ungrabOne(gk)
					delete(grabs, gk)
				}
				delete(ownedGrabs, cmd.key)
				delete(physicalCodes, cmd.key)
				if timer := pending[cmd.key]; timer != nil {
					timer.Stop()
					delete(pending, cmd.key)
				}
				if activeModOnly[cmd.key] {
					delete(activeModOnly, cmd.key)
					h.emit(platform.KeyEvent{Key: cmd.key, Pressed: false})
				}
				cmd.resp <- nil
			case x11Close:
				for gk := range grabs {
					h.ungrabOne(gk)
				}
				cmd.resp <- nil
				stopTimers()
				C.XCloseDisplay(h.display)
				return
			}
			continue
		default:
		}

		var timerFired platform.Key
		select {
		case cmd := <-h.commands:
			// Re-dispatch through the outer loop for clarity.
			h.commands <- cmd
			continue
		case key := <-committed:
			timerFired = key
		case <-time.After(15 * time.Millisecond):
		}

		if timerFired != (platform.Key{}) {
			timer, waiting := pending[timerFired]
			if waiting {
				timer.Stop()
				delete(pending, timerFired)
				if !activeModOnly[timerFired] && modsToX11Mask(timerFired.Mods) == currentState {
					activeModOnly[timerFired] = true
					h.emit(platform.KeyEvent{Key: timerFired, Pressed: true})
				}
			}
			continue
		}

		if C.XPending(h.display) == 0 {
			continue
		}
		var ev C.XEvent
		C.XNextEvent(h.display, &ev)
		h.handleXEvent(
			&ev, grabs, activeCode, activeModOnly, modifierCodes, physicalCodes,
			pending, committed, detectableFallback, &currentState,
		)
	}
}

// grabBinding installs all XGrabKey variants for key. It rolls back grabs it
// added if any request fails.
func (h *x11Hotkey) grabBinding(
	key platform.Key,
	grabs map[grabKey]platform.Key,
	owned map[platform.Key][]grabKey,
	modifierCodes map[C.KeyCode]uint,
	physicalCodes map[platform.Key]map[C.KeyCode]bool,
) error {
	if _, exists := owned[key]; exists {
		return fmt.Errorf("x11 hotkey: already registered: %s", key)
	}

	base := modsToX11Mask(key.Mods)
	var toGrab []grabKey
	physical := make(map[C.KeyCode]bool)

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
				physical[kc] = true
				modifierCodes[kc] = entry.mask
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
		physical[kc] = true
	}

	added := make([]grabKey, 0, len(toGrab))
	for _, gk := range toGrab {
		for _, extra := range expandIgnoredModifiers(gk.modmask) {
			variant := grabKey{keycode: gk.keycode, modmask: extra}
			if _, duplicate := grabs[variant]; duplicate {
				return fmt.Errorf("x11 hotkey: grab already owned for %s", key)
			}
			if status := C.XGrabKey(h.display, C.int(variant.keycode), C.uint(variant.modmask), h.root,
				1, C.GrabModeAsync, C.GrabModeAsync); status == 0 {
				for _, rollback := range added {
					h.ungrabOne(rollback)
					delete(grabs, rollback)
				}
				return fmt.Errorf("x11 hotkey: XGrabKey failed for %s", key)
			}
			grabs[variant] = key
			owned[key] = append(owned[key], variant)
			added = append(added, variant)
		}
	}

	physicalCodes[key] = physical
	C.XSelectInput(h.display, h.root, C.KeyPressMask|C.KeyReleaseMask)
	return nil
}

func (h *x11Hotkey) ungrabOne(gk grabKey) {
	C.XUngrabKey(h.display, C.int(gk.keycode), C.uint(gk.modmask), h.root)
}

// handleXEvent converts X11 key edges while preserving the binding associated
// with the physical press. Release lookup therefore does not depend on the
// modifier state remaining unchanged.
func (h *x11Hotkey) handleXEvent(
	ev *C.XEvent,
	grabs map[grabKey]platform.Key,
	activeCode map[C.KeyCode]platform.Key,
	activeModOnly map[platform.Key]bool,
	modifierCodes map[C.KeyCode]uint,
	physicalCodes map[platform.Key]map[C.KeyCode]bool,
	pending map[platform.Key]*time.Timer,
	committed chan platform.Key,
	lastRelease map[C.KeyCode]C.ulong,
	trackedState *uint,
) {
	pressed := false
	switch C.event_type(ev) {
	case C.KeyPress:
		pressed = true
	case C.KeyRelease:
		pressed = false
	default:
		return
	}

	kc := C.KeyCode(C.event_keycode(ev))
	rawState := uint(C.event_state(ev))
	eventTime := C.ulong(C.event_time(ev))

	// Fallback for servers without detectable auto-repeat: a press in the same
	// tick as the preceding release for the same keycode is autorepeat noise.
	if pressed {
		if ts, seen := lastRelease[kc]; seen && ts == eventTime {
			delete(lastRelease, kc)
			return
		}
	} else {
		lastRelease[kc] = eventTime
	}

	// Compute the state after this edge, rather than the pre-event state
	// reported by Xlib. This lets modifier changes close an active binding.
	state := rawState & x11TrackedMask
	if mask, isModifier := modifierCodes[kc]; isModifier {
		if pressed {
			state |= mask
		} else {
			state &^= mask
		}
	}
	*trackedState = state

	h.invalidateModifierOnlyOnChange(kc, state, activeModOnly, pending, physicalCodes)
	h.releaseActiveOnChange(state, activeCode, activeModOnly, physicalCodes)

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
	binding, matched := grabs[grabKey{keycode: kc, modmask: lookupState}]
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
				case <-h.done:
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
	activeCode map[C.KeyCode]platform.Key,
	activeModOnly map[platform.Key]bool,
	physicalCodes map[platform.Key]map[C.KeyCode]bool,
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
	kc C.KeyCode,
	state uint,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
	physicalCodes map[platform.Key]map[C.KeyCode]bool,
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
	kc C.KeyCode,
	pending map[platform.Key]*time.Timer,
	physicalCodes map[platform.Key]map[C.KeyCode]bool,
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
	select {
	case h.events <- ev:
	case <-h.done:
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
