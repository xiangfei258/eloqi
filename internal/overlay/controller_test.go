package overlay

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/mock"
	"github.com/xiangchang24/eloqi/internal/voice"
)

func TestControllerMapsVoiceLifecycle(t *testing.T) {
	backend := &mock.Overlay{}
	c, err := New(Config{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	states := []voice.State{
		voice.StateConnecting,
		voice.StateRecording,
		voice.StateStoppingDelayed,
		voice.StateStopping,
		voice.StateError,
	}
	want := []platform.OverlayState{
		platform.OverlayConnecting,
		platform.OverlayRecording,
		platform.OverlayStopping,
		platform.OverlayWaiting,
		platform.OverlayError,
	}
	for index, state := range states {
		c.StateChanged(state)
		waitFor(t, func() bool { return len(backend.Calls()) == index+1 })
	}
	c.StateChanged(voice.StateIdle)
	waitFor(t, func() bool { return backend.HideCount() == 1 })

	calls := backend.Calls()
	for index := range want {
		if calls[index].State != want[index] {
			t.Fatalf("call %d state = %s, want %s", index, calls[index].State, want[index])
		}
	}
}

func TestControllerDeduplicatesAndNormalizesError(t *testing.T) {
	backend := &mock.Overlay{}
	c, err := New(Config{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	c.StateChanged(voice.StateRecording)
	c.StateChanged(voice.StateRecording)
	waitFor(t, func() bool { return len(backend.Calls()) == 1 })
	c.ShowError(errors.New("microphone\n  unavailable"))
	waitFor(t, func() bool { return len(backend.Calls()) == 2 })
	call := backend.Calls()[1]
	if call.State != platform.OverlayError || call.Message != "microphone unavailable" {
		t.Fatalf("error call = %+v", call)
	}
}

func TestControllerReportsBackendError(t *testing.T) {
	want := errors.New("display gone")
	backend := &mock.Overlay{ShowErr: want}
	var (
		mu       sync.Mutex
		reported error
	)
	c, err := New(Config{
		Backend: backend,
		OnError: func(err error) {
			mu.Lock()
			reported = err
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	c.StateChanged(voice.StateRecording)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return errors.Is(reported, want)
	})
}

func TestControllerCloseIsIdempotent(t *testing.T) {
	backend := &mock.Overlay{}
	c, err := New(Config{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if backend.CloseCount() != 1 {
		t.Fatalf("backend close count = %d, want 1", backend.CloseCount())
	}
	c.StateChanged(voice.StateRecording)
	if len(backend.Calls()) != 0 {
		t.Fatal("state queued after Close")
	}
}

type blockingBackend struct {
	showEntered  chan struct{}
	showRelease  chan struct{}
	closeEntered chan struct{}
	closeRelease chan struct{}
	showOnce     sync.Once
	closeOnce    sync.Once
}

func (b *blockingBackend) Show(platform.OverlayState, string) error {
	b.showOnce.Do(func() { close(b.showEntered) })
	<-b.showRelease
	return nil
}

func (b *blockingBackend) Hide() error { return nil }

func (b *blockingBackend) Close() error {
	b.closeOnce.Do(func() { close(b.closeEntered) })
	<-b.closeRelease
	return nil
}

func TestControllerCloseIsBoundedWhenShowStalls(t *testing.T) {
	backend := &blockingBackend{
		showEntered:  make(chan struct{}),
		showRelease:  make(chan struct{}),
		closeEntered: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	c, err := New(Config{Backend: backend, CallTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.StateChanged(voice.StateRecording)
	select {
	case <-backend.showEntered:
	case <-time.After(time.Second):
		t.Fatal("backend Show did not start")
	}
	started := time.Now()
	err = c.Close()
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("Close took %s behind stalled Show", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Close error = %v, want timeout", err)
	}
	close(backend.showRelease)
	close(backend.closeRelease)
}

func TestControllerCloseBackendCallIsBounded(t *testing.T) {
	backend := &blockingBackend{
		showEntered:  make(chan struct{}),
		showRelease:  make(chan struct{}),
		closeEntered: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	close(backend.showRelease)
	c, err := New(Config{Backend: backend, CallTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- c.Close() }()
	select {
	case <-backend.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("backend Close did not start")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("Close error = %v, want timeout", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Controller Close blocked in backend Close")
	}
	close(backend.closeRelease)
}

func TestControllerRejectsMissingBackend(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("New error = %v", err)
	}
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
