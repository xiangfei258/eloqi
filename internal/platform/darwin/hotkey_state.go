package darwin

import (
	"fmt"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const darwinModifierOnlySettleDelay = 150 * time.Millisecond

var darwinModifierKeys = map[uint16]platform.Modifiers{
	0x3B: platform.ModCtrl,  // left Control
	0x3E: platform.ModCtrl,  // right Control
	0x3A: platform.ModAlt,   // left Option
	0x3D: platform.ModAlt,   // right Option
	0x37: platform.ModSuper, // left Command
	0x36: platform.ModSuper, // right Command
	0x38: platform.ModShift, // left Shift
	0x3C: platform.ModShift, // right Shift
}

var darwinCodeToVirtual = map[platform.KeyCode]uint16{
	platform.KeyTab:      0x30,
	platform.KeyCapsLock: 0x39,
	platform.KeyEscape:   0x35,
	platform.KeyR:        0x0F,
	platform.KeyLeft:     0x7B,
	platform.KeyRight:    0x7C,
	platform.KeyDown:     0x7D,
	platform.KeyUp:       0x7E,
	platform.KeyHome:     0x73,
	platform.KeyEnd:      0x77,
	platform.KeyPageUp:   0x74,
	platform.KeyPageDown: 0x79,
	platform.KeyInsert:   0x72, // Help/Insert on extended Apple keyboards
	platform.KeyDelete:   0x75, // forward delete
	platform.KeyNum0:     0x52,
	platform.KeyNum1:     0x53,
	platform.KeyNum2:     0x54,
	platform.KeyNum3:     0x55,
	platform.KeyNum4:     0x56,
	platform.KeyNum5:     0x57,
	platform.KeyNum6:     0x58,
	platform.KeyNum7:     0x59,
	platform.KeyNum8:     0x5B,
	platform.KeyNum9:     0x5C,
}

var darwinFunctionKeys = map[int]uint16{
	1: 0x7A, 2: 0x78, 3: 0x63, 4: 0x76, 5: 0x60,
	6: 0x61, 7: 0x62, 8: 0x64, 9: 0x65, 10: 0x6D,
	11: 0x67, 12: 0x6F, 13: 0x69, 14: 0x6B, 15: 0x71,
	16: 0x6A, 17: 0x40, 18: 0x4F, 19: 0x50, 20: 0x5A,
}

type darwinEdgeMachine struct {
	registered map[platform.Key]struct{}
	down       map[uint16]bool
	activeCode map[uint16]platform.Key
	pending    map[platform.Key]time.Time
	activeMods map[platform.Key]bool
	settle     time.Duration
}

func newDarwinEdgeMachine() *darwinEdgeMachine {
	return &darwinEdgeMachine{
		registered: make(map[platform.Key]struct{}),
		down:       make(map[uint16]bool),
		activeCode: make(map[uint16]platform.Key),
		pending:    make(map[platform.Key]time.Time),
		activeMods: make(map[platform.Key]bool),
		settle:     darwinModifierOnlySettleDelay,
	}
}

func (m *darwinEdgeMachine) register(key platform.Key) error {
	if err := validateDarwinBinding(key); err != nil {
		return err
	}
	if _, exists := m.registered[key]; exists {
		return fmt.Errorf("darwin hotkey: already registered: %s", key)
	}
	m.registered[key] = struct{}{}
	return nil
}

// unregister clears state synchronously but intentionally emits no synthetic
// release. Voice.Stop unregisters after its Events consumer exits, so sending
// to a full public channel here would turn shutdown into a deadlock.
func (m *darwinEdgeMachine) unregister(key platform.Key) {
	delete(m.registered, key)
	delete(m.pending, key)
	delete(m.activeMods, key)
	for physical, binding := range m.activeCode {
		if binding == key {
			delete(m.activeCode, physical)
		}
	}
}

func (m *darwinEdgeMachine) edge(keycode uint16, pressed bool, now time.Time) []platform.KeyEvent {
	if m.down[keycode] == pressed {
		return m.commit(now)
	}
	m.down[keycode] = pressed
	mods := m.modifiers()
	var events []platform.KeyEvent
	for physical, binding := range m.activeCode {
		if (!pressed && physical == keycode) || binding.Mods != mods {
			delete(m.activeCode, physical)
			events = append(events, platform.KeyEvent{Key: binding, Pressed: false})
		}
	}

	if _, modifier := darwinModifierKeys[keycode]; modifier {
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
					if _, pending := m.pending[key]; !pending {
						m.pending[key] = now.Add(m.settle)
					}
				}
			}
		}
		events = append(events, m.commit(now)...)
		return events
	}

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
	code, ok := darwinVirtualToCode(keycode)
	if !ok {
		return events
	}
	binding := platform.Key{Mods: mods, Code: code}
	if _, registered := m.registered[binding]; registered {
		m.activeCode[keycode] = binding
		events = append(events, platform.KeyEvent{Key: binding, Pressed: true})
	}
	return events
}

func (m *darwinEdgeMachine) commit(now time.Time) []platform.KeyEvent {
	if m.anyNonModifierDown() {
		return nil
	}
	mods := m.modifiers()
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

func (m *darwinEdgeMachine) modifiers() platform.Modifiers {
	var mods platform.Modifiers
	for keycode, modifier := range darwinModifierKeys {
		if m.down[keycode] {
			mods |= modifier
		}
	}
	return mods
}

func (m *darwinEdgeMachine) anyNonModifierDown() bool {
	for keycode, down := range m.down {
		if !down {
			continue
		}
		if _, modifier := darwinModifierKeys[keycode]; !modifier {
			return true
		}
	}
	return false
}

func validateDarwinBinding(key platform.Key) error {
	const knownMods = platform.ModCtrl | platform.ModAlt | platform.ModSuper | platform.ModShift
	if key.Mods&^knownMods != 0 {
		return fmt.Errorf("darwin hotkey: unsupported modifier mask %#x", key.Mods)
	}
	if key.Code == platform.KeyNone {
		if key.Mods == 0 {
			return fmt.Errorf("darwin hotkey: empty binding")
		}
		return nil
	}
	if _, ok := darwinVirtualKey(key.Code); !ok {
		return fmt.Errorf("darwin hotkey: unsupported key code %q", key.Code)
	}
	return nil
}

func darwinVirtualKey(code platform.KeyCode) (uint16, bool) {
	if keycode, ok := darwinCodeToVirtual[code]; ok {
		return keycode, true
	}
	name := string(code)
	if len(name) >= 2 && name[0] == 'F' {
		n, ok := parseDarwinDecimal(name[1:])
		if !ok {
			return 0, false
		}
		keycode, ok := darwinFunctionKeys[n]
		return keycode, ok
	}
	return 0, false
}

func darwinVirtualToCode(keycode uint16) (platform.KeyCode, bool) {
	for code, candidate := range darwinCodeToVirtual {
		if candidate == keycode {
			return code, true
		}
	}
	for number, candidate := range darwinFunctionKeys {
		if candidate == keycode {
			code, _ := platform.FunctionKey(number)
			return code, true
		}
	}
	return platform.KeyNone, false
}

func parseDarwinDecimal(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	value := 0
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0, false
		}
		value = value*10 + int(char-'0')
	}
	return value, true
}
