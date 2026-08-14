package voice

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/mock"
)

// testHarness wires a Voice with mock dependencies for testing.
type testHarness struct {
	voice      *Voice
	hotkey     *mock.Hotkey
	recorder   *mock.Recorder
	asr        *mock.ASRClient
	clipboard  *mock.Clipboard
	autotype   *mock.Autotype
	resultCh   chan struct{}
	resultText string
	resultErr  error
}

func newHarness(t *testing.T, mode string, autoType bool) *testHarness {
	t.Helper()
	hk := mock.NewHotkey()
	cb := &mock.Clipboard{}
	at := &mock.Autotype{}

	rec := &mock.Recorder{
		Data:      make([]byte, 200),
		ChunkSize: 50,
	}
	asr := &mock.ASRClient{
		FinalText: "hello world",
	}

	h := &testHarness{
		hotkey:    hk,
		recorder:  rec,
		asr:       asr,
		clipboard: cb,
		autotype:  at,
		resultCh:  make(chan struct{}, 1),
	}

	v := New(Config{
		Hotkey:      hk,
		NewRecorder: func() platform.Recorder { return rec },
		NewASR:      func() platform.ASRClient { return asr },
		Clipboard:   cb,
		Autotype:    at,
		Key:         platform.Key{Mods: platform.ModCtrl, Code: "F1"},
		Mode:        mode,
		AutoType:    autoType,
	})
	v.OnResult = func(text string, err error) {
		h.resultText = text
		h.resultErr = err
		select {
		case h.resultCh <- struct{}{}:
		default:
		}
	}
	h.voice = v

	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *testHarness) waitResult(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-h.resultCh:
	case <-time.After(d):
		t.Fatal("timed out waiting for result")
	}
}

func (h *testHarness) press() {
	h.hotkey.Emit(platform.KeyEvent{Key: platform.Key{Mods: platform.ModCtrl, Code: "F1"}, Pressed: true})
}

func (h *testHarness) release() {
	h.hotkey.Emit(platform.KeyEvent{Key: platform.Key{Mods: platform.ModCtrl, Code: "F1"}, Pressed: false})
}

func TestHoldModeClipboard(t *testing.T) {
	h := newHarness(t, "hold", false)

	h.press()
	time.Sleep(60 * time.Millisecond) // allow streaming to read some data
	h.release()
	h.waitResult(t, 2*time.Second)

	if h.resultText != "hello world" {
		t.Fatalf("result = %q, want %q", h.resultText, "hello world")
	}
	if h.resultErr != nil {
		t.Fatalf("result err = %v", h.resultErr)
	}
	if h.clipboard.WriteCount() != 1 {
		t.Fatalf("clipboard writes = %d, want 1", h.clipboard.WriteCount())
	}
	if h.clipboard.Text != "hello world" {
		t.Fatalf("clipboard text = %q, want %q", h.clipboard.Text, "hello world")
	}
	if len(h.autotype.Typed()) != 0 {
		t.Fatalf("autotype should not be called, got %v", h.autotype.Typed())
	}
	if !h.recorder.Started() {
		t.Fatal("recorder not started")
	}
	if !h.asr.Connected() {
		t.Fatal("asr not connected")
	}
	if !h.asr.Finalized() {
		t.Fatal("asr not finalized")
	}
	if !h.recorder.Closed() {
		t.Fatal("recorder not closed")
	}
	if !h.asr.Closed() {
		t.Fatal("asr not closed")
	}
}

func TestHoldModeAutoType(t *testing.T) {
	h := newHarness(t, "hold", true)

	h.press()
	time.Sleep(60 * time.Millisecond)
	h.release()
	h.waitResult(t, 2*time.Second)

	typed := h.autotype.Typed()
	if len(typed) != 1 || typed[0] != "hello world" {
		t.Fatalf("autotype = %v, want [hello world]", typed)
	}
}

func TestToggleMode(t *testing.T) {
	h := newHarness(t, "toggle", false)

	h.press() // start
	time.Sleep(60 * time.Millisecond)
	h.press() // stop
	h.waitResult(t, 2*time.Second)

	if h.resultText != "hello world" {
		t.Fatalf("result = %q, want %q", h.resultText, "hello world")
	}
	if h.clipboard.WriteCount() != 1 {
		t.Fatalf("clipboard writes = %d, want 1", h.clipboard.WriteCount())
	}
}

func TestToggleModeReleaseIgnored(t *testing.T) {
	h := newHarness(t, "toggle", false)

	h.press() // start
	time.Sleep(60 * time.Millisecond)
	h.release() // should be ignored in toggle mode
	time.Sleep(50 * time.Millisecond)

	// No result yet.
	select {
	case <-h.resultCh:
		t.Fatal("unexpected result in toggle mode after release")
	case <-time.After(50 * time.Millisecond):
	}

	h.press() // stop
	h.waitResult(t, 2*time.Second)

	if h.resultText != "hello world" {
		t.Fatalf("result = %q, want %q", h.resultText, "hello world")
	}
}

func TestASRConnectError(t *testing.T) {
	hk := mock.NewHotkey()
	cb := &mock.Clipboard{}
	rec := &mock.Recorder{Data: []byte{1, 2}}
	asr := &mock.ASRClient{
		ConnectErr: errors.New("connection refused"),
	}

	resultCh := make(chan struct{}, 1)
	var resultErr error
	v := New(Config{
		Hotkey:      hk,
		NewRecorder: func() platform.Recorder { return rec },
		NewASR:      func() platform.ASRClient { return asr },
		Clipboard:   cb,
		Key:         platform.Key{Mods: platform.ModCtrl, Code: "F1"},
		Mode:        "hold",
		AutoType:    false,
	})
	v.OnResult = func(_ string, err error) {
		resultErr = err
		select {
		case resultCh <- struct{}{}:
		default:
		}
	}
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}

	hk.Emit(platform.KeyEvent{Key: platform.Key{Mods: platform.ModCtrl, Code: "F1"}, Pressed: true})

	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error result")
	}

	if resultErr == nil {
		t.Fatal("expected error result")
	}
	if !rec.Closed() {
		t.Fatal("recorder should be closed after connect error")
	}
}

func TestRecorderStartError(t *testing.T) {
	hk := mock.NewHotkey()
	cb := &mock.Clipboard{}
	rec := &mock.Recorder{
		StartErr: errors.New("no device"),
	}
	asr := &mock.ASRClient{FinalText: "should not reach"}

	resultCh := make(chan struct{}, 1)
	var resultErr error
	v := New(Config{
		Hotkey:      hk,
		NewRecorder: func() platform.Recorder { return rec },
		NewASR:      func() platform.ASRClient { return asr },
		Clipboard:   cb,
		Key:         platform.Key{Mods: platform.ModCtrl, Code: "F1"},
		Mode:        "hold",
	})
	v.OnResult = func(_ string, err error) {
		resultErr = err
		select {
		case resultCh <- struct{}{}:
		default:
		}
	}
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}

	hk.Emit(platform.KeyEvent{Key: platform.Key{Mods: platform.ModCtrl, Code: "F1"}, Pressed: true})

	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error result")
	}

	if resultErr == nil {
		t.Fatal("expected error result")
	}
	if asr.Connected() {
		t.Fatal("ASR should not be connected when recorder fails")
	}
}

func TestStopCleansUp(t *testing.T) {
	h := newHarness(t, "hold", false)

	h.press()
	time.Sleep(60 * time.Millisecond)
	h.voice.Stop()

	// After Stop, the session should be cleaned up.
	if !h.recorder.Closed() {
		t.Fatal("recorder should be closed after Stop")
	}
}

func TestDoublePressIgnored(t *testing.T) {
	h := newHarness(t, "hold", false)

	h.press()
	time.Sleep(30 * time.Millisecond)
	h.press() // second press should be ignored in hold mode
	time.Sleep(60 * time.Millisecond)
	h.release()
	h.waitResult(t, 2*time.Second)

	if h.resultText != "hello world" {
		t.Fatalf("result = %q, want %q", h.resultText, "hello world")
	}
}
