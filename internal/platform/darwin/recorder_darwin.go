//go:build darwin && cgo

package darwin

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// Recorder captures raw 16 kHz/16-bit/mono PCM through an AudioQueue input.
// Native callbacks copy audio into a bounded queue; the Go lifecycle wrapper
// coordinates concurrent Read, Stop and Close calls.
type Recorder struct {
	mu      sync.Mutex
	readers sync.WaitGroup

	capture   *nativeAudioCapture
	started   bool
	stopping  bool
	closed    bool
	stopDone  chan struct{}
	stopErr   error
	tail      []byte
	tailTaken bool
}

var _ platform.Recorder = (*Recorder)(nil)

func NewRecorder() *Recorder {
	return &Recorder{}
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("darwin recorder: closed")
	}
	if r.started {
		return fmt.Errorf("darwin recorder: already started")
	}
	capture, err := createNativeAudioCapture()
	if err != nil {
		return err
	}
	if err := capture.start(); err != nil {
		_ = capture.close()
		return err
	}
	r.capture = capture
	r.started = true
	r.stopping = false
	r.stopDone = nil
	r.stopErr = nil
	r.tail = nil
	r.tailTaken = false
	return nil
}

func (r *Recorder) Read(destination []byte) (int, error) {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return 0, fmt.Errorf("darwin recorder: not started")
	}
	if r.stopping || r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	capture := r.capture
	r.readers.Add(1)
	r.mu.Unlock()

	count, eof, err := capture.read(destination)
	r.readers.Done()
	if err != nil {
		return count, err
	}
	if eof {
		return count, io.EOF
	}
	return count, nil
}

func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil, nil
	}
	first := r.stopDone == nil
	if first {
		r.stopping = true
		r.stopDone = make(chan struct{})
	}
	done := r.stopDone
	capture := r.capture
	r.mu.Unlock()

	if first {
		tail, err := capture.stop()
		r.readers.Wait()
		r.mu.Lock()
		r.tail = tail
		r.stopErr = err
		close(r.stopDone)
		r.mu.Unlock()
	}
	<-done

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tailTaken {
		return nil, r.stopErr
	}
	tail := r.tail
	r.tail = nil
	r.tailTaken = true
	return tail, r.stopErr
}

func (r *Recorder) Close() error {
	_, stopErr := r.Stop()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return stopErr
	}
	r.closed = true
	capture := r.capture
	r.capture = nil
	r.mu.Unlock()

	var closeErr error
	if capture != nil {
		closeErr = capture.close()
	}
	return errors.Join(stopErr, closeErr)
}
