package voice

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/mock"
)

const p2TestDelay = 25 * time.Millisecond

type p2Harness struct {
	voice     *Voice
	hotkey    *mock.Hotkey
	clipboard *mock.Clipboard
	sessions  chan SessionResult
	key       platform.Key
}

type gatedStartRecorder struct {
	startEntered chan struct{}
	releaseStart chan struct{}
	mu           sync.Mutex
	stopCalls    int
}

type closeCancellableASR struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	closeOnce sync.Once
	closed    atomic.Bool
}

func (a *closeCancellableASR) Connect() error                          { return nil }
func (a *closeCancellableASR) Send([]byte) error                       { return nil }
func (a *closeCancellableASR) SetResultHandler(platform.ResultHandler) {}
func (a *closeCancellableASR) Finalize() (string, error) {
	a.enterOnce.Do(func() { close(a.entered) })
	<-a.release
	return "", errors.New("finalization cancelled")
}
func (a *closeCancellableASR) Close() error {
	a.closed.Store(true)
	a.closeOnce.Do(func() { close(a.release) })
	return nil
}

func (r *gatedStartRecorder) Start() error {
	close(r.startEntered)
	<-r.releaseStart
	return nil
}

func (r *gatedStartRecorder) Read([]byte) (int, error) { return 0, nil }

func (r *gatedStartRecorder) Stop() ([]byte, error) {
	r.mu.Lock()
	r.stopCalls++
	r.mu.Unlock()
	return nil, nil
}

func (r *gatedStartRecorder) Close() error { return nil }

func (r *gatedStartRecorder) StopCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopCalls
}

func newP2Harness(
	t *testing.T,
	mode string,
	key platform.Key,
	newRecorder RecorderFactory,
	newASR ASRFactory,
) *p2Harness {
	t.Helper()
	hk := mock.NewHotkey()
	cb := &mock.Clipboard{}
	if newRecorder == nil {
		newRecorder = func() platform.Recorder {
			return &mock.Recorder{Data: []byte{1, 2, 3, 4}, ChunkSize: 2}
		}
	}
	if newASR == nil {
		newASR = func() platform.ASRClient { return &mock.ASRClient{FinalText: "p2 result"} }
	}
	v := New(Config{
		Hotkey:      hk,
		NewRecorder: newRecorder,
		NewASR:      newASR,
		Clipboard:   cb,
		Key:         key,
		Mode:        mode,
		StopDelay:   p2TestDelay,
		ErrorHold:   250 * time.Millisecond,
	})
	sessions := make(chan SessionResult, 16)
	v.OnSession = func(result SessionResult) { sessions <- result }
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	h := &p2Harness{voice: v, hotkey: hk, clipboard: cb, sessions: sessions, key: key}
	t.Cleanup(func() {
		v.Stop()
		_ = hk.Close()
	})
	return h
}

func (h *p2Harness) emit(pressed bool) {
	h.hotkey.Emit(platform.KeyEvent{Key: h.key, Pressed: pressed})
}

func waitVoiceState(t *testing.T, v *Voice, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state = %s, want %s", v.State(), want)
}

func waitSession(t *testing.T, ch <-chan SessionResult) SessionResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session result")
		return SessionResult{}
	}
}

func TestStopDelayCanBeCancelledInHoldMode(t *testing.T) {
	key := platform.Key{Code: "F6"}
	h := newP2Harness(t, "hold", key, nil, nil)

	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	h.emit(false)
	waitVoiceState(t, h.voice, StateStoppingDelayed)

	// Pressing again within the tail buffer resumes this same session.
	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	time.Sleep(2 * p2TestDelay)
	if got := h.voice.State(); got != StateRecording {
		t.Fatalf("cancelled timer changed state to %s", got)
	}

	h.emit(false)
	result := waitSession(t, h.sessions)
	if result.Err != nil || result.Cancelled || result.Text != "p2 result" {
		t.Fatalf("session result = %+v", result)
	}
	if result.SessionID != 1 {
		t.Fatalf("session id = %d, want 1", result.SessionID)
	}
	waitVoiceState(t, h.voice, StateIdle)
}

func TestExplicitZeroStopDelayStopsImmediately(t *testing.T) {
	key := platform.Key{Code: "F15"}
	hk := mock.NewHotkey()
	v := New(Config{
		Hotkey:       hk,
		NewRecorder:  func() platform.Recorder { return &mock.Recorder{Data: []byte{1}} },
		NewASR:       func() platform.ASRClient { return &mock.ASRClient{FinalText: "no delay"} },
		Clipboard:    &mock.Clipboard{},
		Key:          key,
		Mode:         "hold",
		StopDelay:    0,
		StopDelaySet: true,
	})
	if v.stopDelay != 0 {
		t.Fatalf("runtime stop delay = %v, want explicit zero", v.stopDelay)
	}
	sessions := make(chan SessionResult, 1)
	v.OnSession = func(result SessionResult) { sessions <- result }
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		v.Stop()
		_ = hk.Close()
	})

	hk.Emit(platform.KeyEvent{Key: key, Pressed: true})
	waitVoiceState(t, v, StateRecording)
	started := time.Now()
	hk.Emit(platform.KeyEvent{Key: key, Pressed: false})
	result := waitSession(t, sessions)
	if result.Err != nil || result.Text != "no delay" {
		t.Fatalf("result = %+v", result)
	}
	if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
		t.Fatalf("explicit zero delay took %v, want less than 250ms", elapsed)
	}
}

func TestTenRapidTogglePressesFinishOnce(t *testing.T) {
	key := platform.Key{Code: "F7"}
	var recorderCount atomic.Int32
	h := newP2Harness(t, "toggle", key, func() platform.Recorder {
		recorderCount.Add(1)
		return &mock.Recorder{Data: []byte{1, 2, 3}}
	}, nil)

	for i := 0; i < 10; i++ {
		h.emit(true)
	}
	result := waitSession(t, h.sessions)
	if result.Err != nil || result.Cancelled {
		t.Fatalf("session result = %+v", result)
	}
	if got := recorderCount.Load(); got != 1 {
		t.Fatalf("recorder sessions = %d, want 1", got)
	}
	if got := h.clipboard.WriteCount(); got != 1 {
		t.Fatalf("clipboard writes = %d, want 1", got)
	}
	waitVoiceState(t, h.voice, StateIdle)
	select {
	case duplicate := <-h.sessions:
		t.Fatalf("unexpected duplicate completion: %+v", duplicate)
	case <-time.After(2 * p2TestDelay):
	}
}

func TestHoldQuickTapDuringConnecting(t *testing.T) {
	key := platform.Key{Mods: platform.ModAlt | platform.ModSuper}
	h := newP2Harness(t, "hold", key, nil, nil)

	// Queue both physical edges without waiting for Recorder.Start/Connect.
	h.emit(true)
	h.emit(false)
	result := waitSession(t, h.sessions)
	if result.Err != nil || result.Cancelled {
		t.Fatalf("quick-tap result = %+v", result)
	}
	if h.clipboard.WriteCount() != 1 {
		t.Fatalf("clipboard writes = %d, want 1", h.clipboard.WriteCount())
	}
	waitVoiceState(t, h.voice, StateIdle)
}

func TestModifierOnlyToggleAndFunctionKeyHold(t *testing.T) {
	tests := []struct {
		name string
		mode string
		key  platform.Key
	}{
		{"modifier-only toggle", "toggle", platform.Key{Mods: platform.ModAlt | platform.ModSuper}},
		{"function-key hold", "hold", platform.Key{Code: "F12"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newP2Harness(t, tt.mode, tt.key, nil, nil)
			h.emit(true)
			waitVoiceState(t, h.voice, StateRecording)
			if tt.mode == "toggle" {
				h.emit(false) // release is not the toggle stop gesture
				h.emit(true)
			} else {
				h.emit(false)
			}
			result := waitSession(t, h.sessions)
			if result.Err != nil || result.Text != "p2 result" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestFinalCallbackAndFinalizeReturnOutputOnce(t *testing.T) {
	key := platform.Key{Code: "F8"}
	asr := &mock.ASRClient{FinalText: "one final"}
	h := newP2Harness(t, "hold", key, nil, func() platform.ASRClient { return asr })

	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	// Two early final callbacks plus mock Finalize's callback and return all
	// converge on the same per-session atomic claim.
	asr.Emit(platform.ASRResult{Text: "one final", Final: true})
	asr.Emit(platform.ASRResult{Text: "one final", Final: true})
	h.emit(false)
	result := waitSession(t, h.sessions)
	if result.Err != nil || result.Text != "one final" {
		t.Fatalf("result = %+v", result)
	}
	if got := h.clipboard.WriteCount(); got != 1 {
		t.Fatalf("clipboard writes = %d, want 1", got)
	}
}

func TestEscapeCancelsRecordingWithoutOutput(t *testing.T) {
	key := platform.Key{Code: "F9"}
	asr := &mock.ASRClient{FinalText: "must not be delivered"}
	h := newP2Harness(t, "hold", key, nil, func() platform.ASRClient { return asr })

	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	if !h.hotkey.Registered(escapeKey) {
		t.Fatal("Escape auxiliary hotkey is not registered while recording")
	}
	h.hotkey.Emit(platform.KeyEvent{Key: escapeKey, Pressed: true})
	result := waitSession(t, h.sessions)
	if !result.Cancelled || result.Text != "" || result.Err != nil {
		t.Fatalf("cancel result = %+v", result)
	}
	if got := h.clipboard.WriteCount(); got != 0 {
		t.Fatalf("clipboard writes = %d after cancel, want 0", got)
	}
	if asr.Finalized() {
		t.Fatal("cancelled recording should not call ASR Finalize")
	}
	waitVoiceState(t, h.voice, StateIdle)
	if h.hotkey.Registered(escapeKey) {
		t.Fatal("Escape auxiliary hotkey still registered in idle")
	}
}

func TestEscapeDuringConnectingWaitsForStartBeforeStop(t *testing.T) {
	key := platform.Key{Code: "F19"}
	recorder := &gatedStartRecorder{
		startEntered: make(chan struct{}),
		releaseStart: make(chan struct{}),
	}
	asr := &mock.ASRClient{FinalText: "must not connect"}
	h := newP2Harness(
		t,
		"hold",
		key,
		func() platform.Recorder { return recorder },
		func() platform.ASRClient { return asr },
	)

	h.emit(true)
	select {
	case <-recorder.startEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("Recorder.Start did not begin")
	}
	waitVoiceState(t, h.voice, StateConnecting)
	h.hotkey.Emit(platform.KeyEvent{Key: escapeKey, Pressed: true})
	deadline := time.Now().Add(2 * time.Second)
	cancelled := false
	for time.Now().Before(deadline) {
		h.voice.mu.Lock()
		cancelled = h.voice.current != nil && h.voice.current.cancelled.Load()
		h.voice.mu.Unlock()
		if cancelled {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !cancelled {
		t.Fatal("Escape was not processed during Recorder.Start")
	}
	if got := recorder.StopCalls(); got != 0 {
		t.Fatalf("Recorder.Stop raced Start: calls = %d before Start returned", got)
	}
	close(recorder.releaseStart)
	result := waitSession(t, h.sessions)
	if !result.Cancelled || result.Err != nil || result.Text != "" {
		t.Fatalf("connecting cancel result = %+v", result)
	}
	if got := recorder.StopCalls(); got != 1 {
		t.Fatalf("Recorder.Stop calls = %d, want 1", got)
	}
	if asr.Connected() || asr.Finalized() {
		t.Fatal("ASR should not connect or finalize after connecting cancellation")
	}
}

func TestEscapeCancelsBlockedFinalization(t *testing.T) {
	key := platform.Key{Code: "F10"}
	asr := &slowASR{entered: make(chan struct{}), release: make(chan struct{})}
	h := newP2Harness(t, "hold", key, nil, func() platform.ASRClient { return asr })

	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	h.emit(false)
	select {
	case <-asr.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Finalize did not start")
	}
	if got := h.voice.State(); got != StateStopping {
		t.Fatalf("state while Finalize blocked = %s, want stopping", got)
	}
	h.hotkey.Emit(platform.KeyEvent{Key: escapeKey, Pressed: true})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.voice.mu.Lock()
		cancelled := h.voice.current != nil && h.voice.current.cancelled.Load()
		h.voice.mu.Unlock()
		if cancelled {
			break
		}
		time.Sleep(time.Millisecond)
	}
	h.voice.mu.Lock()
	cancelled := h.voice.current != nil && h.voice.current.cancelled.Load()
	h.voice.mu.Unlock()
	if !cancelled {
		t.Fatal("Escape was not processed while Finalize was blocked")
	}
	close(asr.release)
	result := waitSession(t, h.sessions)
	if !result.Cancelled || result.Text != "" {
		t.Fatalf("cancelled finalization result = %+v", result)
	}
	if got := h.clipboard.WriteCount(); got != 0 {
		t.Fatalf("clipboard writes = %d after finalization cancel, want 0", got)
	}
}

func TestEscapeClosesASRAndUnblocksFinalization(t *testing.T) {
	key := platform.Key{Code: "F18"}
	asr := &closeCancellableASR{entered: make(chan struct{}), release: make(chan struct{})}
	h := newP2Harness(t, "hold", key, nil, func() platform.ASRClient { return asr })

	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	h.emit(false)
	select {
	case <-asr.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Finalize did not start")
	}
	h.hotkey.Emit(platform.KeyEvent{Key: escapeKey, Pressed: true})
	result := waitSession(t, h.sessions)
	if !result.Cancelled || result.Err != nil || result.Text != "" {
		t.Fatalf("cancelled finalization result = %+v", result)
	}
	if !asr.closed.Load() {
		t.Fatal("Escape did not close the active ASR client")
	}
}

func TestSessionDurationExcludesFinalizeLatency(t *testing.T) {
	key := platform.Key{Code: "F16"}
	asr := &slowASR{entered: make(chan struct{}), release: make(chan struct{})}
	h := newP2Harness(t, "hold", key, nil, func() platform.ASRClient { return asr })

	h.emit(true)
	waitVoiceState(t, h.voice, StateRecording)
	wallStarted := time.Now()
	h.emit(false)
	select {
	case <-asr.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Finalize did not start")
	}
	time.Sleep(120 * time.Millisecond)
	close(asr.release)
	result := waitSession(t, h.sessions)
	wallDuration := time.Since(wallStarted)
	if result.Duration <= 0 {
		t.Fatalf("recording duration = %v, want positive", result.Duration)
	}
	if excluded := wallDuration - result.Duration; excluded < 100*time.Millisecond {
		t.Fatalf("wall=%v recording=%v: Finalize latency was not excluded", wallDuration, result.Duration)
	}
}

func TestErrorStateRetryUsesFreshDependencies(t *testing.T) {
	key := platform.Key{Code: "F11"}
	var mu sync.Mutex
	var recorders []*mock.Recorder
	var clients []*mock.ASRClient
	newRecorder := func() platform.Recorder {
		mu.Lock()
		defer mu.Unlock()
		r := &mock.Recorder{Data: []byte{1, 2}}
		if len(recorders) == 0 {
			r.StartErr = errors.New("microphone temporarily busy")
		}
		recorders = append(recorders, r)
		return r
	}
	newASR := func() platform.ASRClient {
		mu.Lock()
		defer mu.Unlock()
		c := &mock.ASRClient{FinalText: "retry succeeded"}
		clients = append(clients, c)
		return c
	}
	h := newP2Harness(t, "hold", key, newRecorder, newASR)

	h.emit(true)
	first := waitSession(t, h.sessions)
	if first.Err == nil || first.Cancelled {
		t.Fatalf("first result = %+v, want failure", first)
	}
	if first.Duration != 0 {
		t.Fatalf("failed Recorder.Start duration = %v, want 0", first.Duration)
	}
	waitVoiceState(t, h.voice, StateError)
	if !h.hotkey.Registered(retryKey) || h.hotkey.Registered(escapeKey) {
		t.Fatal("error state must register only the R retry binding")
	}

	h.hotkey.Emit(platform.KeyEvent{Key: retryKey, Pressed: true})
	waitVoiceState(t, h.voice, StateRecording)
	h.emit(false)
	second := waitSession(t, h.sessions)
	if second.Err != nil || second.Text != "retry succeeded" || second.SessionID == first.SessionID {
		t.Fatalf("retry result = %+v after %+v", second, first)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(recorders) != 2 || len(clients) != 2 {
		t.Fatalf("factory counts = recorders:%d clients:%d, want 2/2", len(recorders), len(clients))
	}
	if recorders[0] == recorders[1] || clients[0] == clients[1] {
		t.Fatal("retry reused a recorder or ASR client")
	}
}

func TestErrorStateExpiresToIdle(t *testing.T) {
	key := platform.Key{Code: "F13"}
	hk := mock.NewHotkey()
	v := New(Config{
		Hotkey:      hk,
		NewRecorder: func() platform.Recorder { return &mock.Recorder{StartErr: errors.New("no input")} },
		NewASR:      func() platform.ASRClient { return &mock.ASRClient{} },
		Clipboard:   &mock.Clipboard{},
		Key:         key,
		Mode:        "hold",
		StopDelay:   p2TestDelay,
		ErrorHold:   40 * time.Millisecond,
	})
	sessions := make(chan SessionResult, 1)
	v.OnSession = func(result SessionResult) { sessions <- result }
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		v.Stop()
		_ = hk.Close()
	})

	hk.Emit(platform.KeyEvent{Key: key, Pressed: true})
	if result := waitSession(t, sessions); result.Err == nil {
		t.Fatal("expected start failure")
	}
	waitVoiceState(t, v, StateError)
	waitVoiceState(t, v, StateIdle)
	if hk.Registered(retryKey) {
		t.Fatal("R retry binding still registered after error timeout")
	}
}

func TestHooksAreUnlockedOrderedAndExactlyOnce(t *testing.T) {
	key := platform.Key{Code: "F14"}
	hk := mock.NewHotkey()
	cb := &mock.Clipboard{}
	asr := &mock.ASRClient{FinalText: "hook result"}
	v := New(Config{
		Hotkey:      hk,
		NewRecorder: func() platform.Recorder { return &mock.Recorder{Data: []byte{1}} },
		NewASR:      func() platform.ASRClient { return asr },
		Clipboard:   cb,
		Key:         key,
		Mode:        "hold",
		StopDelay:   p2TestDelay,
	})
	states := make(chan State, 16)
	sessions := make(chan SessionResult, 4)
	var resultCalls atomic.Int32
	v.OnStateChange = func(state State) {
		_ = v.State() // would deadlock if the hook ran under the machine lock
		states <- state
	}
	v.OnResult = func(string, error) { resultCalls.Add(1) }
	v.OnSession = func(result SessionResult) {
		_ = v.State() // would deadlock if the hook ran under Voice.mu
		sessions <- result
	}
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		v.Stop()
		_ = hk.Close()
	})

	hk.Emit(platform.KeyEvent{Key: key, Pressed: true})
	waitVoiceState(t, v, StateRecording)
	asr.Emit(platform.ASRResult{Text: "hook result", Final: true})
	asr.Emit(platform.ASRResult{Text: "hook result", Final: true})
	hk.Emit(platform.KeyEvent{Key: key, Pressed: false})
	result := waitSession(t, sessions)
	if result.SessionID != 1 || result.Text != "hook result" || result.Duration <= 0 || result.Err != nil || result.Cancelled {
		t.Fatalf("session hook result = %+v", result)
	}
	if got := resultCalls.Load(); got != 1 {
		t.Fatalf("OnResult calls = %d, want 1", got)
	}
	if got := cb.WriteCount(); got != 1 {
		t.Fatalf("clipboard writes = %d, want 1", got)
	}

	wantStates := []State{StateConnecting, StateRecording, StateStoppingDelayed, StateStopping, StateIdle}
	for i, want := range wantStates {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state hook %d = %s, want %s", i, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for state hook %s", want)
		}
	}
	select {
	case duplicate := <-sessions:
		t.Fatalf("duplicate OnSession result: %+v", duplicate)
	case <-time.After(2 * p2TestDelay):
	}
}

func TestBlockedStateHookDoesNotBackpressureAndStopDrains(t *testing.T) {
	key := platform.Key{Code: "F17"}
	hk := mock.NewHotkey()
	v := New(Config{
		Hotkey:       hk,
		NewRecorder:  func() platform.Recorder { return &mock.Recorder{Data: []byte{1}} },
		NewASR:       func() platform.ASRClient { return &mock.ASRClient{FinalText: "queued"} },
		Clipboard:    &mock.Clipboard{},
		Key:          key,
		Mode:         "hold",
		StopDelaySet: true,
	})
	hookEntered := make(chan struct{})
	releaseHook := make(chan struct{})
	var firstHook sync.Once
	var hookCalls atomic.Int32
	v.OnStateChange = func(State) {
		hookCalls.Add(1)
		firstHook.Do(func() {
			close(hookEntered)
			<-releaseHook
		})
	}
	sessions := make(chan SessionResult, 1)
	v.OnSession = func(result SessionResult) { sessions <- result }
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hk.Close() }()

	const sessionCount = 70 // five transitions each: deliberately exceeds the old 256 buffer
	for i := 0; i < sessionCount; i++ {
		hk.Emit(platform.KeyEvent{Key: key, Pressed: true})
		waitVoiceState(t, v, StateRecording)
		if i == 0 {
			select {
			case <-hookEntered:
			case <-time.After(2 * time.Second):
				t.Fatal("state hook did not start")
			}
		}
		hk.Emit(platform.KeyEvent{Key: key, Pressed: false})
		result := waitSession(t, sessions)
		if result.Err != nil || result.Cancelled {
			t.Fatalf("session %d result = %+v", i, result)
		}
		waitVoiceState(t, v, StateIdle)
	}

	stopped := make(chan struct{})
	go func() {
		v.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned before the queued state hook was released")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHook)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not drain queued state hooks")
	}

	wantCalls := int32(sessionCount * 5)
	if got := hookCalls.Load(); got != wantCalls {
		t.Fatalf("state hook calls = %d, want %d", got, wantCalls)
	}
	stable := hookCalls.Load()
	time.Sleep(30 * time.Millisecond)
	if got := hookCalls.Load(); got != stable {
		t.Fatalf("state hooks continued after Stop: %d -> %d", stable, got)
	}
}

func TestStopWaitsForSessionHookAndNoCallbackRunsAfterReturn(t *testing.T) {
	key := platform.Key{Code: "F18"}
	hk := mock.NewHotkey()
	v := New(Config{
		Hotkey:       hk,
		NewRecorder:  func() platform.Recorder { return &mock.Recorder{Data: []byte{1}} },
		NewASR:       func() platform.ASRClient { return &mock.ASRClient{FinalText: "done"} },
		Clipboard:    &mock.Clipboard{},
		Key:          key,
		Mode:         "hold",
		StopDelaySet: true,
	})
	hookEntered := make(chan struct{})
	releaseHook := make(chan struct{})
	var sessionCalls atomic.Int32
	v.OnSession = func(SessionResult) {
		sessionCalls.Add(1)
		close(hookEntered)
		<-releaseHook
	}
	if err := v.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hk.Close() }()

	hk.Emit(platform.KeyEvent{Key: key, Pressed: true})
	waitVoiceState(t, v, StateRecording)
	hk.Emit(platform.KeyEvent{Key: key, Pressed: false})
	select {
	case <-hookEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("OnSession did not start")
	}

	stopped := make(chan struct{})
	go func() {
		v.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while OnSession was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHook)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after OnSession completed")
	}
	if got := sessionCalls.Load(); got != 1 {
		t.Fatalf("OnSession calls = %d, want 1", got)
	}
	time.Sleep(30 * time.Millisecond)
	if got := sessionCalls.Load(); got != 1 {
		t.Fatalf("OnSession ran after Stop returned: %d calls", got)
	}
}
