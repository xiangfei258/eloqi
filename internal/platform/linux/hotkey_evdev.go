//go:build linux

package linux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/evdev"
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
	1:  platform.KeyEscape, // KEY_ESC
	19: platform.KeyR,      // KEY_R (reserved retry binding)
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

type evdevRegistrationCommand struct {
	key      platform.Key
	register bool
	response chan error
}

// evdevEventDispatcher keeps public Events backpressure off the processor that
// owns registration and physical-key state. Shutdown discards queued edges so
// Unregister and Close remain bounded even after the Voice consumer exits.
type evdevEventDispatcher struct {
	mu      sync.Mutex
	ready   *sync.Cond
	pending []platform.KeyEvent
	head    int
	closed  bool
	events  chan platform.KeyEvent
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newEvdevEventDispatcher(capacity int) *evdevEventDispatcher {
	d := &evdevEventDispatcher{
		events: make(chan platform.KeyEvent, capacity),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	d.ready = sync.NewCond(&d.mu)
	go d.run()
	return d
}

func (d *evdevEventDispatcher) enqueue(event platform.KeyEvent) {
	d.mu.Lock()
	if !d.closed {
		d.pending = append(d.pending, event)
		d.ready.Signal()
	}
	d.mu.Unlock()
}

func (d *evdevEventDispatcher) run() {
	defer close(d.events)
	defer close(d.done)
	for {
		d.mu.Lock()
		for d.head == len(d.pending) && !d.closed {
			d.ready.Wait()
		}
		if d.closed {
			d.mu.Unlock()
			return
		}
		event := d.pending[d.head]
		d.head++
		if d.head >= 64 && d.head*2 >= len(d.pending) {
			copy(d.pending, d.pending[d.head:])
			d.pending = d.pending[:len(d.pending)-d.head]
			d.head = 0
		}
		d.mu.Unlock()

		select {
		case d.events <- event:
		case <-d.stop:
			return
		}
	}
}

func (d *evdevEventDispatcher) closeAndWait() {
	if d == nil {
		return
	}
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		close(d.stop)
		d.ready.Broadcast()
		d.mu.Unlock()
	})
	<-d.done
}

// evdevHotkey implements platform.Hotkey using the Linux evdev interface.
type evdevHotkey struct {
	mu         sync.Mutex
	registered map[platform.Key]bool
	events     chan platform.KeyEvent
	rawCh      chan rawEvent
	commands   chan evdevRegistrationCommand
	dispatcher *evdevEventDispatcher
	done       chan struct{}
	files      []*os.File
	closed     bool
	wg         sync.WaitGroup
}

var _ platform.Hotkey = (*evdevHotkey)(nil)

type evdevHotkeyHost struct {
	glob     func(string) ([]string, error)
	openFile func(string, int, os.FileMode) (*os.File, error)
	readFile func(string) ([]byte, error)
}

// newEvdevHotkey opens all available evdev devices and starts the event
// processing goroutines. It returns an error if no devices can be opened
// (typically because the user is not in the "input" group).
func newEvdevHotkey() (*evdevHotkey, error) {
	return newEvdevHotkeyWithHost(evdevHotkeyHost{
		glob:     filepath.Glob,
		openFile: os.OpenFile,
		readFile: os.ReadFile,
	})
}

func newEvdevHotkeyWithHost(host evdevHotkeyHost) (*evdevHotkey, error) {
	paths, err := host.glob("/dev/input/event*")
	if err != nil {
		return nil, fmt.Errorf("evdev: glob devices: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("evdev: no /dev/input/event* devices found (add user to the 'input' group)")
	}

	dispatcher := newEvdevEventDispatcher(64)
	h := &evdevHotkey{
		registered: make(map[platform.Key]bool),
		events:     dispatcher.events,
		rawCh:      make(chan rawEvent, 256),
		commands:   make(chan evdevRegistrationCommand),
		dispatcher: dispatcher,
		done:       make(chan struct{}),
	}

	opened := 0
	for _, p := range paths {
		ydotoolDevice, probeErr := evdev.IsYdotoolVirtualDevice(p, host.readFile)
		// Fail closed when the sysfs name cannot be read. The name lives beside
		// the capability bitmap already required below, and consuming an
		// unidentified synthetic keyboard can feed Eloqui's own Ctrl+V back into
		// the global-hotkey state machine.
		if probeErr != nil || ydotoolDevice {
			continue
		}
		f, err := host.openFile(p, os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		keyboard, probeErr := evdev.IsKeyboardDevice(p, host.readFile)
		if probeErr != nil || !keyboard {
			_ = f.Close()
			continue
		}
		h.files = append(h.files, f)
		opened++
	}
	if opened == 0 {
		dispatcher.closeAndWait()
		return nil, fmt.Errorf("evdev: cannot open a physical keyboard-capable /dev/input/event* device (ydotoold virtual input is ignored; add user to the 'input' group)")
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
	if h.commands != nil {
		return h.submitRegistration(key, true)
	}
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
	if h.commands != nil {
		return h.submitRegistration(key, false)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.registered, key)
	return nil
}

func (h *evdevHotkey) submitRegistration(key platform.Key, register bool) error {
	response := make(chan error, 1)
	command := evdevRegistrationCommand{key: key, register: register, response: response}
	select {
	case <-h.done:
		return fmt.Errorf("evdev hotkey: closed")
	case h.commands <- command:
	}
	select {
	case <-h.done:
		return fmt.Errorf("evdev hotkey: closed")
	case err := <-response:
		return err
	}
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
	var closeErrs []error
	for _, f := range h.files {
		if err := f.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("evdev: close input device: %w", err))
		}
	}
	h.wg.Wait()
	if h.dispatcher != nil {
		h.dispatcher.closeAndWait()
	} else {
		close(h.events)
	}
	return errors.Join(closeErrs...)
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
	modifierRefs := make(map[platform.Modifiers]int)
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
		h.handleRawEvent(raw, &modState, modifierRefs, activeCode, activeModOnly, pending, committed)
	}

	for {
		select {
		case raw, ok := <-h.rawCh:
			if !ok {
				return
			}
			handle(raw)
		case command := <-h.commands:
			err := h.applyRegistrationCommand(command, activeCode, activeModOnly, pending)
			command.response <- err
		case key := <-committed:
			// The timer may have raced with a cancellation; verify the state
			// again before emitting the press edge.
			timer, waiting := pending[key]
			if !waiting {
				continue
			}
			timer.Stop()
			delete(pending, key)
			h.mu.Lock()
			registered := h.registered[key]
			h.mu.Unlock()
			if registered && !activeModOnly[key] && key.Mods == modState {
				activeModOnly[key] = true
				h.emit(platform.KeyEvent{Key: key, Pressed: true})
			}
		case <-h.done:
			stopAllTimers()
			return
		}
	}
}

func (h *evdevHotkey) applyRegistrationCommand(
	command evdevRegistrationCommand,
	activeCode map[uint16]platform.Key,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
) error {
	h.mu.Lock()
	var err error
	switch {
	case h.closed:
		err = fmt.Errorf("evdev hotkey: closed")
	case command.register && h.registered[command.key]:
		err = fmt.Errorf("evdev hotkey: already registered: %s", command.key)
	case command.register:
		h.registered[command.key] = true
	default:
		delete(h.registered, command.key)
	}
	h.mu.Unlock()
	if err != nil || command.register {
		return err
	}

	if timer := pending[command.key]; timer != nil {
		timer.Stop()
	}
	delete(pending, command.key)
	delete(activeModOnly, command.key)
	for physical, binding := range activeCode {
		if binding == command.key {
			delete(activeCode, physical)
		}
	}
	return nil
}

// handleRawEvent processes a single raw evdev key event.
func (h *evdevHotkey) handleRawEvent(
	raw rawEvent,
	modState *platform.Modifiers,
	modifierRefs map[platform.Modifiers]int,
	activeCode map[uint16]platform.Key,
	activeModOnly map[platform.Key]bool,
	pending map[platform.Key]*time.Timer,
	committed chan platform.Key,
) {
	pressed := raw.value == keyPress

	if mod, ok := evdevModMap[raw.code]; ok {
		// Left and right variants share a semantic bit, but each physical key
		// holds its own reference. Releasing left Ctrl while right Ctrl is
		// still down must not clear the shared Ctrl state.
		if pressed {
			if modifierRefs[mod] == 0 {
				*modState |= mod
			}
			modifierRefs[mod]++
		} else if modifierRefs[mod] > 0 {
			modifierRefs[mod]--
			if modifierRefs[mod] == 0 {
				*modState &^= mod
			}
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

	// A binding may be unregistered synchronously by the consumer as soon as
	// its press edge is delivered (Esc and R do this in the voice lifecycle).
	// Always retire the physical press on release, even when the binding is no
	// longer registered, or the next press of that physical key is suppressed
	// forever as a duplicate. Only emit the release when the original binding
	// is still registered, preserving Unregister's delivery boundary.
	if !pressed {
		if binding, active := activeCode[raw.code]; active {
			delete(activeCode, raw.code)
			h.mu.Lock()
			registered := h.registered[binding]
			h.mu.Unlock()
			if registered {
				h.emit(platform.KeyEvent{Key: binding, Pressed: false})
			}
		}
		return
	}

	key := platform.Key{Mods: *modState, Code: code}

	h.mu.Lock()
	registered := h.registered[key]
	h.mu.Unlock()
	if !registered {
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
	if h.dispatcher != nil {
		h.dispatcher.enqueue(ev)
		return
	}
	select {
	case h.events <- ev:
	case <-h.done:
	}
}
