//go:build linux

package linux

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func newPumpTestRecorder() *ArecordRecorder {
	r := NewRecorder()
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	return r
}

func TestArecordStopReturnsUnreadTail(t *testing.T) {
	r := newPumpTestRecorder()
	r.pumpWG.Add(1)
	go r.pump(io.NopCloser(strings.NewReader("abcd")))

	r.pumpWG.Wait()
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

	r.pumpWG.Add(1)
	go r.pump(pr)

	readDone := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 4))
		readDone <- err
	}()

	// Ensure the reader is blocked on the empty recorder buffer.
	time.Sleep(20 * time.Millisecond)
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		if _, err := r.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	time.Sleep(20 * time.Millisecond)
	// This chunk arrives after Stop was requested and must be retained as tail.
	if _, err := pw.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	_ = pw.CloseWithError(errors.New("done"))

	select {
	case err := <-readDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("blocked Read error = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not wake after Stop")
	}
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish")
	}

	r.mu.Lock()
	tailTaken := r.tailTaken
	r.mu.Unlock()
	if !tailTaken {
		t.Fatal("Stop did not claim tail")
	}
}

func TestArecordReadDrainsNaturalEOF(t *testing.T) {
	r := newPumpTestRecorder()
	r.pumpWG.Add(1)
	go r.pump(io.NopCloser(strings.NewReader("xy")))

	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || n != 2 || string(buf[:n]) != "xy" {
		t.Fatalf("first Read = (%d, %q, %v)", n, buf[:n], err)
	}
	if _, err := r.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("drained Read error = %v, want io.EOF", err)
	}
}

func TestArecordPumpRetainsCapOverflowDuringStop(t *testing.T) {
	r := newPumpTestRecorder()
	r.mu.Lock()
	r.stopping = true
	r.buffer = make([]byte, 1<<20)
	r.mu.Unlock()

	chunk := make([]byte, 4096)
	var src strings.Builder
	src.Write(chunk)
	r.pumpWG.Add(1)
	go r.pump(io.NopCloser(strings.NewReader(src.String())))

	tail, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != (1<<20)+len(chunk) {
		t.Fatalf("tail length = %d, want %d", len(tail), (1<<20)+len(chunk))
	}
}

// Guard against accidental future changes that make WaitGroup Wait concurrent
// with the pump's Done path racy in ordinary use.
func TestArecordCloseIsIdempotent(t *testing.T) {
	r := newPumpTestRecorder()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}
