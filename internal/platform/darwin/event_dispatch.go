package darwin

import (
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// darwinEventDispatcher keeps the CGEventTap thread independent of a stalled
// public Events consumer while preserving edge order.
type darwinEventDispatcher struct {
	output chan platform.KeyEvent
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}

	mu     sync.Mutex
	queue  []platform.KeyEvent
	closed bool
	once   sync.Once
}

func newDarwinEventDispatcher(output chan platform.KeyEvent) *darwinEventDispatcher {
	dispatcher := &darwinEventDispatcher{
		output: output,
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

func (d *darwinEventDispatcher) enqueue(events []platform.KeyEvent) bool {
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

func (d *darwinEventDispatcher) close() {
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.queue = nil
		d.mu.Unlock()
		close(d.stop)
	})
	<-d.done
}

func (d *darwinEventDispatcher) run() {
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
