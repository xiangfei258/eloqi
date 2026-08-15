// Package instance prevents multiple Eloqui daemons in one user session from
// recording, transcribing, and pasting the same hotkey gesture twice.
package instance

import "errors"

// ErrAlreadyRunning is returned when another Eloqui daemon owns the user-level
// instance lock.
var ErrAlreadyRunning = errors.New("another Eloqui instance is already running")
