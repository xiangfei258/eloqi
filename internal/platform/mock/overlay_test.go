package mock

import (
	"errors"
	"sync"
	"testing"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestOverlayLifecycle(t *testing.T) {
	o := &Overlay{}
	if err := o.Show(platform.OverlayRecording, "listening"); err != nil {
		t.Fatal(err)
	}
	if !o.Visible() {
		t.Fatal("overlay is not visible after Show")
	}
	if err := o.Hide(); err != nil {
		t.Fatal(err)
	}
	if o.Visible() || o.HideCount() != 1 {
		t.Fatalf("visible=%v hides=%d", o.Visible(), o.HideCount())
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if o.CloseCount() != 1 {
		t.Fatalf("close count = %d, want 1", o.CloseCount())
	}
	if err := o.Show(platform.OverlayError, "late"); err == nil {
		t.Fatal("Show after Close succeeded")
	}
	calls := o.Calls()
	if len(calls) != 1 || calls[0].State != platform.OverlayRecording || calls[0].Message != "listening" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestOverlayInjectedErrorsDoNotMutate(t *testing.T) {
	o := &Overlay{ShowErr: errors.New("show"), HideErr: errors.New("hide"), CloseErr: errors.New("close")}
	if err := o.Show(platform.OverlayConnecting, ""); !errors.Is(err, o.ShowErr) {
		t.Fatalf("Show error = %v", err)
	}
	if err := o.Hide(); !errors.Is(err, o.HideErr) {
		t.Fatalf("Hide error = %v", err)
	}
	if err := o.Close(); !errors.Is(err, o.CloseErr) {
		t.Fatalf("Close error = %v", err)
	}
	if len(o.Calls()) != 0 || o.HideCount() != 0 || o.CloseCount() != 0 {
		t.Fatal("failed operations mutated counters")
	}
}

func TestOverlayConcurrentShow(t *testing.T) {
	o := &Overlay{}
	const workers = 32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := o.Show(platform.OverlayWaiting, "result"); err != nil {
				t.Errorf("Show: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := len(o.Calls()); got != workers {
		t.Fatalf("calls = %d, want %d", got, workers)
	}
}
