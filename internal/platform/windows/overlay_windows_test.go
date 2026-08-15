//go:build windows

package windows

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowsOverlayCloseFallsBackToThreadQuit(t *testing.T) {
	done := make(chan struct{})
	var threadQuitCalls atomic.Int32
	overlay := &Overlay{
		commands:         make(chan overlayCommand, 1),
		done:             done,
		operationTimeout: 50 * time.Millisecond,
		postWindow: func(uintptr, uint32) error {
			return errors.New("PostMessage failed")
		},
		postThread: func(_ uint32, message uint32) error {
			if message != wmQuit {
				t.Fatalf("thread message = %#x, want WM_QUIT", message)
			}
			if threadQuitCalls.Add(1) == 1 {
				close(done)
			}
			return nil
		},
	}
	overlay.hwnd.Store(1)
	overlay.threadID.Store(2)

	returned := make(chan error, 1)
	go func() { returned <- overlay.Close() }()
	select {
	case err := <-returned:
		if err == nil || !strings.Contains(err.Error(), "PostMessage failed") {
			t.Fatalf("Close error = %v, want original wake failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not use the thread-level WM_QUIT fallback")
	}
	if threadQuitCalls.Load() == 0 {
		t.Fatal("WM_QUIT fallback was not attempted")
	}
}

func TestWindowsOverlayCloseTimeoutIsBounded(t *testing.T) {
	overlay := &Overlay{
		commands:         make(chan overlayCommand, 1),
		done:             make(chan struct{}),
		operationTimeout: 20 * time.Millisecond,
		postWindow: func(uintptr, uint32) error {
			return errors.New("window queue unavailable")
		},
		postThread: func(uint32, uint32) error {
			return errors.New("thread queue unavailable")
		},
	}
	overlay.hwnd.Store(1)
	overlay.threadID.Store(2)

	started := time.Now()
	err := overlay.Close()
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("Close error = %v, want bounded exit timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %s despite a 20ms timeout", elapsed)
	}
}
