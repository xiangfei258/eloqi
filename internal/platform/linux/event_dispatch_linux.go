//go:build linux

package linux

import (
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// x11EventDispatcher keeps Xlib ownership and command handling independent
// from public Events backpressure. Close abandons queued output deliberately:
// once the consumer exits, shutdown must take precedence over stale edges.
type x11EventDispatcher struct {
	output chan platform.KeyEvent
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}

	mu     sync.Mutex
	queue  []platform.KeyEvent
	closed bool
	once   sync.Once
}

func newX11EventDispatcher(output chan platform.KeyEvent) *x11EventDispatcher {
	dispatcher := &x11EventDispatcher{
		output: output,
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

func (d *x11EventDispatcher) enqueue(event platform.KeyEvent) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	d.queue = append(d.queue, event)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
	return true
}

func (d *x11EventDispatcher) close() {
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.queue = nil
		d.mu.Unlock()
		close(d.stop)
	})
	<-d.done
}

func (d *x11EventDispatcher) run() {
	defer close(d.done)
	defer close(d.output)
	for {
		d.mu.Lock()
		var event platform.KeyEvent
		haveEvent := len(d.queue) != 0
		if haveEvent {
			event = d.queue[0]
			d.queue[0] = platform.KeyEvent{}
			d.queue = d.queue[1:]
		}
		d.mu.Unlock()
		if haveEvent {
			select {
			case d.output <- event:
			case <-d.stop:
				return
			}
			continue
		}
		select {
		case <-d.wake:
		case <-d.stop:
			return
		}
	}
}
