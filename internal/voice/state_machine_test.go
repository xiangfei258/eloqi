package voice

import (
	"errors"
	"testing"
)

func TestStateMachineLegalTransitions(t *testing.T) {
	tests := []struct {
		name string
		path []State
	}{
		{
			name: "normal session",
			path: []State{StateConnecting, StateRecording, StateStoppingDelayed, StateStopping, StateIdle},
		},
		{
			name: "cancel delayed stop after connection",
			path: []State{StateConnecting, StateRecording, StateStoppingDelayed, StateRecording, StateStopping, StateIdle},
		},
		{
			name: "hold released while connecting then resumed",
			path: []State{StateConnecting, StateStoppingDelayed, StateConnecting, StateRecording, StateStopping, StateIdle},
		},
		{
			name: "escape while connecting",
			path: []State{StateConnecting, StateStopping, StateIdle},
		},
		{
			name: "error and reset",
			path: []State{StateConnecting, StateError, StateIdle},
		},
		{
			name: "finalization error and reset",
			path: []State{StateConnecting, StateRecording, StateStopping, StateError, StateIdle},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewStateMachine()
			for _, next := range tt.path {
				if err := m.Transition(next); err != nil {
					t.Fatalf("Transition(%s): %v", next, err)
				}
				if got := m.State(); got != next {
					t.Fatalf("state = %s, want %s", got, next)
				}
			}
		})
	}
}

func TestStateMachineEveryDeclaredEdge(t *testing.T) {
	for from, destinations := range legalTransitions {
		for to := range destinations {
			m := &StateMachine{state: from}
			if err := m.Transition(to); err != nil {
				t.Errorf("declared edge %s -> %s rejected: %v", from, to, err)
			}
		}
	}
}

func TestStateMachineIllegalTransitions(t *testing.T) {
	states := []State{
		StateIdle,
		StateConnecting,
		StateRecording,
		StateStoppingDelayed,
		StateStopping,
		StateError,
	}
	for _, from := range states {
		for _, to := range states {
			if CanTransition(from, to) {
				continue
			}
			name := string(from) + "_to_" + string(to)
			t.Run(name, func(t *testing.T) {
				m := &StateMachine{state: from}
				err := m.Transition(to)
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("error = %v, want ErrInvalidTransition", err)
				}
				if got := m.State(); got != from {
					t.Fatalf("rejected transition changed state to %s", got)
				}
				var transitionErr *TransitionError
				if !errors.As(err, &transitionErr) {
					t.Fatalf("error type = %T, want *TransitionError", err)
				}
				if transitionErr.From != from || transitionErr.To != to {
					t.Fatalf("transition error = %+v", transitionErr)
				}
			})
		}
	}

	t.Run("unknown source", func(t *testing.T) {
		from := State("unknown")
		m := &StateMachine{state: from}
		err := m.Transition(StateIdle)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
		if got := m.State(); got != from {
			t.Fatalf("rejected transition changed state to %s", got)
		}
	})
}
