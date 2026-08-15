//go:build darwin && cgo

package darwin

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// Hotkey observes global keyboard edges through a listen-only CGEventTap.
// The tap runs on one locked OS thread with its own Core Foundation run loop;
// it never modifies or suppresses an event.
type Hotkey struct {
	mu         sync.Mutex
	machine    *darwinEdgeMachine
	events     chan platform.KeyEvent
	dispatcher *darwinEventDispatcher
	stop       chan struct{}
	closed     atomic.Bool
	wg         sync.WaitGroup
}

var _ platform.Hotkey = (*Hotkey)(nil)

func NewHotkey() (*Hotkey, error) {
	hotkey := &Hotkey{
		machine: newDarwinEdgeMachine(),
		events:  make(chan platform.KeyEvent, 64),
		stop:    make(chan struct{}),
	}
	hotkey.dispatcher = newDarwinEventDispatcher(hotkey.events)
	ready := make(chan error, 1)
	hotkey.wg.Add(1)
	go hotkey.run(ready)
	if err := <-ready; err != nil {
		hotkey.wg.Wait()
		return nil, err
	}
	return hotkey, nil
}

func (h *Hotkey) Register(key platform.Key) error {
	if h.closed.Load() {
		return fmt.Errorf("darwin hotkey: closed")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.machine.register(key)
}

func (h *Hotkey) Unregister(key platform.Key) error {
	if h.closed.Load() {
		return fmt.Errorf("darwin hotkey: closed")
	}
	h.mu.Lock()
	h.machine.unregister(key)
	h.mu.Unlock()
	return nil
}

func (h *Hotkey) Events() <-chan platform.KeyEvent {
	return h.events
}

func (h *Hotkey) Close() error {
	if h.closed.CompareAndSwap(false, true) {
		close(h.stop)
	}
	h.wg.Wait()
	return nil
}

func (h *Hotkey) run(ready chan<- error) {
	defer h.wg.Done()
	defer h.dispatcher.close()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tap, err := createNativeEventTap()
	if err != nil {
		ready <- err
		return
	}
	defer tap.close()
	ready <- nil

	for {
		select {
		case <-h.stop:
			return
		default:
		}
		event, observed, err := tap.next(0.01)
		if err != nil {
			return
		}
		now := time.Now()
		h.mu.Lock()
		var events []platform.KeyEvent
		if observed {
			events = h.machine.edge(event.keycode, event.pressed, now)
		} else {
			events = h.machine.commit(now)
		}
		h.mu.Unlock()
		if !h.emit(events) {
			return
		}
	}
}

func (h *Hotkey) emit(events []platform.KeyEvent) bool {
	return h.dispatcher.enqueue(events)
}
