package mock

import (
	"errors"
	"io"
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

var _ platform.Recorder = (*Recorder)(nil)

// Recorder is an in-memory platform.Recorder for tests. It streams a fixed
// payload in fixed-size chunks and records its lifecycle for assertions.
//
// Read is non-blocking: it returns (0, nil) while the recorder is started but
// has no data ready, and io.EOF once stopped and fully drained.
type Recorder struct {
	mu sync.Mutex

	// Data is the full PCM payload delivered by Read.
	Data []byte
	// ChunkSize is the maximum number of bytes returned per Read. A value
	// <= 0 means the entire remaining payload is returned at once.
	ChunkSize int

	// StartErr, ReadErr and StopErr, when non-nil, are returned by the
	// corresponding method instead of its normal behavior.
	StartErr error
	ReadErr  error
	StopErr  error

	started bool
	stopped bool
	closed  bool
	offset  int
}

// Start implements platform.Recorder.
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return errors.New("mock recorder: already started")
	}
	if r.StartErr != nil {
		return r.StartErr
	}
	r.started = true
	return nil
}

// Read implements platform.Recorder.
func (r *Recorder) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return 0, errors.New("mock recorder: read before start")
	}
	if r.offset >= len(r.Data) {
		if r.stopped {
			if r.ReadErr != nil {
				return 0, r.ReadErr
			}
			return 0, io.EOF
		}
		return 0, nil
	}
	n := r.chunk()
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.Data)-r.offset {
		n = len(r.Data) - r.offset
	}
	copy(p, r.Data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

// Stop implements platform.Recorder.
func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.StopErr != nil {
		return nil, r.StopErr
	}
	r.stopped = true
	remaining := append([]byte(nil), r.Data[r.offset:]...)
	r.offset = len(r.Data)
	return remaining, nil
}

// Close implements platform.Recorder.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// Started reports whether Start was called successfully.
func (r *Recorder) Started() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

// Stopped reports whether Stop was called successfully.
func (r *Recorder) Stopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

// Closed reports whether Close was called.
func (r *Recorder) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *Recorder) chunk() int {
	if r.ChunkSize <= 0 {
		return len(r.Data)
	}
	return r.ChunkSize
}
