//go:build linux

package linux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	recorderBufferLimit = 1 << 20
	recorderStopTimeout = 2 * time.Second
	recorderKillTimeout = time.Second
)

var (
	errRecorderBufferOverflow = errors.New("linux recorder: audio buffer limit exceeded")
	errRecorderUnexpectedEOF  = errors.New("linux recorder: arecord stdout closed before Stop")
	errRecorderStopTimeout    = errors.New("linux recorder: arecord did not stop before timeout")
	errRecorderKillTimeout    = errors.New("linux recorder: arecord did not exit after Kill")
)

type recorderProcess struct {
	signal func(os.Signal) error
	kill   func() error
	wait   func() error
}

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

	// lifecycle is guarded by mu. Process controls are installed by Start
	// before any Read or Stop caller can observe the recorder as started.
	mu              sync.Mutex
	cond            *sync.Cond
	process         recorderProcess
	stdout          io.ReadCloser
	started         bool
	stopping        bool
	pumpDone        bool
	pumpDoneCh      chan struct{}
	pumpErr         error
	stopDone        chan struct{}
	stopErr         error
	shutdownByClose bool
	tailTaken       bool
	closed          bool
	buffer          []byte

	stopTimeout time.Duration
	killTimeout time.Duration
	isExitError func(error) bool
}

var _ platform.Recorder = (*ArecordRecorder)(nil)

// NewRecorder returns a Recorder that uses arecord with the default capture
// parameters (16 kHz / 16-bit / mono).
func NewRecorder() *ArecordRecorder {
	r := &ArecordRecorder{
		rate:        platform.DefaultSampleRate,
		chans:       platform.DefaultChannels,
		bits:        platform.DefaultBitDepth,
		stopTimeout: recorderStopTimeout,
		killTimeout: recorderKillTimeout,
		isExitError: isProcessExitError,
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
		_ = stdout.Close()
		return err
	}

	r.process = recorderProcess{
		signal: cmd.Process.Signal,
		kill:   cmd.Process.Kill,
		wait:   cmd.Wait,
	}
	r.stdout = stdout
	r.started = true
	r.stopping = false
	r.pumpDone = false
	r.pumpDoneCh = make(chan struct{})
	r.pumpErr = nil
	r.stopDone = nil
	r.stopErr = nil
	r.shutdownByClose = false
	r.tailTaken = false
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

	for len(r.buffer) == 0 && !r.stopping && !r.pumpDone && !r.closed {
		r.cond.Wait()
	}
	if r.stopping {
		return 0, io.EOF
	}
	if len(r.buffer) == 0 {
		if r.pumpErr != nil {
			return 0, r.pumpErr
		}
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
		r.mu.Unlock()
		// The platform contract permits Stop after Start fails. There is no
		// process or buffered audio to finish in that state, so this is a no-op.
		return nil, nil
	}
	pumpWasRunning := !r.pumpDone
	first, done := r.beginShutdownLocked(false)
	r.mu.Unlock()

	if first {
		r.finishShutdown(false, pumpWasRunning)
	}
	<-done

	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.stopErr
	if r.tailTaken {
		// A prior Stop claimed the remaining buffer. This call is a harmless
		// lifecycle retry.
		return nil, err
	}
	remaining := r.buffer
	r.buffer = nil
	r.tailTaken = true
	r.cond.Broadcast()
	return remaining, err
}

// Close releases resources. It terminates arecord if still running and is
// idempotent.
func (r *ArecordRecorder) Close() error {
	r.mu.Lock()
	r.closed = true
	r.cond.Broadcast()
	if !r.started {
		r.buffer = nil
		r.mu.Unlock()
		return nil
	}
	pumpWasRunning := !r.pumpDone
	first, done := r.beginShutdownLocked(true)
	r.mu.Unlock()

	if first {
		r.finishShutdown(true, pumpWasRunning)
	}
	<-done

	r.mu.Lock()
	err := error(nil)
	if r.shutdownByClose {
		err = r.stopErr
	}
	r.buffer = nil
	r.cond.Broadcast()
	r.mu.Unlock()
	return err
}

// beginShutdownLocked makes concurrent Stop and Close calls share one
// process-termination and Wait sequence. The caller must hold r.mu.
func (r *ArecordRecorder) beginShutdownLocked(byClose bool) (bool, <-chan struct{}) {
	if r.stopDone != nil {
		return false, r.stopDone
	}
	r.stopping = true
	r.shutdownByClose = byClose
	r.stopDone = make(chan struct{})
	r.cond.Broadcast()
	return true, r.stopDone
}

// finishShutdown performs the blocking half of shutdown exactly once. A
// graceful Stop sends SIGINT, then escalates to Kill after a bounded wait.
// Close skips directly to Kill. Wait is called only after the stdout pump has
// exited, as required by exec.Cmd.StdoutPipe.
func (r *ArecordRecorder) finishShutdown(force bool, pumpWasRunning bool) {
	r.mu.Lock()
	process := r.process
	stdout := r.stdout
	pumpDone := r.pumpDoneCh
	stopTimeout := r.stopTimeout
	killTimeout := r.killTimeout
	isExitError := r.isExitError
	r.mu.Unlock()

	if isExitError == nil {
		isExitError = isProcessExitError
	}
	if stopTimeout <= 0 {
		stopTimeout = recorderStopTimeout
	}
	if killTimeout <= 0 {
		killTimeout = recorderKillTimeout
	}
	if pumpDone == nil {
		pumpDone = make(chan struct{})
	}

	waitDone := make(chan error, 1)
	go func() {
		<-pumpDone
		if process.wait == nil {
			waitDone <- nil
			return
		}
		waitDone <- process.wait()
	}()

	var errs []error
	signalSucceeded := false
	killSucceeded := false
	forced := force
	timedOut := false

	kill := func() {
		if process.kill != nil {
			err := process.kill()
			if err == nil {
				killSucceeded = true
			} else if !errors.Is(err, os.ErrProcessDone) {
				errs = append(errs, fmt.Errorf("linux recorder: kill arecord: %w", err))
			}
		}
		// Closing the pipe is a final unblocking safeguard. Keep it async so a
		// broken ReadCloser cannot defeat Stop's timeout guarantee.
		if stdout != nil {
			go func() { _ = stdout.Close() }()
		}
	}

	if force {
		kill()
	} else if process.signal != nil {
		if err := process.signal(sigInterrupt); err == nil {
			signalSucceeded = true
		} else if !errors.Is(err, os.ErrProcessDone) {
			errs = append(errs, fmt.Errorf("linux recorder: signal arecord: %w", err))
			forced = true
			kill()
		}
	}

	initialTimeout := stopTimeout
	if forced {
		initialTimeout = killTimeout
	}
	waitErr, finished := waitForRecorder(waitDone, initialTimeout)
	if !finished && !forced {
		timedOut = true
		errs = append(errs, fmt.Errorf("%w (%s)", errRecorderStopTimeout, stopTimeout))
		kill()
		waitErr, finished = waitForRecorder(waitDone, killTimeout)
	}
	if !finished {
		errs = append(errs, fmt.Errorf("%w (%s)", errRecorderKillTimeout, killTimeout))
	}

	if finished && waitErr != nil {
		// alsa-utils handles SIGINT itself and exits with status 1. Suppress
		// that expected ExitError only when Stop observed a live pump and sent
		// SIGINT successfully. If the pump had already ended, a non-zero exit
		// remains a real capture failure.
		expectedSIGINTExit := !force && !timedOut && signalSucceeded && pumpWasRunning && isExitError(waitErr)
		expectedKillExit := killSucceeded && isExitError(waitErr)
		if !expectedSIGINTExit && !expectedKillExit {
			errs = append(errs, fmt.Errorf("linux recorder: wait for arecord: %w", waitErr))
		}
	}

	r.mu.Lock()
	if r.pumpErr != nil {
		errs = append(errs, r.pumpErr)
	}
	r.stopErr = errors.Join(errs...)
	close(r.stopDone)
	r.mu.Unlock()
}

func waitForRecorder(done <-chan error, timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

func isProcessExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

// pump owns arecord's stdout and appends every captured chunk to the recorder
// buffer. The buffer is capped so a caller that never reads cannot grow memory
// without bound while recording.
func (r *ArecordRecorder) pump(stdout io.ReadCloser) {
	defer func() { _ = stdout.Close() }()
	defer func() {
		r.mu.Lock()
		r.pumpDone = true
		if r.pumpDoneCh != nil {
			close(r.pumpDoneCh)
		}
		r.cond.Broadcast()
		r.mu.Unlock()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			r.mu.Lock()
			for len(r.buffer)+len(chunk) > recorderBufferLimit && !r.closed && !r.stopping {
				// Wait for Read to consume space. Stop and Close broadcast on
				// this condition, so shutdown cannot deadlock when no reader is
				// draining the buffer.
				r.cond.Wait()
			}
			if !r.closed {
				available := recorderBufferLimit - len(r.buffer)
				if available < 0 {
					available = 0
				}
				if len(chunk) > available {
					r.recordPumpErrorLocked(errRecorderBufferOverflow)
					chunk = chunk[:available]
				}
				r.buffer = append(r.buffer, chunk...)
			}
			r.cond.Broadcast()
			r.mu.Unlock()
		}
		if err != nil {
			r.mu.Lock()
			if errors.Is(err, io.EOF) {
				if !r.stopping && !r.closed {
					r.recordPumpErrorLocked(errRecorderUnexpectedEOF)
				}
			} else if !r.closed {
				r.recordPumpErrorLocked(fmt.Errorf("linux recorder: read arecord stdout: %w", err))
			}
			r.mu.Unlock()
			return
		}
	}
}

// recordPumpErrorLocked retains distinct terminal pump failures without
// allowing repeated overflow chunks to grow the error value without bound.
// The caller must hold r.mu.
func (r *ArecordRecorder) recordPumpErrorLocked(err error) {
	if err == nil || errors.Is(r.pumpErr, err) {
		return
	}
	r.pumpErr = errors.Join(r.pumpErr, err)
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
