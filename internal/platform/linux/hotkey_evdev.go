//go:build linux

package linux

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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

// evdevHotkey implements platform.Hotkey using the Linux evdev interface. It
// opens all /dev/input/event* devices, reads key events, tracks modifier
// state, and matches registered hotkey combos.
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

	close(h.done) // unblock reader goroutines waiting on send
	for _, f := range h.files {
		f.Close() // unblock f.Read
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
		n, err := f.Read(buf)
		if err != nil {
			return
		}
		if n < inputEventSize {
			continue
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

// processEvents consumes raw evdev events, tracks modifier state, and emits
// platform.KeyEvent values for registered combos.
func (h *evdevHotkey) processEvents() {
	defer h.wg.Done()

	var modState platform.Modifiers
	activeModOnly := make(map[platform.Key]bool)

	for {
		select {
		case raw, ok := <-h.rawCh:
			if !ok {
				return
			}
			h.handleRawEvent(raw, &modState, activeModOnly)
		case <-h.done:
			return
		}
	}
}

// handleRawEvent processes a single raw evdev key event.
func (h *evdevHotkey) handleRawEvent(raw rawEvent, modState *platform.Modifiers, activeModOnly map[platform.Key]bool) {
	pressed := raw.value == keyPress

	if mod, ok := evdevModMap[raw.code]; ok {
		if pressed {
			*modState |= mod
		} else {
			*modState &^= mod
		}
		h.checkModOnlyCombos(*modState, activeModOnly, pressed)
		return
	}

	code, ok := evdevKeyMap[raw.code]
	if !ok {
		return
	}
	key := platform.Key{Mods: *modState, Code: code}
	h.mu.Lock()
	registered := h.registered[key]
	h.mu.Unlock()
	if registered {
		select {
		case h.events <- platform.KeyEvent{Key: key, Pressed: pressed}:
		case <-h.done:
		}
	}
}

// checkModOnlyCombos checks whether any registered modifier-only combo matches
// the current modifier state and emits press/release events accordingly.
func (h *evdevHotkey) checkModOnlyCombos(mods platform.Modifiers, active map[platform.Key]bool, pressed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key := range h.registered {
		if key.Code != platform.KeyNone {
			continue
		}
		matches := key.Mods == mods
		if matches && !active[key] && pressed {
			active[key] = true
			select {
			case h.events <- platform.KeyEvent{Key: key, Pressed: true}:
			case <-h.done:
			}
		} else if !matches && active[key] {
			active[key] = false
			select {
			case h.events <- platform.KeyEvent{Key: key, Pressed: false}:
			case <-h.done:
			}
		}
	}
}
