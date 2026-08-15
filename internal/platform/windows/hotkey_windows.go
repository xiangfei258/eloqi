//go:build windows

package windows

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	whKeyboardLL = 13

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
	wmQuit       = 0x0012

	llkhfInjected = 0x10
	pmNoRemove    = 0x0000

	hotkeyCloseTimeout = 2 * time.Second
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPeekMessageW        = user32.NewProc("PeekMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procGetCurrentThreadID  = kernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procRtlMoveMemory       = kernel32.NewProc("RtlMoveMemory")
)

type rawWindowsKeyEdge struct {
	vk      uint16
	pressed bool
}

type hotkeyCommand struct {
	register bool
	key      platform.Key
	response chan error
}

type windowsPoint struct {
	x int32
	y int32
}

type windowsMessage struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	point   windowsPoint
	private uint32
}

type keyboardHookData struct {
	vkCode    uint32
	scanCode  uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// Hotkey combines a low-latency GetAsyncKeyState poller with an observing
// WH_KEYBOARD_LL hook. The hook always forwards events to the next hook and
// exists only to preserve very short physical edges between poll intervals.
type Hotkey struct {
	events     chan platform.KeyEvent
	dispatcher *windowsEventDispatcher
	raw        chan rawWindowsKeyEdge
	commands   chan hotkeyCommand
	stop       chan struct{}
	done       chan struct{}

	closed   atomic.Bool
	threadID atomic.Uint32
	wg       sync.WaitGroup

	operationTimeout time.Duration
	postThread       func(threadID uint32, message uint32) error
}

var _ platform.Hotkey = (*Hotkey)(nil)

var (
	activeHookMu     sync.Mutex
	activeHookEvents chan<- rawWindowsKeyEdge
	keyboardHookProc = syscall.NewCallback(lowLevelKeyboardProc)
)

// NewHotkey installs an observation-only low-level keyboard hook and starts a
// 5 ms physical-state poller. Windows delivers neither stream exclusively:
// feeding both through edgeMachine avoids repeats and repairs missed edges.
func NewHotkey() (*Hotkey, error) {
	h := &Hotkey{
		events:   make(chan platform.KeyEvent, 64),
		raw:      make(chan rawWindowsKeyEdge, 512),
		commands: make(chan hotkeyCommand, 16),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	activeHookMu.Lock()
	if activeHookEvents != nil {
		activeHookMu.Unlock()
		return nil, fmt.Errorf("windows hotkey: a low-level hook is already active")
	}
	activeHookEvents = h.raw
	activeHookMu.Unlock()
	h.dispatcher = newWindowsEventDispatcher(h.events)

	ready := make(chan error, 1)
	h.wg.Add(1)
	go h.hookLoop(ready)
	if err := <-ready; err != nil {
		h.wg.Wait()
		h.dispatcher.close()
		activeHookMu.Lock()
		if activeHookEvents == h.raw {
			activeHookEvents = nil
		}
		activeHookMu.Unlock()
		return nil, err
	}

	h.wg.Add(1)
	go h.process()
	go func() {
		h.wg.Wait()
		close(h.done)
	}()
	return h, nil
}

func (h *Hotkey) Register(key platform.Key) error {
	return h.submit(hotkeyCommand{register: true, key: key})
}

func (h *Hotkey) Unregister(key platform.Key) error {
	return h.submit(hotkeyCommand{key: key})
}

func (h *Hotkey) Events() <-chan platform.KeyEvent {
	return h.events
}

func (h *Hotkey) Close() error {
	var postErr error
	if h.closed.CompareAndSwap(false, true) {
		close(h.stop)
		if threadID := h.threadID.Load(); threadID != 0 {
			postErr = h.postThreadMessage(threadID, wmQuit)
		}
	}
	done := h.done
	if done == nil {
		done = make(chan struct{})
		go func() {
			h.wg.Wait()
			close(done)
		}()
	}
	timer := time.NewTimer(h.timeout())
	defer timer.Stop()
	select {
	case <-done:
		return postErr
	case <-timer.C:
		var retryErr error
		if threadID := h.threadID.Load(); threadID != 0 {
			retryErr = h.postThreadMessage(threadID, wmQuit)
		}
		return errors.Join(
			postErr,
			retryErr,
			fmt.Errorf("windows hotkey: native threads did not exit within %s", h.timeout()),
		)
	}
}

func (h *Hotkey) timeout() time.Duration {
	if h.operationTimeout > 0 {
		return h.operationTimeout
	}
	return hotkeyCloseTimeout
}

func (h *Hotkey) postThreadMessage(threadID uint32, message uint32) error {
	if h.postThread != nil {
		return h.postThread(threadID, message)
	}
	if result, _, callErr := procPostThreadMessageW.Call(uintptr(threadID), uintptr(message), 0, 0); result == 0 {
		return fmt.Errorf("PostThreadMessageW: %w", callErr)
	}
	return nil
}

func (h *Hotkey) submit(command hotkeyCommand) error {
	if h.closed.Load() {
		return fmt.Errorf("windows hotkey: closed")
	}
	command.response = make(chan error, 1)
	select {
	case h.commands <- command:
	case <-h.stop:
		return fmt.Errorf("windows hotkey: closed")
	}
	select {
	case err := <-command.response:
		return err
	case <-h.stop:
		return fmt.Errorf("windows hotkey: closed")
	}
}

func (h *Hotkey) process() {
	h.processWithMachine(newEdgeMachine())
}

// processWithMachine keeps the native event loop independent from the
// deterministic edge state so command-boundary regressions can be exercised
// without installing a system hook.
func (h *Hotkey) processWithMachine(machine *edgeMachine) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	h.processWithMachineTicks(machine, ticker.C)
}

func (h *Hotkey) processWithMachineTicks(machine *edgeMachine, ticks <-chan time.Time) {
	defer h.wg.Done()
	dispatcher := h.dispatcher
	if dispatcher == nil {
		dispatcher = newWindowsEventDispatcher(h.events)
		h.dispatcher = dispatcher
	}
	defer dispatcher.close()

	emit := func(events []platform.KeyEvent) bool {
		return dispatcher.enqueue(events)
	}

	for {
		select {
		case edge := <-h.raw:
			if !emit(machine.edge(edge.vk, edge.pressed, time.Now())) {
				return
			}
		case command := <-h.commands:
			if command.register {
				command.response <- machine.register(command.key)
			} else {
				machine.unregister(command.key)
				command.response <- nil
			}
		case now := <-ticks:
			if !h.poll(machine, now, emit) {
				return
			}
		case <-h.stop:
			return
		}
	}
}

func (h *Hotkey) poll(machine *edgeMachine, now time.Time, emit func([]platform.KeyEvent) bool) bool {
	// While a modifier-only chord is pending or active, reconcile every
	// keyboard VK. This makes an unregistered extra key cancel the chord even
	// if its low-level-hook observation was lost under extreme input pressure.
	keys := trackedWindowsKeys(machine.registered, len(machine.pending) != 0 || len(machine.activeMods) != 0)
	type polledEdge struct {
		vk      uint16
		pressed bool
	}
	pressedModifiers := make([]polledEdge, 0, len(keys))
	pressedOrdinary := make([]polledEdge, 0, len(keys))
	releasedOrdinary := make([]polledEdge, 0, len(keys))
	releasedModifiers := make([]polledEdge, 0, len(keys))
	for _, vk := range keys {
		down := getAsyncKeyDown(vk)
		if down == machine.isDown(vk) {
			continue
		}
		_, modifier := windowsModifierKeys[vk]
		switch {
		case down && modifier:
			pressedModifiers = append(pressedModifiers, polledEdge{vk: vk, pressed: true})
		case down:
			pressedOrdinary = append(pressedOrdinary, polledEdge{vk: vk, pressed: true})
		case modifier:
			releasedModifiers = append(releasedModifiers, polledEdge{vk: vk})
		default:
			releasedOrdinary = append(releasedOrdinary, polledEdge{vk: vk})
		}
	}
	// A snapshot has no ordering information. This ordering reconstructs the
	// only sequence that can form a chord: modifiers down before its main key,
	// and the main key up before modifiers are released.
	for _, group := range [][]polledEdge{pressedModifiers, pressedOrdinary, releasedOrdinary, releasedModifiers} {
		for _, edge := range group {
			if !emit(machine.edge(edge.vk, edge.pressed, now)) {
				return false
			}
		}
	}
	return emit(machine.commit(now))
}

func getAsyncKeyDown(vk uint16) bool {
	state, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return uint16(state)&0x8000 != 0
}

func (h *Hotkey) hookLoop(ready chan<- error) {
	defer h.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	threadID, _, _ := procGetCurrentThreadID.Call()
	h.threadID.Store(uint32(threadID))
	// Force creation of the thread message queue before another goroutine can
	// post WM_QUIT during an immediate Close.
	var initial windowsMessage
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&initial)), 0, 0, 0, pmNoRemove)

	module, _, _ := procGetModuleHandleW.Call(0)
	hook, _, callErr := procSetWindowsHookExW.Call(whKeyboardLL, keyboardHookProc, module, 0)
	if hook == 0 {
		ready <- fmt.Errorf("windows hotkey: install WH_KEYBOARD_LL: %v", callErr)
		return
	}
	ready <- nil

	var message windowsMessage
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
	procUnhookWindowsHookEx.Call(hook)
	h.threadID.Store(0)

	activeHookMu.Lock()
	if activeHookEvents == h.raw {
		activeHookEvents = nil
	}
	activeHookMu.Unlock()
}

// lowLevelKeyboardProc is strictly observing: every code path ends by calling
// CallNextHookEx, so Eloqui never consumes or replays Alt+Tab or any other
// system shortcut.
func lowLevelKeyboardProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) >= 0 && lParam != 0 {
		var data keyboardHookData
		procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&data)), lParam, unsafe.Sizeof(data))
		if data.flags&llkhfInjected == 0 {
			pressed := wParam == wmKeyDown || wParam == wmSysKeyDown
			released := wParam == wmKeyUp || wParam == wmSysKeyUp
			if pressed || released {
				activeHookMu.Lock()
				destination := activeHookEvents
				if destination != nil {
					select {
					case destination <- rawWindowsKeyEdge{
						vk:      normalizeWindowsHookKey(uint16(data.vkCode), data.scanCode, data.flags),
						pressed: pressed,
					}:
					default:
					}
				}
				activeHookMu.Unlock()
			}
		}
	}
	result, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return result
}
