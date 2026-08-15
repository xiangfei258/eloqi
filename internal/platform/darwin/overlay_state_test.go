package darwin

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestDarwinOverlayCommand(t *testing.T) {
	t.Parallel()
	command, err := darwinOverlayCommand(platform.OverlayRecording, "  中文 status  ")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSuffix(command, "\n"), "\t")
	if len(parts) != 3 || parts[0] != "show" || parts[1] != string(platform.OverlayRecording) {
		t.Fatalf("unexpected command %q", command)
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "中文 status" {
		t.Fatalf("decoded message %q", decoded)
	}
	if _, err := darwinOverlayCommand("bogus", ""); err == nil {
		t.Fatal("unknown state should fail")
	}
}

func TestDarwinOverlayShutdownIsBoundedWhenPipeCloseBlocks(t *testing.T) {
	closeGate := make(chan struct{})
	done := make(chan struct{})
	var release sync.Once
	started := time.Now()
	result := waitDarwinOverlayShutdown(
		done,
		20*time.Millisecond,
		func() error {
			<-closeGate
			return nil
		},
		func() error {
			release.Do(func() {
				close(closeGate)
				close(done)
			})
			return nil
		},
	)
	if !result.timedOut || !result.exited {
		t.Fatalf("shutdown result = %+v, want timed-out then killed exit", result)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown took %s despite a 20ms timeout", elapsed)
	}
}

func TestDarwinOverlayShutdownBoundsWaitAfterKill(t *testing.T) {
	done := make(chan struct{})
	started := time.Now()
	result := waitDarwinOverlayShutdown(done, 20*time.Millisecond, nil, func() error { return nil })
	if !result.timedOut || result.exited {
		t.Fatalf("shutdown result = %+v, want bounded non-exit", result)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown took %s despite bounded waits", elapsed)
	}
}

func TestReplaceEnvironment(t *testing.T) {
	t.Parallel()
	got := replaceEnvironment([]string{"PATH=/bin", "ELOQUI_INTERNAL_NSPANEL_HELPER=old"}, "ELOQUI_INTERNAL_NSPANEL_HELPER", "1")
	want := []string{"PATH=/bin", "ELOQUI_INTERNAL_NSPANEL_HELPER=1"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
