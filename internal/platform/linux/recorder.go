//go:build linux

package linux

import (
	"errors"
	"io"
	"os/exec"
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// ArecordRecorder captures raw mono PCM audio by launching the external
// arecord(1) utility (ALSA). An internal pump goroutine copies arecord's
// stdout into a bounded buffer, which makes Stop safe to call while a Read is
// blocked and lets the recorder honor the interface's "Read returns io.EOF
// after Stop" contract.
type ArecordRecorder struct {
	rate   int
	chans  int
	bits   int
	device string

	// lifecycle is guarded by mu. cmd is written only during Start, before any
	// Read or Stop caller can observe the recorder as started.
	mu       sync.Mutex
	cond     *sync.Cond
	cmd      *exec.Cmd
	started  bool
	stopping bool
	stopDone bool
	closed   bool
	buffer   []byte

	pumpWG sync.WaitGroup
}

var _ platform.Recorder = (*ArecordRecorder)(nil)

// NewRecorder returns a Recorder that uses arecord with the default capture
// parameters (16 kHz / 16-bit / mono).
func NewRecorder() *ArecordRecorder {
	r := &ArecordRecorder{
		rate:  platform.DefaultSampleRate,
		chans: platform.DefaultChannels,
		bits:  platform.DefaultBitDepth,
	}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// WithDevice sets the ALSA capture device (e.g. "default", "hw:0,0").
func (r *ArecordRecorder) WithDevice(device string) *ArecordRecorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.device = device
	return r
}

// Start launches arecord with the configured parameters.
func (r *ArecordRecorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("linux recorder: closed")
	}
	if r.started {
		return errors.New("linux recorder: already started")
	}

	args := []string{
		"-q",        // quiet
		"-t", "raw", // raw PCM (no WAV header)
		"-f", "S16_LE", // 16-bit signed little-endian
		"-c", itoa(r.chans),
		"-r", itoa(r.rate),
	}
	if r.device != "" {
		args = append(args, "-D", r.device)
	}
	args = append(args, "-") // output to stdout

	cmd := exec.Command("arecord", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	r.cmd = cmd
	r.started = true
	r.stopDone = false
	r.pumpWG.Add(1)
	go r.pump(stdout)
	return nil
}

// Read blocks until PCM data is available, the recorder stops, or the stream
// ends. It implements the platform.Recorder lifecycle contract.
func (r *ArecordRecorder) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return 0, errors.New("linux recorder: not started")
	}
	if r.closed {
		return 0, io.EOF
	}

	for len(r.buffer) == 0 && !r.stopDone {
		r.cond.Wait()
	}
	if len(r.buffer) == 0 {
		return 0, io.EOF
	}

	n := len(p)
	if n > len(r.buffer) {
		n = len(r.buffer)
	}
	copy(p, r.buffer[:n])
	r.buffer = r.buffer[n:]
	r.cond.Signal()
	return n, nil
}

// Stop asks arecord to flush and exit, waits for its stdout to drain, and
// returns the PCM samples that have not yet been consumed by Read. Stop may be
// called while another goroutine is blocked in Read; both calls are then
// coordinated through the internal condition variable.
func (r *ArecordRecorder) Stop() ([]byte, error) {
	r.mu.Lock()
	if !r.started {
		defer r.mu.Unlock()
		return nil, errors.New("linux recorder: not started")
	}
	cmd := r.cmd
	r.stopping = true
	r.cond.Broadcast()
	r.mu.Unlock()

	// SIGINT makes arecord flush its buffers and exit cleanly. This unblocks
	// the pump if it is waiting for more microphone data.
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(sigInterrupt)
	}
	r.pumpWG.Wait()
	_ = cmd.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopDone {
		// A prior Stop consumed the remaining buffer. This call is a harmless
		// lifecycle retry.
		return nil, nil
	}
	remaining := r.buffer
	r.buffer = nil
	r.stopDone = true
	r.cond.Broadcast()
	return remaining, nil
}

// Close releases resources. It terminates arecord if still running and is
// idempotent.
func (r *ArecordRecorder) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	cmd := r.cmd
	running := r.started && !r.stopDone
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()

	if running && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if running {
		r.pumpWG.Wait()
		_ = cmd.Wait()
	}

	r.mu.Lock()
	r.buffer = nil
	r.cond.Broadcast()
	r.mu.Unlock()
	return nil
}

// pump owns arecord's stdout and appends every captured chunk to the recorder
// buffer. The buffer is capped so a caller that never reads cannot grow memory
// without bound while recording.
func (r *ArecordRecorder) pump(stdout io.ReadCloser) {
	defer r.pumpWG.Done()
	defer stdout.Close()

	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			r.mu.Lock()
			for len(r.buffer)+len(chunk) > 1<<20 && !r.closed && !r.stopping {
				// Wait for Read to consume space. Stop and Close broadcast on
				// this condition, so shutdown cannot deadlock when no reader is
				// draining the buffer.
				r.cond.Wait()
			}
			if !r.closed && !r.stopping {
				r.buffer = append(r.buffer, chunk...)
			}
			r.cond.Broadcast()
			r.mu.Unlock()
		}
		if err != nil {
			r.mu.Lock()
			r.stopDone = true
			r.cond.Broadcast()
			r.mu.Unlock()
			return
		}
	}
}

// itoa is a local int-to-string helper to avoid a strconv import in this file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
