// Package overlay connects voice lifecycle changes to a platform status
// capsule without allowing a slow desktop UI backend to block audio control.
package overlay

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/voice"
)

const (
	updateBuffer       = 8
	defaultCallTimeout = 2 * time.Second
)

// Config describes one asynchronous overlay controller.
type Config struct {
	Backend platform.Overlay
	OnError func(error)
	// CallTimeout bounds every native backend operation. Zero selects two
	// seconds. A timed-out backend is quarantined so no concurrent native calls
	// are started against an unknown UI state.
	CallTimeout time.Duration
}

type update struct {
	state   voice.State
	message string
}

// Controller serializes and coalesces overlay updates on a dedicated
// goroutine. StateChanged and ShowError never invoke native UI code directly.
type Controller struct {
	backend platform.Overlay
	onError func(error)

	mu       sync.Mutex
	closed   bool
	hasLast  bool
	last     update
	closeErr error
	stalled  bool

	updates chan update
	stop    chan struct{}
	done    chan struct{}
	close   sync.Once
	timeout time.Duration
}

// New creates and starts a controller.
func New(cfg Config) (*Controller, error) {
	if cfg.Backend == nil {
		return nil, errors.New("overlay: backend is required")
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultCallTimeout
	}
	c := &Controller{
		backend: cfg.Backend,
		onError: cfg.OnError,
		updates: make(chan update, updateBuffer),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		timeout: cfg.CallTimeout,
	}
	go c.run()
	return c, nil
}

// StateChanged queues a lifecycle state for display.
func (c *Controller) StateChanged(state voice.State) {
	c.enqueue(update{state: state})
}

// ShowError queues an error state with a concise single-line message.
func (c *Controller) ShowError(err error) {
	if err == nil {
		return
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	c.enqueue(update{state: voice.StateError, message: message})
}

func (c *Controller) enqueue(next update) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || (c.hasLast && c.last == next) {
		return
	}
	c.hasLast = true
	c.last = next
	select {
	case c.updates <- next:
		return
	default:
	}
	// The user only needs the freshest visible state. Discard one stale update
	// rather than delaying the voice state machine behind a full UI queue.
	select {
	case <-c.updates:
	default:
	}
	select {
	case c.updates <- next:
	default:
	}
}

func (c *Controller) run() {
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			c.finish()
			return
		default:
		}
		select {
		case <-c.stop:
			c.finish()
			return
		case next := <-c.updates:
			if err := c.apply(next); err != nil && c.onError != nil {
				c.onError(err)
			}
		}
	}
}

func (c *Controller) apply(next update) error {
	switch next.state {
	case voice.StateIdle:
		if err := c.invoke("hide idle state", c.backend.Hide); err != nil {
			return fmt.Errorf("overlay: hide idle state: %w", err)
		}
		return nil
	case voice.StateConnecting:
		return c.show(platform.OverlayConnecting, next.message)
	case voice.StateRecording:
		return c.show(platform.OverlayRecording, next.message)
	case voice.StateStoppingDelayed:
		return c.show(platform.OverlayStopping, next.message)
	case voice.StateStopping:
		return c.show(platform.OverlayWaiting, next.message)
	case voice.StateError:
		return c.show(platform.OverlayError, next.message)
	default:
		return fmt.Errorf("overlay: unsupported voice state %q", next.state)
	}
}

func (c *Controller) show(state platform.OverlayState, message string) error {
	if err := c.invoke("show "+string(state)+" state", func() error { return c.backend.Show(state, message) }); err != nil {
		return fmt.Errorf("overlay: show %s state: %w", state, err)
	}
	return nil
}

func (c *Controller) finish() {
	err := errors.Join(
		c.invoke("hide during close", c.backend.Hide),
		c.invoke("close backend", c.backend.Close),
	)
	c.mu.Lock()
	c.closeErr = err
	c.mu.Unlock()
	if err != nil && c.onError != nil {
		c.onError(fmt.Errorf("overlay: close: %w", err))
	}
}

func (c *Controller) invoke(operation string, call func() error) error {
	c.mu.Lock()
	if c.stalled {
		c.mu.Unlock()
		return fmt.Errorf("%s skipped because a previous backend call timed out", operation)
	}
	timeout := c.timeout
	c.mu.Unlock()

	result := make(chan error, 1)
	go func() { result <- call() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		c.mu.Lock()
		c.stalled = true
		c.mu.Unlock()
		return fmt.Errorf("%s timed out after %s", operation, timeout)
	}
}

// Close stops the worker and releases the native overlay. It is idempotent.
func (c *Controller) Close() error {
	if c == nil {
		return nil
	}
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		close(c.stop)
		c.mu.Unlock()
	})
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErr
}
