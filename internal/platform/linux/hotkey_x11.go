//go:build linux

package linux

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
#include <X11/X.h>

// Wrapper to read the type field from an XEvent.
static int event_type(XEvent *ev) {
    return ev->type;
}

// Wrapper to read the keycode field from an XEvent treated as XKeyEvent.
static unsigned int event_keycode(XEvent *ev) {
    return ((XKeyEvent *)ev)->keycode;
}

// Wrapper to read the state (modifier mask) from an XEvent.
static unsigned int event_state(XEvent *ev) {
    return ((XKeyEvent *)ev)->state;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// X11 modifier masks (from X11/X.h).
const (
	x11ShiftMask   = 1 << 0 // ShiftMask
	x11ControlMask = 1 << 2 // ControlMask
	x11Mod1Mask    = 1 << 3 // Mod1Mask (Alt)
	x11Mod4Mask    = 1 << 6 // Mod4Mask (Super)
	x11AllModMask  = x11ShiftMask | x11ControlMask | x11Mod1Mask | x11Mod4Mask
)

// grabKey pairs an X11 keycode with the modifier mask it was grabbed with.
type grabKey struct {
	keycode C.KeyCode
	modmask uint
}

// x11Hotkey implements platform.Hotkey using Xlib XGrabKey.
type x11Hotkey struct {
	mu         sync.Mutex
	display    *C.Display
	root       C.Window
	registered map[platform.Key]bool
	grabs      map[grabKey]platform.Key // reverse lookup: (keycode, modmask) -> Key
	events     chan platform.KeyEvent
	done       chan struct{}
	closed     bool
	wg         sync.WaitGroup
}

var _ platform.Hotkey = (*x11Hotkey)(nil)

// newX11Hotkey opens the X display and prepares the hotkey listener. The
// event loop starts immediately.
func newX11Hotkey() (*x11Hotkey, error) {
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return nil, fmt.Errorf("x11 hotkey: cannot open display (is DISPLAY set?)")
	}
	h := &x11Hotkey{
		display:    dpy,
		root:       C.XDefaultRootWindow(dpy),
		registered: make(map[platform.Key]bool),
		grabs:      make(map[grabKey]platform.Key),
		events:     make(chan platform.KeyEvent, 64),
		done:       make(chan struct{}),
	}
	h.wg.Add(1)
	go h.eventLoop()
	return h, nil
}

// Register grabs a key combination globally.
func (h *x11Hotkey) Register(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("x11 hotkey: closed")
	}
	if h.registered[key] {
		return fmt.Errorf("x11 hotkey: already registered: %s", key)
	}

	if key.Code == platform.KeyNone {
		if err := h.grabModifierOnly(key); err != nil {
			return err
		}
	} else {
		if err := h.grabKeyWithMods(key); err != nil {
			return err
		}
	}
	h.registered[key] = true
	return nil
}

// grabKeyWithMods grabs a non-modifier key with a modifier mask.
func (h *x11Hotkey) grabKeyWithMods(key platform.Key) error {
	ksym := keyCodeToX11Keysym(key.Code)
	if ksym == 0 {
		return fmt.Errorf("x11 hotkey: unknown key code %q", key.Code)
	}
	kc := C.XKeysymToKeycode(h.display, C.KeySym(ksym))
	if kc == 0 {
		return fmt.Errorf("x11 hotkey: no keycode for keysym %d", ksym)
	}
	mask := modsToX11Mask(key.Mods)
	h.addGrab(kc, mask, key)
	return nil
}

// grabModifierOnly grabs a modifier-only combo. For each modifier in the combo,
// it grabs that modifier's keycode with the remaining modifiers as the mask.
func (h *x11Hotkey) grabModifierOnly(key platform.Key) error {
	for _, entry := range modKeysymTable {
		if key.Mods&entry.mod == 0 {
			continue
		}
		kc := C.XKeysymToKeycode(h.display, C.KeySym(entry.keysym))
		if kc == 0 {
			continue
		}
		otherMask := modsToX11Mask(key.Mods) &^ entry.x11mask
		h.addGrab(kc, otherMask, key)
	}
	return nil
}

// addGrab registers an XGrabKey and stores the reverse mapping.
func (h *x11Hotkey) addGrab(kc C.KeyCode, mask uint, key platform.Key) {
	gk := grabKey{keycode: kc, modmask: mask}
	h.grabs[gk] = key
	C.XGrabKey(h.display, C.int(kc), C.uint(mask), h.root,
		C.Bool(1), // owner_events = True (don't consume)
		C.GrabModeAsync, C.GrabModeAsync)
	C.XSelectInput(h.display, h.root, C.KeyPressMask|C.KeyReleaseMask)
}

// Unregister removes a previously registered key.
func (h *x11Hotkey) Unregister(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registered[key] {
		return nil
	}
	delete(h.registered, key)
	// Remove all grabs associated with this key.
	for gk, k := range h.grabs {
		if k == key {
			C.XUngrabKey(h.display, C.int(gk.keycode), C.uint(gk.modmask), h.root)
			delete(h.grabs, gk)
		}
	}
	return nil
}

// Events returns the channel delivering hotkey edge events.
func (h *x11Hotkey) Events() <-chan platform.KeyEvent {
	return h.events
}

// Close ungrabs all keys, stops the event loop and closes the display.
func (h *x11Hotkey) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()

	close(h.done)
	for gk := range h.grabs {
		C.XUngrabKey(h.display, C.int(gk.keycode), C.uint(gk.modmask), h.root)
	}
	h.wg.Wait()
	C.XCloseDisplay(h.display)
	close(h.events)
	return nil
}

// eventLoop polls for X11 key events and delivers them as KeyEvents.
func (h *x11Hotkey) eventLoop() {
	defer h.wg.Done()
	var ev C.XEvent
	for {
		select {
		case <-h.done:
			return
		default:
		}
		if C.XPending(h.display) > 0 {
			C.XNextEvent(h.display, &ev)
			h.handleXEvent(&ev)
		} else {
			select {
			case <-h.done:
				return
			case <-time.After(15 * time.Millisecond):
			}
		}
	}
}

// handleXEvent converts an X11 KeyPress/KeyRelease to a platform.KeyEvent.
func (h *x11Hotkey) handleXEvent(ev *C.XEvent) {
	var pressed bool
	switch C.event_type(ev) {
	case C.KeyPress:
		pressed = true
	case C.KeyRelease:
		pressed = false
	default:
		return
	}

	kc := C.KeyCode(C.event_keycode(ev))
	state := uint(C.event_state(ev)) & x11AllModMask

	h.mu.Lock()
	key, ok := h.grabs[grabKey{keycode: kc, modmask: state}]
	h.mu.Unlock()
	if !ok {
		return
	}

	select {
	case h.events <- platform.KeyEvent{Key: key, Pressed: pressed}:
	case <-h.done:
	}
}

// modKeysymEntry maps a platform.Modifier to its X11 keysym and mask.
type modKeysymEntry struct {
	mod     platform.Modifiers
	keysym  uint
	x11mask uint
}

var modKeysymTable = []modKeysymEntry{
	{platform.ModCtrl, 0xFFE3, x11ControlMask}, // XK_Control_L
	{platform.ModAlt, 0xFFE9, x11Mod1Mask},     // XK_Alt_L
	{platform.ModSuper, 0xFFEB, x11Mod4Mask},   // XK_Super_L
	{platform.ModShift, 0xFFE1, x11ShiftMask},  // XK_Shift_L
}

// modsToX11Mask converts platform.Modifiers to an X11 modifier mask.
func modsToX11Mask(mods platform.Modifiers) uint {
	var m uint
	for _, entry := range modKeysymTable {
		if mods&entry.mod != 0 {
			m |= entry.x11mask
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
	// Function keys F1..F24: XK_F1 = 0xFFBE, incrementing.
	s := string(code)
	if len(s) >= 2 && s[0] == 'F' {
		var n int
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

// Ensure unsafe is referenced (used implicitly by cgo pointer conversions).
var _ = unsafe.Sizeof(C.XEvent{})
