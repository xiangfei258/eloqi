package mock

import (
	"errors"
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

var _ platform.Overlay = (*Overlay)(nil)

// OverlayCall records one successful Show invocation.
type OverlayCall struct {
	State   platform.OverlayState
	Message string
}

// Overlay is a deterministic in-memory platform.Overlay implementation.
type Overlay struct {
	mu sync.Mutex

	ShowErr  error
	HideErr  error
	CloseErr error

	calls      []OverlayCall
	hideCount  int
	closeCount int
	visible    bool
	closed     bool
}

// Show implements platform.Overlay.
func (o *Overlay) Show(state platform.OverlayState, message string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("mock overlay: closed")
	}
	if o.ShowErr != nil {
		return o.ShowErr
	}
	o.calls = append(o.calls, OverlayCall{State: state, Message: message})
	o.visible = true
	return nil
}

// Hide implements platform.Overlay.
func (o *Overlay) Hide() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	if o.HideErr != nil {
		return o.HideErr
	}
	o.hideCount++
	o.visible = false
	return nil
}

// Close implements platform.Overlay.
func (o *Overlay) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	if o.CloseErr != nil {
		return o.CloseErr
	}
	o.closeCount++
	o.closed = true
	o.visible = false
	return nil
}

// Calls returns a copy of successful Show calls.
func (o *Overlay) Calls() []OverlayCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]OverlayCall(nil), o.calls...)
}

// HideCount reports successful Hide calls.
func (o *Overlay) HideCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hideCount
}

// CloseCount reports successful Close calls.
func (o *Overlay) CloseCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closeCount
}

// Visible reports the current mock visibility.
func (o *Overlay) Visible() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.visible
}
