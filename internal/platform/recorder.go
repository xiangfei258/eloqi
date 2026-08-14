package platform

// Default capture parameters used across all Recorder implementations.
// The format is raw mono PCM: 16 kHz sample rate, single channel, 16-bit
// little-endian samples (32000 bytes per second).
const (
	DefaultSampleRate = 16000 // Hz
	DefaultChannels   = 1     // mono
	DefaultBitDepth   = 16    // bits per sample
)

// Recorder captures raw mono PCM audio from a microphone.
//
// The lifecycle is Start, then repeated Read calls until Stop. Stop returns
// any samples that were buffered but not yet delivered to a Read, so the
// caller can forward that tail to the recognizer as the final chunk.
type Recorder interface {
	// Start opens and configures the capture device. It returns an error if
	// the device cannot be opened.
	Start() error

	// Read copies the next chunk of captured samples into p and returns the
	// number of bytes written. Implementations may block until samples are
	// available. It returns io.EOF (possibly alongside a short read) once the
	// recorder has been stopped and its buffer fully drained.
	Read(p []byte) (int, error)

	// Stop ends capture and returns any buffered samples not yet read.
	// Subsequent Read calls drain to io.EOF. Stop is safe to call after a
	// failed Start.
	Stop() ([]byte, error)

	// Close releases the underlying device. It is idempotent.
	Close() error
}
