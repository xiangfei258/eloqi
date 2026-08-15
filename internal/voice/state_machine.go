package voice

import (
	"errors"
	"fmt"
	"sync"
)

// State is the externally observable lifecycle state of the voice pipeline.
type State string

const (
	StateIdle            State = "idle"
	StateConnecting      State = "connecting"
	StateRecording       State = "recording"
	StateStoppingDelayed State = "stopping_delayed"
	StateStopping        State = "stopping"
	StateError           State = "error"
)

// ErrInvalidTransition is returned when a caller attempts an edge that is
// absent from the explicit transition table.
var ErrInvalidTransition = errors.New("voice: invalid state transition")

// TransitionError describes a rejected state-machine edge.
type TransitionError struct {
	From State
	To   State
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%v: %s -> %s", ErrInvalidTransition, e.From, e.To)
}

func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }

// legalTransitions is deliberately exhaustive. Alternate edges cover the
// interactions that do not fit the happy-path diagram: a hold key can be
// released while Connect is still running, pressing again during the delay
// returns to that earlier phase, and Escape can skip directly to cleanup.
var legalTransitions = map[State]map[State]struct{}{
	StateIdle: {
		StateConnecting: {},
	},
	StateConnecting: {
		StateRecording:       {},
		StateStoppingDelayed: {},
		StateStopping:        {},
		StateError:           {},
	},
	StateRecording: {
		StateStoppingDelayed: {},
		StateStopping:        {},
		StateError:           {},
	},
	StateStoppingDelayed: {
		StateConnecting: {},
		StateRecording:  {},
		StateStopping:   {},
		StateError:      {},
	},
	StateStopping: {
		StateIdle:  {},
		StateError: {},
	},
	StateError: {
		StateIdle: {},
	},
}

// StateMachine serializes access to one explicit voice lifecycle.
type StateMachine struct {
	mu    sync.RWMutex
	state State
}

// NewStateMachine returns a state machine in idle.
func NewStateMachine() *StateMachine {
	return &StateMachine{state: StateIdle}
}

// State returns the current state.
func (m *StateMachine) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// CanTransition reports whether from -> to is present in the transition
// table. It does not inspect or modify a StateMachine instance.
func CanTransition(from, to State) bool {
	_, ok := legalTransitions[from][to]
	return ok
}

// Transition applies a legal edge or leaves the state unchanged and returns
// a TransitionError. Self-transitions are not implicit: callers should ignore
// duplicate input edges instead of hiding them as state changes.
func (m *StateMachine) Transition(to State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !CanTransition(m.state, to) {
		return &TransitionError{From: m.state, To: to}
	}
	m.state = to
	return nil
}
