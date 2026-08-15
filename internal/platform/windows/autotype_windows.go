//go:build windows

package windows

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	inputKeyboard = 1
	keyEventKeyUp = 0x0002
	virtualCtrl   = 0x11
	virtualV      = 0x56
)

var procSendInput = user32.NewProc("SendInput")

type mouseInput struct {
	dx        int32
	dy        int32
	mouseData uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

type keyboardInput struct {
	virtualKey uint16
	scanCode   uint16
	flags      uint32
	time       uint32
	extraInfo  uintptr
}

// input uses mouseInput for the union's storage because MOUSEINPUT is the
// largest INPUT union member on both 32-bit and 64-bit Windows. Its uintptr
// field also gives the union the native alignment required after Type.
type input struct {
	typeID uint32
	data   mouseInput
}

type Autotype struct {
	clipboard platform.Clipboard
}

var _ platform.Autotype = (*Autotype)(nil)

func NewAutotype(clipboard platform.Clipboard) (*Autotype, error) {
	if clipboard == nil {
		return nil, fmt.Errorf("windows autotype: clipboard is required")
	}
	return &Autotype{clipboard: clipboard}, nil
}

// Type writes Unicode text to the clipboard, then injects one Ctrl+V chord.
// SendInput is used instead of per-character virtual keys so arbitrary Unicode
// follows the target application's normal paste path.
func (a *Autotype) Type(text string) error {
	if err := a.clipboard.Write(text); err != nil {
		return fmt.Errorf("windows autotype: write clipboard: %w", err)
	}
	inputs := []input{
		newKeyboardInput(virtualCtrl, false),
		newKeyboardInput(virtualV, false),
		newKeyboardInput(virtualV, true),
		newKeyboardInput(virtualCtrl, true),
	}
	sent, _, callErr := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	runtime.KeepAlive(inputs)
	if sent != uintptr(len(inputs)) {
		// A rare partial insertion must not leave Ctrl or V logically held.
		cleanup := []input{
			newKeyboardInput(virtualV, true),
			newKeyboardInput(virtualCtrl, true),
		}
		procSendInput.Call(
			uintptr(len(cleanup)),
			uintptr(unsafe.Pointer(&cleanup[0])),
			unsafe.Sizeof(cleanup[0]),
		)
		runtime.KeepAlive(cleanup)
		return fmt.Errorf("windows autotype: SendInput sent %d of %d events: %v (the focused process may run at a higher integrity level)", sent, len(inputs), callErr)
	}
	return nil
}

func newKeyboardInput(virtualKey uint16, released bool) input {
	value := input{typeID: inputKeyboard}
	keyboard := (*keyboardInput)(unsafe.Pointer(&value.data))
	keyboard.virtualKey = virtualKey
	if released {
		keyboard.flags = keyEventKeyUp
	}
	return value
}
