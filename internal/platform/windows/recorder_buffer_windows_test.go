//go:build windows

package windows

import (
	"testing"
	"time"
)

func TestRecorderFullBufferWakesWhenStopping(t *testing.T) {
	recorder := NewRecorder()
	recorder.buffer = make([]byte, windowsRecorderBufferLimit)
	entered := make(chan struct{})
	returned := make(chan struct{})
	go func() {
		recorder.mu.Lock()
		close(entered)
		recorder.appendCapturedLocked([]byte{1, 2})
		recorder.mu.Unlock()
		close(returned)
	}()
	<-entered

	// appendCapturedLocked releases mu in cond.Wait. Stop's state transition
	// and broadcast must always make that wait finite, even at exactly 1 MiB.
	recorder.mu.Lock()
	recorder.stopping = true
	recorder.cond.Broadcast()
	recorder.mu.Unlock()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("full recorder buffer remained blocked after Stop broadcast")
	}
	if recorder.pumpErr == nil {
		t.Fatal("truncated final chunk should record a buffer overflow")
	}
}
