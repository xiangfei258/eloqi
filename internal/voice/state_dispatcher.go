package voice

import "sync"

// stateDispatcher is an ordered, unbounded handoff between the state machine
// and OnStateChange. Enqueue never waits for the user callback, so a blocked
// observer cannot apply backpressure while Voice.mu is held.
type stateDispatcher struct {
	mu      sync.Mutex
	ready   *sync.Cond
	pending []State
	head    int
	closed  bool
	done    chan struct{}
}

func newStateDispatcher() *stateDispatcher {
	d := &stateDispatcher{done: make(chan struct{})}
	d.ready = sync.NewCond(&d.mu)
	return d
}

func (d *stateDispatcher) enqueue(state State) {
	d.mu.Lock()
	if !d.closed {
		d.pending = append(d.pending, state)
		d.ready.Signal()
	}
	d.mu.Unlock()
}

func (d *stateDispatcher) run(callback func(State)) {
	defer close(d.done)
	for {
		d.mu.Lock()
		for d.head == len(d.pending) && !d.closed {
			d.ready.Wait()
		}
		if d.head == len(d.pending) && d.closed {
			d.mu.Unlock()
			return
		}
		state := d.pending[d.head]
		d.head++
		// Periodically discard delivered entries so a long-running process
		// does not retain callback history forever.
		if d.head >= 64 && d.head*2 >= len(d.pending) {
			copy(d.pending, d.pending[d.head:])
			d.pending = d.pending[:len(d.pending)-d.head]
			d.head = 0
		}
		d.mu.Unlock()

		callback(state)
	}
}

func (d *stateDispatcher) closeAndWait() {
	d.mu.Lock()
	d.closed = true
	d.ready.Broadcast()
	d.mu.Unlock()
	<-d.done
}
