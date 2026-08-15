package windows

import (
	"fmt"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const modifierOnlySettleDelay = 150 * time.Millisecond

const (
	vkShift    uint16 = 0x10
	vkControl  uint16 = 0x11
	vkMenu     uint16 = 0x12
	vkTab      uint16 = 0x09
	vkCapsLock uint16 = 0x14
	vkEscape   uint16 = 0x1B
	vkPageUp   uint16 = 0x21
	vkPageDown uint16 = 0x22
	vkEnd      uint16 = 0x23
	vkHome     uint16 = 0x24
	vkLeft     uint16 = 0x25
	vkUp       uint16 = 0x26
	vkRight    uint16 = 0x27
	vkDown     uint16 = 0x28
	vkInsert   uint16 = 0x2D
	vkDelete   uint16 = 0x2E
	vkR        uint16 = 0x52
	vkNum0     uint16 = 0x60
	vkF1       uint16 = 0x70

	vkLShift   uint16 = 0xA0
	vkRShift   uint16 = 0xA1
	vkLControl uint16 = 0xA2
	vkRControl uint16 = 0xA3
	vkLMenu    uint16 = 0xA4
	vkRMenu    uint16 = 0xA5
	vkLWin     uint16 = 0x5B
	vkRWin     uint16 = 0x5C
)

const lowLevelHookExtended = 0x01

var windowsModifierKeys = map[uint16]platform.Modifiers{
	vkLControl: platform.ModCtrl,
	vkRControl: platform.ModCtrl,
	vkLMenu:    platform.ModAlt,
	vkRMenu:    platform.ModAlt,
	vkLWin:     platform.ModSuper,
	vkRWin:     platform.ModSuper,
	vkLShift:   platform.ModShift,
	vkRShift:   platform.ModShift,
}

// edgeMachine is the cgo-free, deterministic part of the Windows hotkey
// backend. Both the 5 ms GetAsyncKeyState poller and the observing low-level
// hook feed it physical edges; duplicate observations are collapsed here.
type edgeMachine struct {
	registered map[platform.Key]struct{}
	down       map[uint16]bool
	activeCode map[uint16]platform.Key
	pending    map[platform.Key]time.Time
	activeMods map[platform.Key]bool
	settle     time.Duration
}

func newEdgeMachine() *edgeMachine {
	return &edgeMachine{
		registered: make(map[platform.Key]struct{}),
		down:       make(map[uint16]bool),
		activeCode: make(map[uint16]platform.Key),
		pending:    make(map[platform.Key]time.Time),
		activeMods: make(map[platform.Key]bool),
		settle:     modifierOnlySettleDelay,
	}
}

func (m *edgeMachine) register(key platform.Key) error {
	if err := validateWindowsBinding(key); err != nil {
		return err
	}
	if _, exists := m.registered[key]; exists {
		return fmt.Errorf("windows hotkey: already registered: %s", key)
	}
	m.registered[key] = struct{}{}
	return nil
}

// unregister removes all state owned by key without publishing a synthetic
// release. Unregister is also used during Voice.Stop, after the public Events
// consumer has stopped; attempting to publish there can deadlock forever when
// the channel is already full.
func (m *edgeMachine) unregister(key platform.Key) {
	delete(m.registered, key)
	delete(m.pending, key)
	delete(m.activeMods, key)
	for physical, binding := range m.activeCode {
		if binding == key {
			delete(m.activeCode, physical)
		}
	}
}

func (m *edgeMachine) edge(vk uint16, pressed bool, now time.Time) []platform.KeyEvent {
	if m.down[vk] == pressed {
		return m.commit(now)
	}
	m.down[vk] = pressed

	mods := m.modifiers()
	var events []platform.KeyEvent
	for physical, binding := range m.activeCode {
		if (!pressed && physical == vk) || binding.Mods != mods {
			delete(m.activeCode, physical)
			events = append(events, platform.KeyEvent{Key: binding, Pressed: false})
		}
	}

	if _, isModifier := windowsModifierKeys[vk]; isModifier {
		for key := range m.pending {
			if key.Mods != mods {
				delete(m.pending, key)
			}
		}
		for key := range m.activeMods {
			if key.Mods != mods {
				delete(m.activeMods, key)
				events = append(events, platform.KeyEvent{Key: key, Pressed: false})
			}
		}
		if !m.anyNonModifierDown() {
			for key := range m.registered {
				if key.Code == platform.KeyNone && key.Mods == mods && !m.activeMods[key] {
					if _, waiting := m.pending[key]; !waiting {
						m.pending[key] = now.Add(m.settle)
					}
				}
			}
		}
		events = append(events, m.commit(now)...)
		return events
	}

	// A modifier-only chord is observational. Any ordinary key proves that
	// the chord was part of a larger shortcut (for example Alt+Super+Tab), so
	// cancel it without consuming the ordinary key.
	for key := range m.pending {
		delete(m.pending, key)
	}
	for key := range m.activeMods {
		delete(m.activeMods, key)
		events = append(events, platform.KeyEvent{Key: key, Pressed: false})
	}
	if !pressed {
		return events
	}
	code, ok := windowsKeyCode(vk)
	if !ok {
		return events
	}
	key := platform.Key{Mods: mods, Code: code}
	if _, registered := m.registered[key]; registered {
		m.activeCode[vk] = key
		events = append(events, platform.KeyEvent{Key: key, Pressed: true})
	}
	return events
}

func (m *edgeMachine) commit(now time.Time) []platform.KeyEvent {
	mods := m.modifiers()
	if m.anyNonModifierDown() {
		return nil
	}
	var events []platform.KeyEvent
	for key, deadline := range m.pending {
		if deadline.After(now) {
			continue
		}
		delete(m.pending, key)
		if key.Mods != mods || m.activeMods[key] {
			continue
		}
		if _, registered := m.registered[key]; !registered {
			continue
		}
		m.activeMods[key] = true
		events = append(events, platform.KeyEvent{Key: key, Pressed: true})
	}
	return events
}

func (m *edgeMachine) modifiers() platform.Modifiers {
	var mods platform.Modifiers
	for vk, mod := range windowsModifierKeys {
		if m.down[vk] {
			mods |= mod
		}
	}
	return mods
}

func (m *edgeMachine) anyNonModifierDown() bool {
	for vk, down := range m.down {
		if !down {
			continue
		}
		if _, modifier := windowsModifierKeys[vk]; !modifier {
			return true
		}
	}
	return false
}

//nolint:unused // used by hotkey_windows.go on native Windows builds.
func (m *edgeMachine) isDown(vk uint16) bool {
	return m.down[vk]
}

func validateWindowsBinding(key platform.Key) error {
	const knownMods = platform.ModCtrl | platform.ModAlt | platform.ModSuper | platform.ModShift
	if key.Mods&^knownMods != 0 {
		return fmt.Errorf("windows hotkey: unsupported modifier mask %#x", key.Mods)
	}
	if key.Code == platform.KeyNone {
		if key.Mods == 0 {
			return fmt.Errorf("windows hotkey: empty binding")
		}
		return nil
	}
	if _, ok := windowsVirtualKey(key.Code); !ok {
		return fmt.Errorf("windows hotkey: unsupported key code %q", key.Code)
	}
	return nil
}

func windowsVirtualKey(code platform.KeyCode) (uint16, bool) {
	switch code {
	case platform.KeyTab:
		return vkTab, true
	case platform.KeyCapsLock:
		return vkCapsLock, true
	case platform.KeyEscape:
		return vkEscape, true
	case platform.KeyR:
		return vkR, true
	case platform.KeyLeft:
		return vkLeft, true
	case platform.KeyRight:
		return vkRight, true
	case platform.KeyUp:
		return vkUp, true
	case platform.KeyDown:
		return vkDown, true
	case platform.KeyHome:
		return vkHome, true
	case platform.KeyEnd:
		return vkEnd, true
	case platform.KeyPageUp:
		return vkPageUp, true
	case platform.KeyPageDown:
		return vkPageDown, true
	case platform.KeyInsert:
		return vkInsert, true
	case platform.KeyDelete:
		return vkDelete, true
	}
	name := string(code)
	if len(name) >= 2 && name[0] == 'F' {
		if n, ok := decimalSuffix(name[1:]); ok && n >= 1 && n <= 24 {
			return vkF1 + uint16(n-1), true
		}
	}
	if len(name) == 4 && name[:3] == "Num" && name[3] >= '0' && name[3] <= '9' {
		return vkNum0 + uint16(name[3]-'0'), true
	}
	return 0, false
}

func windowsKeyCode(vk uint16) (platform.KeyCode, bool) {
	switch vk {
	case vkTab:
		return platform.KeyTab, true
	case vkCapsLock:
		return platform.KeyCapsLock, true
	case vkEscape:
		return platform.KeyEscape, true
	case vkR:
		return platform.KeyR, true
	case vkLeft:
		return platform.KeyLeft, true
	case vkRight:
		return platform.KeyRight, true
	case vkUp:
		return platform.KeyUp, true
	case vkDown:
		return platform.KeyDown, true
	case vkHome:
		return platform.KeyHome, true
	case vkEnd:
		return platform.KeyEnd, true
	case vkPageUp:
		return platform.KeyPageUp, true
	case vkPageDown:
		return platform.KeyPageDown, true
	case vkInsert:
		return platform.KeyInsert, true
	case vkDelete:
		return platform.KeyDelete, true
	}
	if vk >= vkF1 && vk < vkF1+24 {
		code, _ := platform.FunctionKey(int(vk-vkF1) + 1)
		return code, true
	}
	if vk >= vkNum0 && vk < vkNum0+10 {
		return platform.KeyCode("Num" + string(rune('0'+vk-vkNum0))), true
	}
	return platform.KeyNone, false
}

func decimalSuffix(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

//nolint:unused // used by hotkey_windows.go on native Windows builds.
func trackedWindowsKeys(registered map[platform.Key]struct{}, includeUnregistered bool) []uint16 {
	seen := make(map[uint16]bool, len(registered)+len(windowsModifierKeys))
	for vk := range windowsModifierKeys {
		seen[vk] = true
	}
	for key := range registered {
		if key.Code == platform.KeyNone {
			continue
		}
		if vk, ok := windowsVirtualKey(key.Code); ok {
			seen[vk] = true
		}
	}
	if includeUnregistered {
		for vk := uint16(1); vk < 255; vk++ {
			switch vk {
			case 0x01, 0x02, 0x04, 0x05, 0x06, // mouse buttons
				vkShift, vkControl, vkMenu: // generic aliases of side-specific modifiers
				continue
			}
			seen[vk] = true
		}
	}
	keys := make([]uint16, 0, len(seen))
	for vk := range seen {
		keys = append(keys, vk)
	}
	return keys
}

func normalizeWindowsHookKey(vk uint16, scanCode uint32, flags uint32) uint16 {
	switch vk {
	case vkControl:
		if flags&lowLevelHookExtended != 0 {
			return vkRControl
		}
		return vkLControl
	case vkMenu:
		if flags&lowLevelHookExtended != 0 {
			return vkRMenu
		}
		return vkLMenu
	case vkShift:
		if scanCode == 0x36 {
			return vkRShift
		}
		return vkLShift
	default:
		return vk
	}
}
