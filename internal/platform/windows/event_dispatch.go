//go:build windows

package windows

import (
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// windowsEventDispatcher separates the native owner loop from public Events
// backpressure. Its queue is intentionally private and ordered; shutdown drops
// any abandoned tail so Close remains bounded after the consumer exits.
type windowsEventDispatcher struct {
	output chan platform.KeyEvent
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}

	mu     sync.Mutex
	queue  []platform.KeyEvent
	closed bool
	once   sync.Once
}

func newWindowsEventDispatcher(output chan platform.KeyEvent) *windowsEventDispatcher {
	dispatcher := &windowsEventDispatcher{
		output: output,
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

func (d *windowsEventDispatcher) enqueue(events []platform.KeyEvent) bool {
	if len(events) == 0 {
		return true
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	d.queue = append(d.queue, events...)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
	return true
}

func (d *windowsEventDispatcher) close() {
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.queue = nil
		d.mu.Unlock()
		close(d.stop)
	})
	<-d.done
}

func (d *windowsEventDispatcher) run() {
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
