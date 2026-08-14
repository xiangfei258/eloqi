package mock

import (
	"errors"
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

var _ platform.Hotkey = (*Hotkey)(nil)

// Hotkey is an in-memory platform.Hotkey for tests. Events are delivered on an
// internal buffered channel; Emit pushes a synthetic event into it.
type Hotkey struct {
	mu sync.Mutex

	// RegisterErr and UnregisterErr, when non-nil, are returned by Register
	// and Unregister instead of mutating state.
	RegisterErr   error
	UnregisterErr error

	events     chan platform.KeyEvent
	registered map[platform.Key]bool
	closed     bool
}

// NewHotkey returns a Hotkey with a buffered event channel.
func NewHotkey() *Hotkey {
	return &Hotkey{
		events:     make(chan platform.KeyEvent, 64),
		registered: make(map[platform.Key]bool),
	}
}

// Register implements platform.Hotkey.
func (h *Hotkey) Register(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("mock hotkey: closed")
	}
	if h.RegisterErr != nil {
		return h.RegisterErr
	}
	if h.registered[key] {
		return errors.New("mock hotkey: already registered")
	}
	h.registered[key] = true
	return nil
}

// Unregister implements platform.Hotkey.
func (h *Hotkey) Unregister(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.UnregisterErr != nil {
		return h.UnregisterErr
	}
	delete(h.registered, key)
	return nil
}

// Events implements platform.Hotkey.
func (h *Hotkey) Events() <-chan platform.KeyEvent {
	return h.events
}

// Close implements platform.Hotkey. It is idempotent and closes the event
// channel. Emit must not be called after Close.
func (h *Hotkey) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	close(h.events)
	return nil
}

// Emit delivers a synthetic event through the event channel. It is a test
// helper and must not be called after Close.
func (h *Hotkey) Emit(e platform.KeyEvent) {
	h.events <- e
}

// Registered reports whether key is currently registered.
func (h *Hotkey) Registered(key platform.Key) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.registered[key]
}

// Closed reports whether Close was called.
func (h *Hotkey) Closed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}
