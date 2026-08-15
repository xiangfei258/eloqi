//go:build windows

package windows

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	procOpenClipboard     = user32.NewProc("OpenClipboard")
	procCloseClipboard    = user32.NewProc("CloseClipboard")
	procEmptyClipboard    = user32.NewProc("EmptyClipboard")
	procGetClipboardData  = user32.NewProc("GetClipboardData")
	procSetClipboardData  = user32.NewProc("SetClipboardData")
	procIsClipboardFormat = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalAlloc       = kernel32.NewProc("GlobalAlloc")
	procGlobalFree        = kernel32.NewProc("GlobalFree")
	procGlobalLock        = kernel32.NewProc("GlobalLock")
	procGlobalUnlock      = kernel32.NewProc("GlobalUnlock")
	procGlobalSize        = kernel32.NewProc("GlobalSize")
)

// Clipboard implements plain-text clipboard access with CF_UNICODETEXT. It
// retries briefly because another desktop application may hold the clipboard
// open while processing a notification.
type Clipboard struct{}

var _ platform.Clipboard = (*Clipboard)(nil)

func NewClipboard() (*Clipboard, error) {
	return &Clipboard{}, nil
}

func (c *Clipboard) Read() (string, error) {
	available, _, _ := procIsClipboardFormat.Call(cfUnicodeText)
	if available == 0 {
		return "", nil
	}
	if err := openWindowsClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	handle, _, callErr := procGetClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return "", fmt.Errorf("windows clipboard: GetClipboardData(CF_UNICODETEXT): %v", callErr)
	}
	size, _, callErr := procGlobalSize.Call(handle)
	if size == 0 {
		return "", fmt.Errorf("windows clipboard: GlobalSize: %v", callErr)
	}
	pointer, _, callErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		return "", fmt.Errorf("windows clipboard: GlobalLock: %v", callErr)
	}
	defer procGlobalUnlock.Call(handle)

	bytes := make([]byte, int(size))
	if len(bytes) != 0 {
		procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&bytes[0])), pointer, size)
	}
	return decodeWindowsClipboardText(bytes)
}

func (c *Clipboard) Write(text string) error {
	bytesCopy, err := encodeWindowsClipboardText(text)
	if err != nil {
		return fmt.Errorf("windows clipboard: encode Unicode text: %w", err)
	}
	handle, _, callErr := procGlobalAlloc.Call(gmemMoveable, uintptr(len(bytesCopy)))
	if handle == 0 {
		return fmt.Errorf("windows clipboard: GlobalAlloc: %v", callErr)
	}
	owned := true
	defer func() {
		if owned {
			procGlobalFree.Call(handle)
		}
	}()

	pointer, _, callErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		return fmt.Errorf("windows clipboard: GlobalLock: %v", callErr)
	}
	if len(bytesCopy) != 0 {
		procRtlMoveMemory.Call(pointer, uintptr(unsafe.Pointer(&bytesCopy[0])), uintptr(len(bytesCopy)))
	}
	procGlobalUnlock.Call(handle)

	if err := openWindowsClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()
	if result, _, callErr := procEmptyClipboard.Call(); result == 0 {
		return fmt.Errorf("windows clipboard: EmptyClipboard: %v", callErr)
	}
	if result, _, callErr := procSetClipboardData.Call(cfUnicodeText, handle); result == 0 {
		return fmt.Errorf("windows clipboard: SetClipboardData(CF_UNICODETEXT): %v", callErr)
	}
	// Ownership transfers to the system only after SetClipboardData succeeds.
	owned = false
	return nil
}

func openWindowsClipboard() error {
	deadline := time.Now().Add(250 * time.Millisecond)
	var lastErr error
	for {
		if result, _, callErr := procOpenClipboard.Call(0); result != 0 {
			return nil
		} else {
			lastErr = callErr
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("windows clipboard: OpenClipboard: %v", lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
