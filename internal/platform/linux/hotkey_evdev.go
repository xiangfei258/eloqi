//go:build linux

package linux

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// evdev event constants (from linux/input-event-codes.h).
const (
	evKey = 0x01

	keyRelease = 0
	keyPress   = 1
	keyRepeat  = 2
)

// inputEventSize is the size of struct input_event on 64-bit Linux: two
// 8-byte timeval fields + 2-byte type + 2-byte code + 4-byte value = 24.
const inputEventSize = 24

// modifierOnlySettleDelay is the observation window used before a
// modifier-only binding commits. If another non-modifier key arrives inside
// the window (for example Tab while the user is typing Alt+Super+Tab), the
// candidate is discarded. This is a pragmatic compromise: an observation-only
// input stream cannot prove that the user will never press another key.
const modifierOnlySettleDelay = 150 * time.Millisecond

// evdevModMap maps evdev key codes to modifier bits. Both left and right
// variants map to the same modifier.
var evdevModMap = map[uint16]platform.Modifiers{
	29:  platform.ModCtrl,  // KEY_LEFTCTRL
	97:  platform.ModCtrl,  // KEY_RIGHTCTRL
	56:  platform.ModAlt,   // KEY_LEFTALT
	100: platform.ModAlt,   // KEY_RIGHTALT
	125: platform.ModSuper, // KEY_LEFTMETA
	126: platform.ModSuper, // KEY_RIGHTMETA
	42:  platform.ModShift, // KEY_LEFTSHIFT
	54:  platform.ModShift, // KEY_RIGHTSHIFT
}

// evdevKeyMap maps evdev non-modifier key codes to platform.KeyCode values.
var evdevKeyMap = map[uint16]platform.KeyCode{
	15: platform.KeyTab,
	58: platform.KeyCapsLock,
	59: "F1", 60: "F2", 61: "F3", 62: "F4", 63: "F5", 64: "F6",
	65: "F7", 66: "F8", 67: "F9", 68: "F10",
	87: "F11", 88: "F12",
	183: "F13", 184: "F14", 185: "F15", 186: "F16",
	187: "F17", 188: "F18", 189: "F19", 190: "F20",
	191: "F21", 192: "F22", 193: "F23", 194: "F24",
	103: platform.KeyUp, 108: platform.KeyDown,
	105: platform.KeyLeft, 106: platform.KeyRight,
	102: platform.KeyHome, 107: platform.KeyEnd,
	104: platform.KeyPageUp, 109: platform.KeyPageDown,
	110: platform.KeyInsert, 111: platform.KeyDelete,
	71: platform.KeyNum7, 72: platform.KeyNum8, 73: platform.KeyNum9,
	75: platform.KeyNum4, 76: platform.KeyNum5, 77: platform.KeyNum6,
	79: platform.KeyNum1, 80: platform.KeyNum2, 81: platform.KeyNum3,
	82: platform.KeyNum0,
}

// rawEvent is an unparsed evdev key event sent from device readers to the
// central processor.
type rawEvent struct {
	code  uint16
	value int32
}

// evdevHotkey implements platform.Hotkey using the Linux evdev interface.
type evdevHotkey struct {
	mu         sync.Mutex
	registered map[platform.Key]bool
	events     chan platform.KeyEvent
	rawCh      chan rawEvent
	done       chan struct{}
	files      []*os.File
	closed     bool
	wg         sync.WaitGroup
}

var _ platform.Hotkey = (*evdevHotkey)(nil)

// newEvdevHotkey opens all available evdev devices and starts the event
// processing goroutines. It returns an error if no devices can be opened
// (typically because the user is not in the "input" group).
func newEvdevHotkey() (*evdevHotkey, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, fmt.Errorf("evdev: glob devices: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("evdev: no /dev/input/event* devices found (add user to the 'input' group)")
	}

	h := &evdevHotkey{
		registered: make(map[platform.Key]bool),
		events:     make(chan platform.KeyEvent, 64),
		rawCh:      make(chan rawEvent, 256),
		done:       make(chan struct{}),
	}

	opened := 0
	for _, p := range paths {
		f, err := os.OpenFile(p, os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		h.files = append(h.files, f)
		opened++
	}
	if opened == 0 {
		return nil, fmt.Errorf("evdev: cannot open any /dev/input/event* device (add user to the 'input' group)")
	}

	for _, f := range h.files {
		h.wg.Add(1)
		go h.readDevice(f)
	}
	h.wg.Add(1)
	go h.processEvents()

	return h, nil
}

// Register adds a hotkey binding.
func (h *evdevHotkey) Register(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("evdev hotkey: closed")
	}
	if h.registered[key] {
		return fmt.Errorf("evdev hotkey: already registered: %s", key)
	}
	h.registered[key] = true
	return nil
}

// Unregister removes a hotkey binding.
func (h *evdevHotkey) Unregister(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.registered, key)
	return nil
}

// Events returns the channel delivering hotkey edge events.
func (h *evdevHotkey) Events() <-chan platform.KeyEvent {
	return h.events
}

// Close stops all goroutines and closes devices. It is idempotent.
func (h *evdevHotkey) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()

	close(h.done)
	for _, f := range h.files {
		f.Close()
	}
	h.wg.Wait()
	close(h.events)
	return nil
}

// readDevice reads input_event structs from a device file and sends key events
// to the raw channel. It exits when the file is closed, an error occurs, or
// done is closed.
func (h *evdevHotkey) readDevice(f *os.File) {
	defer h.wg.Done()
	buf := make([]byte, inputEventSize)
	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			return
		}
		typ := binary.LittleEndian.Uint16(buf[16:18])
		if typ != evKey {
			continue
		}
		code := binary.LittleEndian.Uint16(buf[18:20])
		val := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if val == keyRepeat {
			continue
		}
		select {
		case h.rawCh <- rawEvent{code: code, value: val}:
		case <-h.done:
			return
		}
	}
}

// processEvents serializes raw events, modifier state and edge emission. All
// mutable state below is owned by this goroutine, which avoids lock-order
// issues around channel sends.
func (h *evdevHotkey) processEvents() {
	defer h.wg.Done()

	var modState platform.Modifiers
	activeCode := make(map[uint16]platform.Key)   // physical key -> binding
	activeModOnly := make(map[platform.Key]bool)  // committed modifier-only binding
	pending := make(map[platform.Key]*time.Timer) // settle-window candidates
	committed := make(chan platform.Key, 16)

	stopAllTimers := func() {
		for _, timer := range pending {
			timer.Stop()
		}
	}
	defer stopAllTimers()

	handle := func(raw rawEvent) {
		h.handleRawEvent(raw, &modState, activeCode, activeModOnly, pending, committed)
	}

	for {
		select {
		case raw, ok := <-h.rawCh:
			if !ok {
				return
			}
			handle(raw)
		case key := <-committed:
			// The timer may have raced with a cancellation; verify the state
			// again before emitting the press edge.
			h.mu.Lock()
			registered := h.registered[key]
			h.mu.Unlock()
			if timer, ok := pending[key]; ok && registered && !activeModOnly[key] && key.Mods == modState {
				timer.Stop()
				delete(pending, key)
				activeModOnly[key] = true
				h.emit(platform.KeyEvent{Key: key, Pressed: true})
			}
		case <-h.done:
			stopAllTimers()
			return
		}
	}
}

// handleRawEvent processes a single raw evdev key event.
func (h *evdevHotkey) handleRawEvent(
	raw rawEvent,
	modState *platform.Modifiers,
	activeCode map[uint16]platform.Key,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
	committed chan platform.Key,
) {
	pressed := raw.value == keyPress

	if mod, ok := evdevModMap[raw.code]; ok {
		if pressed {
			*modState |= mod
		} else {
			*modState &^= mod
		}
		h.afterModifierChange(*modState, activeCode, activeModOnly, pending, committed)
		return
	}

	// Any non-modifier key invalidates an uncommitted or active modifier-only
	// chord. This covers the common fast Alt+Super followed immediately by Tab
	// case without making ordinary Ctrl+F1 bindings impossible.
	if len(activeModOnly) != 0 || len(pending) != 0 {
		h.releaseAllModOnly(activeModOnly, pending)
	}
	h.releaseRegularWithChangedMods(*modState, activeCode)

	code, ok := evdevKeyMap[raw.code]
	if !ok {
		return
	}
	key := platform.Key{Mods: *modState, Code: code}

	h.mu.Lock()
	registered := h.registered[key]
	h.mu.Unlock()
	if !registered {
		return
	}

	// Pair release with the original press binding, not with the modifier
	// state at release time. If Ctrl is released before F1, the F1 release
	// still closes the Ctrl+F1 edge.
	if !pressed {
		if binding, active := activeCode[raw.code]; active {
			delete(activeCode, raw.code)
			h.emit(platform.KeyEvent{Key: binding, Pressed: false})
		}
		return
	}
	if _, active := activeCode[raw.code]; active {
		return
	}
	activeCode[raw.code] = key
	h.emit(platform.KeyEvent{Key: key, Pressed: true})
}

// afterModifierChange releases bindings whose exact modifier set no longer
// holds and starts (or cancels) modifier-only settle candidates.
func (h *evdevHotkey) afterModifierChange(
	mods platform.Modifiers,
	activeCode map[uint16]platform.Key,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
	committed chan platform.Key,
) {
	h.releaseRegularWithChangedMods(mods, activeCode)

	h.mu.Lock()
	keys := make([]platform.Key, 0, len(h.registered))
	for key := range h.registered {
		keys = append(keys, key)
	}
	h.mu.Unlock()

	for _, key := range keys {
		if key.Code != platform.KeyNone {
			continue
		}
		if key.Mods == mods {
			if !activeModOnly[key] {
				if _, waiting := pending[key]; !waiting {
					pending[key] = time.AfterFunc(modifierOnlySettleDelay, func() {
						select {
						case committed <- key:
						case <-h.done:
						}
					})
				}
			}
			continue
		}
		if activeModOnly[key] {
			delete(activeModOnly, key)
			h.emit(platform.KeyEvent{Key: key, Pressed: false})
		}
		if timer, waiting := pending[key]; waiting {
			timer.Stop()
			delete(pending, key)
		}
	}
}

// releaseRegularWithChangedMods releases ordinary bindings whose exact
// modifier set has changed.
func (h *evdevHotkey) releaseRegularWithChangedMods(mods platform.Modifiers, activeCode map[uint16]platform.Key) {
	for physical, binding := range activeCode {
		if binding.Mods != mods {
			delete(activeCode, physical)
			h.emit(platform.KeyEvent{Key: binding, Pressed: false})
		}
	}
}

// releaseAllModOnly immediately discards modifier-only candidates and closes
// active modifier-only edges.
func (h *evdevHotkey) releaseAllModOnly(active map[platform.Key]bool, pending map[platform.Key]*time.Timer) {
	for key := range pending {
		if timer := pending[key]; timer != nil {
			timer.Stop()
		}
		delete(pending, key)
	}
	for key := range active {
		delete(active, key)
		h.emit(platform.KeyEvent{Key: key, Pressed: false})
	}
}

// emit sends an event unless the hotkey is shutting down.
func (h *evdevHotkey) emit(ev platform.KeyEvent) {
	select {
	case h.events <- ev:
	case <-h.done:
	}
}
