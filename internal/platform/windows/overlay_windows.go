//go:build windows

package windows

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	wsPopup = 0x80000000

	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExTransparent = 0x00000020
	wsExLayered     = 0x00080000
	wsExNoActivate  = 0x08000000

	wmDestroy      = 0x0002
	wmPaint        = 0x000F
	wmClose        = 0x0010
	wmEraseBkgnd   = 0x0014
	wmNCHitTest    = 0x0084
	wmOverlayApply = 0x8001

	swHide           = 0
	swShowNoActivate = 4

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040

	lwaAlpha       = 0x00000002
	dtCenter       = 0x00000001
	dtVCenter      = 0x00000004
	dtSingleLine   = 0x00000020
	dtEndEllipsis  = 0x00008000
	transparentBkg = 1
	defaultGUIFont = 17

	overlayWidth  = 360
	overlayHeight = 54

	overlayOperationTimeout = 2 * time.Second
)

var (
	gdi32                          = syscall.NewLazyDLL("gdi32.dll")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procUnregisterClassW           = user32.NewProc("UnregisterClassW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procSetWindowPos               = user32.NewProc("SetWindowPos")
	procInvalidateRect             = user32.NewProc("InvalidateRect")
	procGetClientRect              = user32.NewProc("GetClientRect")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procSetWindowRgn               = user32.NewProc("SetWindowRgn")
	procBeginPaint                 = user32.NewProc("BeginPaint")
	procEndPaint                   = user32.NewProc("EndPaint")
	procFillRect                   = user32.NewProc("FillRect")
	procDrawTextW                  = user32.NewProc("DrawTextW")
	procSetBkMode                  = gdi32.NewProc("SetBkMode")
	procSetTextColor               = gdi32.NewProc("SetTextColor")
	procCreateSolidBrush           = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject               = gdi32.NewProc("DeleteObject")
	procCreateRoundRectRgn         = gdi32.NewProc("CreateRoundRectRgn")
	procGetStockObject             = gdi32.NewProc("GetStockObject")
	procSelectObject               = gdi32.NewProc("SelectObject")
)

type windowsRect struct {
	left   int32
	top    int32
	right  int32
	bottom int32
}

type paintStruct struct {
	dc        uintptr
	erase     int32
	paint     windowsRect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type windowClassEx struct {
	size        uint32
	style       uint32
	wndProc     uintptr
	classExtra  int32
	windowExtra int32
	instance    uintptr
	icon        uintptr
	cursor      uintptr
	background  uintptr
	menuName    *uint16
	className   *uint16
	iconSmall   uintptr
}

type overlayCommandKind uint8

const (
	overlayShow overlayCommandKind = iota
	overlayHide
	overlayClose
)

type overlayCommand struct {
	kind     overlayCommandKind
	state    platform.OverlayState
	message  string
	response chan error
}

// Overlay owns a transparent, topmost Win32 popup on a dedicated message
// thread. WS_EX_NOACTIVATE and HTTRANSPARENT keep focus and mouse input in the
// user's target application.
type Overlay struct {
	commands chan overlayCommand
	done     chan struct{}
	hwnd     atomic.Uintptr
	threadID atomic.Uint32
	closing  atomic.Bool
	wg       sync.WaitGroup

	operationTimeout time.Duration
	postWindow       func(hwnd uintptr, message uint32) error
	postThread       func(threadID uint32, message uint32) error

	// The following fields are owned by the window thread.
	text  string
	color uint32
}

var _ platform.Overlay = (*Overlay)(nil)

var (
	overlayWindowProc = syscall.NewCallback(overlayWndProc)
	overlayClassID    atomic.Uint32
	overlayRegistryMu sync.RWMutex
	overlayRegistry   = make(map[uintptr]*Overlay)
)

func NewOverlay() (*Overlay, error) {
	overlay := &Overlay{
		commands: make(chan overlayCommand, 16),
		done:     make(chan struct{}),
	}
	ready := make(chan error, 1)
	overlay.wg.Add(1)
	go overlay.windowLoop(ready)
	if err := <-ready; err != nil {
		overlay.wg.Wait()
		return nil, err
	}
	return overlay, nil
}

func (o *Overlay) Show(state platform.OverlayState, message string) error {
	if _, _, err := windowsOverlayPresentation(state, message); err != nil {
		return err
	}
	return o.submit(overlayCommand{kind: overlayShow, state: state, message: message})
}

func (o *Overlay) Hide() error {
	return o.submit(overlayCommand{kind: overlayHide})
}

func (o *Overlay) Close() error {
	if !o.closing.CompareAndSwap(false, true) {
		return o.waitUntilClosed(nil)
	}
	err := o.submitClosing(overlayCommand{kind: overlayClose})
	if err != nil {
		err = errors.Join(err, o.requestQuit())
	}
	return o.waitUntilClosed(err)
}

func (o *Overlay) submit(command overlayCommand) error {
	if o.closing.Load() {
		return fmt.Errorf("windows overlay: closed")
	}
	return o.submitClosing(command)
}

func (o *Overlay) submitClosing(command overlayCommand) error {
	timer := time.NewTimer(o.timeout())
	defer timer.Stop()
	command.response = make(chan error, 1)
	select {
	case o.commands <- command:
	case <-o.done:
		return fmt.Errorf("windows overlay: window thread stopped")
	case <-timer.C:
		return fmt.Errorf("windows overlay: command queue did not accept request within %s", o.timeout())
	}
	hwnd := o.hwnd.Load()
	if hwnd == 0 {
		return fmt.Errorf("windows overlay: window is unavailable")
	}
	if err := o.postWindowMessage(hwnd, wmOverlayApply); err != nil {
		return fmt.Errorf("windows overlay: wake window thread: %w", err)
	}
	select {
	case err := <-command.response:
		return err
	case <-o.done:
		select {
		case err := <-command.response:
			return err
		default:
			return fmt.Errorf("windows overlay: window thread stopped")
		}
	case <-timer.C:
		return fmt.Errorf("windows overlay: window thread did not respond within %s", o.timeout())
	}
}

func (o *Overlay) timeout() time.Duration {
	if o.operationTimeout > 0 {
		return o.operationTimeout
	}
	return overlayOperationTimeout
}

func (o *Overlay) postWindowMessage(hwnd uintptr, message uint32) error {
	if o.postWindow != nil {
		return o.postWindow(hwnd, message)
	}
	if result, _, callErr := procPostMessageW.Call(hwnd, uintptr(message), 0, 0); result == 0 {
		return callErr
	}
	return nil
}

func (o *Overlay) postThreadMessage(threadID uint32, message uint32) error {
	if o.postThread != nil {
		return o.postThread(threadID, message)
	}
	if result, _, callErr := procPostThreadMessageW.Call(uintptr(threadID), uintptr(message), 0, 0); result == 0 {
		return callErr
	}
	return nil
}

// requestQuit bypasses the command channel. In particular, PostMessage can
// fail after a close command has already been queued; posting WM_QUIT to the
// owning thread still wakes GetMessage and guarantees an exit path.
func (o *Overlay) requestQuit() error {
	attempted := false
	succeeded := false
	var failures []error
	if hwnd := o.hwnd.Load(); hwnd != 0 {
		attempted = true
		if err := o.postWindowMessage(hwnd, wmClose); err != nil {
			failures = append(failures, fmt.Errorf("post WM_CLOSE: %w", err))
		} else {
			succeeded = true
		}
	}
	if threadID := o.threadID.Load(); threadID != 0 {
		attempted = true
		if err := o.postThreadMessage(threadID, wmQuit); err != nil {
			failures = append(failures, fmt.Errorf("post WM_QUIT: %w", err))
		} else {
			succeeded = true
		}
	}
	if succeeded {
		return nil
	}
	if !attempted {
		return fmt.Errorf("windows overlay: no window thread available for shutdown")
	}
	return errors.Join(failures...)
}

func (o *Overlay) waitUntilClosed(prior error) error {
	timer := time.NewTimer(o.timeout())
	defer timer.Stop()
	select {
	case <-o.done:
		o.wg.Wait()
		return prior
	case <-timer.C:
		fallbackErr := o.requestQuit()
		return errors.Join(
			prior,
			fallbackErr,
			fmt.Errorf("windows overlay: window thread did not exit within %s", o.timeout()),
		)
	}
}

func (o *Overlay) windowLoop(ready chan<- error) {
	defer o.wg.Done()
	defer close(o.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	threadID, _, _ := procGetCurrentThreadID.Call()
	o.threadID.Store(uint32(threadID))
	defer o.threadID.Store(0)
	// Ensure PostThreadMessage has a queue to target even if Close races with
	// class or window creation.
	var initial windowsMessage
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&initial)), 0, 0, 0, pmNoRemove)

	instance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16FromString(fmt.Sprintf("EloquiOverlay_%d_%d", os.Getpid(), overlayClassID.Add(1)))
	windowName, _ := syscall.UTF16FromString("Eloqui status")
	class := windowClassEx{
		size:      uint32(unsafe.Sizeof(windowClassEx{})),
		wndProc:   overlayWindowProc,
		instance:  instance,
		className: &className[0],
	}
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		ready <- fmt.Errorf("windows overlay: RegisterClassExW: %v", callErr)
		return
	}
	defer procUnregisterClassW.Call(uintptr(unsafe.Pointer(&className[0])), instance)

	screenWidth, _, _ := procGetSystemMetrics.Call(0)
	screenHeight, _, _ := procGetSystemMetrics.Call(1)
	x := (int32(screenWidth) - overlayWidth) / 2
	y := int32(screenHeight) - overlayHeight - 72
	exStyle := uintptr(wsExTopmost | wsExToolWindow | wsExTransparent | wsExLayered | wsExNoActivate)
	hwnd, _, callErr := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(&className[0])),
		uintptr(unsafe.Pointer(&windowName[0])),
		uintptr(wsPopup),
		uintptr(x), uintptr(y), overlayWidth, overlayHeight,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("windows overlay: CreateWindowExW: %v", callErr)
		return
	}
	o.hwnd.Store(hwnd)
	overlayRegistryMu.Lock()
	overlayRegistry[hwnd] = o
	overlayRegistryMu.Unlock()
	defer func() {
		overlayRegistryMu.Lock()
		delete(overlayRegistry, hwnd)
		overlayRegistryMu.Unlock()
		o.hwnd.Store(0)
	}()

	procSetLayeredWindowAttributes.Call(hwnd, 0, 238, lwaAlpha)
	region, _, _ := procCreateRoundRectRgn.Call(0, 0, overlayWidth+1, overlayHeight+1, overlayHeight, overlayHeight)
	if region != 0 {
		// The system owns a successful SetWindowRgn region.
		if result, _, _ := procSetWindowRgn.Call(hwnd, region, 0); result == 0 {
			procDeleteObject.Call(region)
		}
	}
	ready <- nil

	var message windowsMessage
	for {
		result, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			return
		}
		if result == 0 {
			return
		}
		if message.message == wmOverlayApply {
			select {
			case command := <-o.commands:
				if o.apply(hwnd, command) {
					return
				}
			default:
				_ = callErr
			}
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func (o *Overlay) apply(hwnd uintptr, command overlayCommand) bool {
	switch command.kind {
	case overlayShow:
		text, color, err := windowsOverlayPresentation(command.state, command.message)
		if err == nil {
			o.text = text
			o.color = color
			procInvalidateRect.Call(hwnd, 0, 0)
			procShowWindow.Call(hwnd, swShowNoActivate)
			procSetWindowPos.Call(hwnd, ^uintptr(0), 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
		}
		command.response <- err
	case overlayHide:
		procShowWindow.Call(hwnd, swHide)
		command.response <- nil
	case overlayClose:
		command.response <- nil
		procDestroyWindow.Call(hwnd)
		return true
	}
	return false
}

func overlayWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	overlayRegistryMu.RLock()
	overlay := overlayRegistry[hwnd]
	overlayRegistryMu.RUnlock()
	if overlay != nil {
		switch uint32(message) {
		case wmPaint:
			overlay.paint(hwnd)
			return 0
		case wmEraseBkgnd:
			return 1
		case wmNCHitTest:
			return ^uintptr(0) // HTTRANSPARENT
		case wmClose:
			procDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
}

func (o *Overlay) paint(hwnd uintptr) {
	var paint paintStruct
	dc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	var bounds windowsRect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds)))
	brush, _, _ := procCreateSolidBrush.Call(uintptr(o.color))
	if brush != 0 {
		procFillRect.Call(dc, uintptr(unsafe.Pointer(&bounds)), brush)
		procDeleteObject.Call(brush)
	}
	procSetBkMode.Call(dc, transparentBkg)
	procSetTextColor.Call(dc, 0x00FFFFFF)
	font, _, _ := procGetStockObject.Call(defaultGUIFont)
	if font != 0 {
		previous, _, _ := procSelectObject.Call(dc, font)
		defer procSelectObject.Call(dc, previous)
	}
	text := windowsOverlayUTF16(o.text)
	procDrawTextW.Call(
		dc,
		uintptr(unsafe.Pointer(&text[0])),
		uintptr(^uint32(0)),
		uintptr(unsafe.Pointer(&bounds)),
		dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis,
	)
}
