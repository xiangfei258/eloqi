package platform

// OverlayState identifies the user-visible phase rendered by the status
// overlay. The values are stable protocol tokens so an out-of-process native
// helper can consume them without importing Go types.
type OverlayState string

const (
	OverlayConnecting OverlayState = "connecting"
	OverlayRecording  OverlayState = "recording"
	OverlayStopping   OverlayState = "stopping"
	OverlayWaiting    OverlayState = "waiting"
	OverlayError      OverlayState = "error"
)

// Overlay presents a small, non-activating status capsule. Implementations
// must not steal keyboard focus from the application receiving dictated text.
type Overlay interface {
	// Show displays state and an optional short message.
	Show(state OverlayState, message string) error

	// Hide removes the capsule without destroying its native resources.
	Hide() error

	// Close releases native resources. It is idempotent.
	Close() error
}
