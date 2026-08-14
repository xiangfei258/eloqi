//go:build linux

package linux

import "testing"

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
