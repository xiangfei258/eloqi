package platform

import "testing"

func TestModifiersString(t *testing.T) {
	tests := []struct {
		name string
		mods Modifiers
		want string
	}{
		{"zero", 0, ""},
		{"ctrl", ModCtrl, "Ctrl"},
		{"alt", ModAlt, "Alt"},
		{"super", ModSuper, "Super"},
		{"shift", ModShift, "Shift"},
		{"ctrl+alt", ModCtrl | ModAlt, "Ctrl+Alt"},
		{"alt+shift", ModAlt | ModShift, "Alt+Shift"},
		{"all", ModCtrl | ModAlt | ModSuper | ModShift, "Ctrl+Alt+Super+Shift"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mods.String(); got != tt.want {
				t.Fatalf("Modifiers.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeyString(t *testing.T) {
	tests := []struct {
		name string
		key  Key
		want string
	}{
		{"empty", Key{}, ""},
		{"modifier only", Key{Mods: ModAlt | ModSuper}, "Alt+Super"},
		{"modifier + key", Key{Mods: ModCtrl, Code: KeyTab}, "Ctrl+Tab"},
		{"bare key", Key{Code: KeyCapsLock}, "CapsLock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.String(); got != tt.want {
				t.Fatalf("Key.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFunctionKey(t *testing.T) {
	tests := []struct {
		n    int
		code KeyCode
		ok   bool
	}{
		{-1, KeyNone, false},
		{0, KeyNone, false},
		{1, "F1", true},
		{12, "F12", true},
		{24, "F24", true},
		{25, KeyNone, false},
	}
	for _, tt := range tests {
		code, ok := FunctionKey(tt.n)
		if code != tt.code || ok != tt.ok {
			t.Errorf("FunctionKey(%d) = (%q, %v), want (%q, %v)",
				tt.n, code, ok, tt.code, tt.ok)
		}
	}
}

func TestKeyComparableAsMapKey(t *testing.T) {
	// Key must be usable as a map key so mock.Hotkey can track registrations.
	m := map[Key]bool{
		{Mods: ModCtrl, Code: KeyTab}: true,
	}
	if !m[Key{Mods: ModCtrl, Code: KeyTab}] {
		t.Fatal("expected struct-equivalent Key to match the map entry")
	}
}
