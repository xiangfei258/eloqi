//go:build linux

package linux

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newPumpTestRecorder() *ArecordRecorder {
	r := NewRecorder()
	r.mu.Lock()
	r.started = true
	r.pumpDoneCh = make(chan struct{})
	r.process = recorderProcess{
		signal: func(os.Signal) error { return nil },
		kill:   func() error { return nil },
		wait:   func() error { return nil },
	}
	r.mu.Unlock()
	return r
}

func startTestPump(r *ArecordRecorder, stdout io.ReadCloser) {
	r.mu.Lock()
	r.stdout = stdout
	r.mu.Unlock()
	go r.pump(stdout)
}

func waitForPump(t *testing.T, r *ArecordRecorder) {
	t.Helper()
	r.mu.Lock()
	done := r.pumpDoneCh
	r.mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recorder pump did not finish")
	}
}

func waitForStopping(t *testing.T, r *ArecordRecorder) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		stopping := r.stopping
		r.mu.Unlock()
		if stopping {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("recorder did not enter stopping state")
}

func TestArecordStopReturnsUnreadTail(t *testing.T) {
	r := newPumpTestRecorder()
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
	startTestPump(r, io.NopCloser(strings.NewReader("abcd")))
	waitForPump(t, r)

	tail, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if string(tail) != "abcd" {
		t.Fatalf("tail = %q, want %q", tail, "abcd")
	}

	tail, err = r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 0 {
		t.Fatalf("second Stop tail = %q, want empty", tail)
	}
}

func TestArecordStopDoesNotStealTailFromBlockedRead(t *testing.T) {
	r := newPumpTestRecorder()
	pr, pw := io.Pipe()
	startTestPump(r, pr)

	readDone := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 4))
		readDone <- err
	}()

	stopDone := make(chan struct {
		tail []byte
		err  error
	}, 1)
	go func() {
		tail, err := r.Stop()
		stopDone <- struct {
			tail []byte
			err  error
		}{tail: tail, err: err}
	}()
	waitForStopping(t, r)

	// This chunk arrives after Stop was requested and must be retained as tail.
	if _, err := pw.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-readDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("blocked Read error = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not wake after Stop")
	}
	select {
	case result := <-stopDone:
		if result.err != nil {
			t.Fatalf("Stop: %v", result.err)
		}
		if string(result.tail) != "tail" {
			t.Fatalf("tail = %q, want %q", result.tail, "tail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish")
	}
}

func TestArecordReadReportsUnexpectedEOF(t *testing.T) {
	r := newPumpTestRecorder()
	startTestPump(r, io.NopCloser(strings.NewReader("xy")))

	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || n != 2 || string(buf[:n]) != "xy" {
		t.Fatalf("first Read = (%d, %q, %v)", n, buf[:n], err)
	}
	if _, err := r.Read(buf); !errors.Is(err, errRecorderUnexpectedEOF) {
		t.Fatalf("drained Read error = %v, want unexpected EOF", err)
	}

	if _, err := r.Stop(); !errors.Is(err, errRecorderUnexpectedEOF) {
		t.Fatalf("Stop error = %v, want unexpected EOF", err)
	}
}

func TestArecordPumpBoundsTailDuringStop(t *testing.T) {
	r := newPumpTestRecorder()
	r.mu.Lock()
	r.stopping = true
	r.buffer = make([]byte, recorderBufferLimit)
	r.mu.Unlock()

	startTestPump(r, io.NopCloser(strings.NewReader(strings.Repeat("x", 3*4096))))
	tail, err := r.Stop()
	if !errors.Is(err, errRecorderBufferOverflow) {
		t.Fatalf("Stop error = %v, want buffer overflow", err)
	}
	if len(tail) != recorderBufferLimit {
		t.Fatalf("tail length = %d, want bounded length %d", len(tail), recorderBufferLimit)
	}
}

func TestArecordActiveSIGINTExitIsExpected(t *testing.T) {
	r := newPumpTestRecorder()
	pr, pw := io.Pipe()
	startTestPump(r, pr)

	exitErr := errors.New("arecord exit status 1")
	var waitCalls atomic.Int32
	r.mu.Lock()
	r.process.signal = func(os.Signal) error { return pw.Close() }
	r.process.wait = func() error {
		waitCalls.Add(1)
		return exitErr
	}
	r.isExitError = func(err error) bool { return errors.Is(err, exitErr) }
	r.mu.Unlock()

	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop reported expected SIGINT exit: %v", err)
	}
	if got := waitCalls.Load(); got != 1 {
		t.Fatalf("Wait calls = %d, want 1", got)
	}
}

func TestArecordNaturalExitReportsEOFAndWaitError(t *testing.T) {
	r := newPumpTestRecorder()
	exitErr := errors.New("arecord exit status 2")
	var waitCalls atomic.Int32
	r.mu.Lock()
	r.process.signal = func(os.Signal) error { return os.ErrProcessDone }
	r.process.wait = func() error {
		waitCalls.Add(1)
		return exitErr
	}
	r.isExitError = func(err error) bool { return errors.Is(err, exitErr) }
	r.mu.Unlock()
	startTestPump(r, io.NopCloser(strings.NewReader("")))
	waitForPump(t, r)

	_, err := r.Stop()
	if !errors.Is(err, errRecorderUnexpectedEOF) {
		t.Fatalf("Stop error = %v, want unexpected EOF", err)
	}
	if !errors.Is(err, exitErr) {
		t.Fatalf("Stop error = %v, want process exit error", err)
	}
	if got := waitCalls.Load(); got != 1 {
		t.Fatalf("Wait calls = %d, want 1", got)
	}
}

func TestArecordPumpReadErrorPropagates(t *testing.T) {
	want := errors.New("capture pipe failed")
	r := newPumpTestRecorder()
	startTestPump(r, &failingReadCloser{err: want})
	waitForPump(t, r)

	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, want) {
		t.Fatalf("Read error = %v, want %v", err, want)
	}
	if _, err := r.Stop(); !errors.Is(err, want) {
		t.Fatalf("Stop error = %v, want %v", err, want)
	}
}

func TestArecordStopPropagatesProcessErrors(t *testing.T) {
	signalErr := errors.New("signal failed")
	killErr := errors.New("kill failed")
	waitErr := errors.New("wait failed")
	tests := []struct {
		name      string
		signalErr error
		killErr   error
		waitErr   error
		want      []error
	}{
		{name: "signal", signalErr: signalErr, want: []error{signalErr}},
		{name: "signal and kill", signalErr: signalErr, killErr: killErr, want: []error{signalErr, killErr}},
		{name: "wait", waitErr: waitErr, want: []error{waitErr}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newPumpTestRecorder()
			r.mu.Lock()
			r.stopping = true
			r.process.signal = func(os.Signal) error { return tt.signalErr }
			r.process.kill = func() error { return tt.killErr }
			r.process.wait = func() error { return tt.waitErr }
			r.isExitError = func(error) bool { return false }
			r.mu.Unlock()
			startTestPump(r, io.NopCloser(strings.NewReader("")))
			waitForPump(t, r)

			_, err := r.Stop()
			for _, want := range tt.want {
				if !errors.Is(err, want) {
					t.Errorf("Stop error = %v, want wrapped %v", err, want)
				}
			}
		})
	}
}

func TestArecordStopEscalatesToKillAndWaitsOnce(t *testing.T) {
	r := newPumpTestRecorder()
	stdout := newBlockingReadCloser()
	startTestPump(r, stdout)

	exitErr := errors.New("killed")
	var mu sync.Mutex
	var calls []string
	r.mu.Lock()
	r.stopTimeout = 15 * time.Millisecond
	r.killTimeout = time.Second
	r.process.signal = func(os.Signal) error {
		mu.Lock()
		calls = append(calls, "signal")
		mu.Unlock()
		return nil
	}
	r.process.kill = func() error {
		mu.Lock()
		calls = append(calls, "kill")
		mu.Unlock()
		return nil
	}
	r.process.wait = func() error {
		mu.Lock()
		calls = append(calls, "wait")
		mu.Unlock()
		return exitErr
	}
	r.isExitError = func(err error) bool { return errors.Is(err, exitErr) }
	r.mu.Unlock()

	started := time.Now()
	_, err := r.Stop()
	if !errors.Is(err, errRecorderStopTimeout) {
		t.Fatalf("Stop error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Stop took %s despite timeout", elapsed)
	}

	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	want := []string{"signal", "kill", "wait"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("shutdown calls = %v, want %v", got, want)
	}
}

func TestArecordStopReturnsAfterKillTimeout(t *testing.T) {
	r := newPumpTestRecorder()
	stdout := newStubbornReadCloser()
	startTestPump(r, stdout)

	r.mu.Lock()
	r.stopTimeout = 10 * time.Millisecond
	r.killTimeout = 10 * time.Millisecond
	r.mu.Unlock()

	started := time.Now()
	_, err := r.Stop()
	if !errors.Is(err, errRecorderStopTimeout) {
		t.Fatalf("Stop error = %v, want graceful timeout", err)
	}
	if !errors.Is(err, errRecorderKillTimeout) {
		t.Fatalf("Stop error = %v, want kill timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Stop took %s despite hard timeout", elapsed)
	}

	// Release the synthetic reader so the pump and its reaper goroutine do not
	// outlive the test. A real os.File is normally released by Kill or Close.
	stdout.release()
	waitForPump(t, r)
}

func TestArecordConcurrentStopSharesShutdown(t *testing.T) {
	r := newPumpTestRecorder()
	pr, pw := io.Pipe()
	startTestPump(r, pr)
	if _, err := pw.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}

	var signalCalls atomic.Int32
	var waitCalls atomic.Int32
	var closeOnce sync.Once
	r.mu.Lock()
	r.process.signal = func(os.Signal) error {
		signalCalls.Add(1)
		closeOnce.Do(func() { _ = pw.Close() })
		return nil
	}
	r.process.wait = func() error {
		waitCalls.Add(1)
		return nil
	}
	r.mu.Unlock()

	const callers = 8
	results := make(chan struct {
		tail []byte
		err  error
	}, callers)
	for range callers {
		go func() {
			tail, err := r.Stop()
			results <- struct {
				tail []byte
				err  error
			}{tail: tail, err: err}
		}()
	}

	tailOwners := 0
	for range callers {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("concurrent Stop: %v", result.err)
			}
			if len(result.tail) > 0 {
				tailOwners++
				if string(result.tail) != "tail" {
					t.Fatalf("tail = %q, want %q", result.tail, "tail")
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Stop did not finish")
		}
	}
	if tailOwners != 1 {
		t.Fatalf("tail owners = %d, want 1", tailOwners)
	}
	if got := signalCalls.Load(); got != 1 {
		t.Fatalf("Signal calls = %d, want 1", got)
	}
	if got := waitCalls.Load(); got != 1 {
		t.Fatalf("Wait calls = %d, want 1", got)
	}
}

func TestArecordCloseIsIdempotent(t *testing.T) {
	r := NewRecorder()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestArecordStopBeforeStartIsNoOp(t *testing.T) {
	r := NewRecorder()

	tail, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if tail != nil {
		t.Fatalf("Stop before Start tail = %q, want nil", tail)
	}
}

func TestArecordStopAfterFailedStartIsNoOp(t *testing.T) {
	// Make executable lookup fail deterministically on every platform where
	// this source-level test is run, independent of whether arecord is installed.
	t.Setenv("PATH", t.TempDir())
	r := NewRecorder()
	if err := r.Start(); err == nil {
		t.Fatal("Start succeeded with PATH containing no arecord executable")
	}

	tail, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop after failed Start: %v", err)
	}
	if tail != nil {
		t.Fatalf("Stop after failed Start tail = %q, want nil", tail)
	}
}

type failingReadCloser struct {
	err error
}

func (r *failingReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r *failingReadCloser) Close() error             { return nil }

type blockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{closed: make(chan struct{})}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.EOF
}

func (r *blockingReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

type stubbornReadCloser struct {
	released chan struct{}
	once     sync.Once
}

func newStubbornReadCloser() *stubbornReadCloser {
	return &stubbornReadCloser{released: make(chan struct{})}
}

func (r *stubbornReadCloser) Read([]byte) (int, error) {
	<-r.released
	return 0, io.EOF
}

func (r *stubbornReadCloser) Close() error { return nil }

func (r *stubbornReadCloser) release() {
	r.once.Do(func() { close(r.released) })
}
