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
// arecord(1) utility (ALSA). It reads 16 kHz / 16-bit / mono raw PCM from
// arecord's stdout.
type ArecordRecorder struct {
	mu sync.Mutex

	rate   int
	chans  int
	bits   int
	device string

	cmd     *exec.Cmd
	stdout  io.ReadCloser
	started bool
	stopped bool
	closed  bool
}

var _ platform.Recorder = (*ArecordRecorder)(nil)

// NewRecorder returns a Recorder that uses arecord with the default capture
// parameters (16 kHz / 16-bit / mono).
func NewRecorder() *ArecordRecorder {
	return &ArecordRecorder{
		rate:  platform.DefaultSampleRate,
		chans: platform.DefaultChannels,
		bits:  platform.DefaultBitDepth,
	}
}

// WithDevice sets the ALSA capture device (e.g. "default", "hw:0,0").
func (r *ArecordRecorder) WithDevice(device string) *ArecordRecorder {
	r.device = device
	return r
}

// Start launches arecord with the configured parameters.
func (r *ArecordRecorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.stdout = stdout
	r.started = true
	return nil
}

// Read reads the next chunk of captured PCM from arecord's stdout. It blocks
// until data is available or the recorder is stopped.
func (r *ArecordRecorder) Read(p []byte) (int, error) {
	r.mu.Lock()
	stdout := r.stdout
	r.mu.Unlock()
	if stdout == nil {
		return 0, errors.New("linux recorder: not started")
	}
	n, err := stdout.Read(p)
	if err == io.EOF && n == 0 {
		return 0, io.EOF
	}
	return n, err
}

// Stop terminates arecord and returns any buffered samples remaining in the
// stdout pipe.
func (r *ArecordRecorder) Stop() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil, errors.New("linux recorder: not started")
	}
	r.stopped = true

	// Signal arecord to stop so it flushes remaining samples.
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(sigInterrupt)
	}

	// Drain any remaining buffered data from the pipe.
	var tail []byte
	if r.stdout != nil {
		buf := make([]byte, 4096)
		for {
			n, err := r.stdout.Read(buf)
			if n > 0 {
				tail = append(tail, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
	}

	// Wait for the process to exit.
	if r.cmd != nil {
		_ = r.cmd.Wait()
	}
	return tail, nil
}

// Close releases resources. It kills the arecord process if still running and
// is idempotent.
func (r *ArecordRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
	}
	if r.stdout != nil {
		_ = r.stdout.Close()
	}
	return nil
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
