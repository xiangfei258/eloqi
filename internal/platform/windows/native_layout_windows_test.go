//go:build windows

package windows

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestWindowsNativeStructLayouts(t *testing.T) {
	t.Parallel()
	var wantInput, wantKeyboard, wantWaveHeader uintptr
	switch runtime.GOARCH {
	case "386":
		wantInput, wantKeyboard, wantWaveHeader = 28, 16, 32
	case "amd64", "arm64":
		wantInput, wantKeyboard, wantWaveHeader = 40, 24, 48
	default:
		t.Skipf("layout expectations are not defined for %s", runtime.GOARCH)
	}
	if got := unsafe.Sizeof(input{}); got != wantInput {
		t.Fatalf("INPUT size = %d, want %d", got, wantInput)
	}
	if got := unsafe.Sizeof(keyboardInput{}); got != wantKeyboard {
		t.Fatalf("KEYBDINPUT size = %d, want %d", got, wantKeyboard)
	}
	if got := unsafe.Sizeof(waveHeader{}); got != wantWaveHeader {
		t.Fatalf("WAVEHDR size = %d, want %d", got, wantWaveHeader)
	}
	if got := unsafe.Offsetof(input{}.data); got != unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("INPUT union offset = %d, want pointer size %d", got, unsafe.Sizeof(uintptr(0)))
	}
}
