package platform

// ASRResult is a single recognition result produced by the engine.
type ASRResult struct {
	// Text is the recognized text. It may be empty for a heartbeat result.
	Text string
	// Final reports whether this result is the session's final text.
	Final bool
}

// ResultHandler receives incremental and final recognition results. It may be
// invoked from a background goroutine, so implementations must be safe for
// concurrent use.
type ResultHandler func(ASRResult)

// ASRClient converts speech audio into text.
//
// Streaming and non-streaming backends are unified behind one lifecycle:
// Connect, then stream audio with Send, then Finalize to flush the remaining
// audio and wait for the final text. A handler registered with
// SetResultHandler observes incremental results for engines that emit them.
type ASRClient interface {
	// Connect establishes a session with the recognition backend.
	Connect() error

	// Send streams a chunk of audio to the backend.
	Send(audio []byte) error

	// SetResultHandler registers the handler that receives incremental and
	// final results.
	SetResultHandler(h ResultHandler)

	// Finalize signals the end of audio and blocks until the final text is
	// available, returning it.
	Finalize() (string, error)

	// Close tears down the session. It is idempotent.
	Close() error
}
