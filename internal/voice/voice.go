// Package voice implements Eloqui's platform-independent recording lifecycle.
package voice

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	// DefaultStopDelay leaves a short tail after the stop gesture so the last
	// spoken syllable is not clipped.
	DefaultStopDelay = 800 * time.Millisecond
	// DefaultErrorHold keeps the error state visible long enough for R retry.
	DefaultErrorHold = 3 * time.Second
)

var (
	escapeKey = platform.Key{Code: platform.KeyEscape}
	retryKey  = platform.Key{Code: platform.KeyR}
)

// RecorderFactory returns a fresh Recorder for each recording session.
type RecorderFactory func() platform.Recorder

// ASRFactory returns a fresh ASRClient for each recording session.
type ASRFactory func() platform.ASRClient

// Config holds the dependencies and parameters for Voice.
type Config struct {
	Hotkey       platform.Hotkey
	NewRecorder  RecorderFactory
	NewASR       ASRFactory
	Clipboard    platform.Clipboard // required unless a usable Autotype is set
	Autotype     platform.Autotype  // optional; nil means clipboard-only
	Key          platform.Key
	Mode         string        // "hold" or "toggle"
	AutoType     bool          // true to inject text, false to only copy
	StopDelay    time.Duration // zero is immediate when StopDelaySet is true
	StopDelaySet bool          // false preserves the API default for P1 callers
	ErrorHold    time.Duration // zero selects DefaultErrorHold
}

// SessionResult is emitted exactly once for every session that entered the
// connecting state. It intentionally contains no stats/overlay dependencies.
type SessionResult struct {
	SessionID uint64
	Text      string
	Duration  time.Duration
	Err       error
	Cancelled bool
}

// stopResult carries the recorder's final chunk and stop error.
type stopResult struct {
	tail []byte
	err  error
}

// session owns all mutable state for one recording/finalization cycle.
type session struct {
	id uint64

	recorder platform.Recorder

	resourceMu        sync.Mutex
	asr               platform.ASRClient
	asrCloseRequested bool
	asrClosed         bool
	asrCancelOnce     sync.Once

	recorderReady   chan struct{}
	recorderStarted atomic.Bool
	connected       atomic.Bool

	stopRequested   atomic.Bool
	stopRequestedCh chan struct{}
	stopOnce        sync.Once
	stopCh          chan stopResult

	cancelled atomic.Bool

	// delayTimer is only accessed while Voice.mu is held.
	delayTimer *time.Timer

	errMu sync.Mutex
	errs  []error

	finalMu   sync.Mutex
	finalText string

	timingMu           sync.Mutex
	recordingStartedAt time.Time
	recordingDuration  time.Duration

	outputReady   atomic.Bool
	outputClaimed atomic.Bool
	outputDone    chan struct{}

	finishOnce sync.Once
	done       chan struct{}
}

func newSession(id uint64) *session {
	return &session{
		id:              id,
		recorderReady:   make(chan struct{}),
		stopRequestedCh: make(chan struct{}),
		stopCh:          make(chan stopResult, 1),
		outputDone:      make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// requestStop is non-blocking. The stop worker waits until Recorder.Start has
// completed, avoiding a Start/Stop race when hold is released very quickly.
func (s *session) requestStop() bool {
	first := false
	s.stopOnce.Do(func() {
		first = true
		s.stopRequested.Store(true)
		close(s.stopRequestedCh)
		go func() {
			<-s.recorderReady
			if !s.recorderStarted.Load() || s.recorder == nil {
				s.stopCh <- stopResult{}
				return
			}
			tail, err := s.recorder.Stop()
			s.markRecordingStopped()
			s.stopCh <- stopResult{tail: tail, err: err}
		}()
	})
	return first
}

func (s *session) attachASR(client platform.ASRClient) {
	s.resourceMu.Lock()
	s.asr = client
	closeRequested := s.asrCloseRequested && client != nil
	if closeRequested {
		s.asrClosed = true
	}
	s.resourceMu.Unlock()
	if closeRequested {
		go func() { _ = client.Close() }()
	}
}

func (s *session) closeASR() error {
	s.resourceMu.Lock()
	s.asrCloseRequested = true
	if s.asr == nil || s.asrClosed {
		s.resourceMu.Unlock()
		return nil
	}
	client := s.asr
	s.asrClosed = true
	s.resourceMu.Unlock()
	return client.Close()
}

func (s *session) cancelASR() {
	s.asrCancelOnce.Do(func() {
		go func() { _ = s.closeASR() }()
	})
}

func (s *session) addErr(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	s.errs = append(s.errs, err)
	s.errMu.Unlock()
}

func (s *session) failure() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return errors.Join(s.errs...)
}

func (s *session) offerFinal(text string) {
	if text == "" {
		return
	}
	s.finalMu.Lock()
	if s.finalText == "" {
		s.finalText = text
	}
	s.finalMu.Unlock()
}

func (s *session) resultText() string {
	s.finalMu.Lock()
	defer s.finalMu.Unlock()
	return s.finalText
}

func (s *session) markRecordingStarted() {
	s.timingMu.Lock()
	s.recordingStartedAt = time.Now()
	s.timingMu.Unlock()
}

func (s *session) markRecordingStopped() {
	s.timingMu.Lock()
	if !s.recordingStartedAt.IsZero() && s.recordingDuration == 0 {
		s.recordingDuration = time.Since(s.recordingStartedAt)
	}
	s.timingMu.Unlock()
}

func (s *session) recordedDuration() time.Duration {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.recordingDuration
}

// Voice ties hotkey edges to an explicit state machine and an asynchronous
// record-recognize-output pipeline.
type Voice struct {
	hotkey      platform.Hotkey
	newRecorder RecorderFactory
	newASR      ASRFactory
	clipboard   platform.Clipboard
	autotype    platform.Autotype
	key         platform.Key
	mode        string
	autoType    bool
	stopDelay   time.Duration
	errorHold   time.Duration

	machine *StateMachine

	mu         sync.Mutex
	current    *session
	nextID     uint64
	auxKey     platform.Key
	errorTimer *time.Timer
	errorToken uint64
	started    bool
	stopped    bool
	sessionWG  sync.WaitGroup

	quit         chan struct{}
	loopDone     chan struct{}
	shutdownDone chan struct{}
	stateQueue   *stateDispatcher

	// Hooks should be configured before Start. They are never invoked while
	// Voice.mu is held. Stop drains all hook calls before returning, so a hook
	// must not call Stop itself. OnResult is retained for P1 callers.
	OnResult      func(text string, err error)
	OnStateChange func(State)
	OnSession     func(SessionResult)
}

// New creates a Voice from the given config.
func New(cfg Config) *Voice {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "toggle" {
		mode = "hold"
	}
	stopDelay := cfg.StopDelay
	if !cfg.StopDelaySet && stopDelay == 0 {
		stopDelay = DefaultStopDelay
	}
	errorHold := cfg.ErrorHold
	if errorHold == 0 {
		errorHold = DefaultErrorHold
	}
	return &Voice{
		hotkey:       cfg.Hotkey,
		newRecorder:  cfg.NewRecorder,
		newASR:       cfg.NewASR,
		clipboard:    cfg.Clipboard,
		autotype:     cfg.Autotype,
		key:          cfg.Key,
		mode:         mode,
		autoType:     cfg.AutoType,
		stopDelay:    stopDelay,
		errorHold:    errorHold,
		machine:      NewStateMachine(),
		quit:         make(chan struct{}),
		loopDone:     make(chan struct{}),
		shutdownDone: make(chan struct{}),
		stateQueue:   newStateDispatcher(),
	}
}

// State returns the current explicit lifecycle state.
func (v *Voice) State() State { return v.machine.State() }

// Start registers the primary hotkey and starts the event and callback loops.
func (v *Voice) Start() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.started {
		return errors.New("voice: already started")
	}
	if v.stopped {
		return errors.New("voice: already stopped")
	}
	if v.hotkey == nil {
		return errors.New("voice: hotkey is required")
	}
	if err := v.hotkey.Register(v.key); err != nil {
		return err
	}
	v.started = true
	go v.callbackLoop()
	go v.eventLoop()
	return nil
}

// Stop cancels an active session and waits for resource cleanup. It does not
// close the shared Hotkey provider; application wiring still owns that.
func (v *Voice) Stop() {
	v.mu.Lock()
	if v.stopped {
		done := v.shutdownDone
		v.mu.Unlock()
		<-done
		return
	}
	v.stopped = true
	started := v.started
	cur := v.current
	if v.errorTimer != nil {
		v.errorTimer.Stop()
		v.errorTimer = nil
	}
	if cur != nil {
		cur.cancelled.Store(true)
		if cur.delayTimer != nil {
			cur.delayTimer.Stop()
			cur.delayTimer = nil
		}
		v.enterStoppingLocked()
	} else if v.machine.State() == StateError {
		v.mustTransitionLocked(StateIdle)
	}
	if started {
		close(v.quit)
	}
	v.mu.Unlock()

	if cur != nil {
		cur.requestStop()
		cur.cancelASR()
	}
	if started {
		<-v.loopDone
	}
	v.sessionWG.Wait()

	v.mu.Lock()
	_ = v.setAuxLocked(platform.Key{})
	v.mu.Unlock()
	if started {
		_ = v.hotkey.Unregister(v.key)
		v.stateQueue.closeAndWait()
	}
	close(v.shutdownDone)
}

func (v *Voice) callbackLoop() {
	v.stateQueue.run(func(state State) {
		if h := v.OnStateChange; h != nil {
			h(state)
		}
	})
}

func (v *Voice) eventLoop() {
	defer close(v.loopDone)
	for {
		select {
		case <-v.quit:
			return
		case ev, ok := <-v.hotkey.Events():
			if !ok {
				return
			}
			v.handleEvent(ev)
		}
	}
}

func (v *Voice) handleEvent(ev platform.KeyEvent) {
	switch ev.Key {
	case v.key:
		if ev.Pressed {
			v.onPrimaryPress()
		} else {
			v.onPrimaryRelease()
		}
	case escapeKey:
		if ev.Pressed {
			v.cancelCurrent()
		}
	case retryKey:
		if ev.Pressed {
			v.retryLast()
		}
	}
}

func (v *Voice) onPrimaryPress() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped {
		return
	}

	switch v.machine.State() {
	case StateIdle:
		v.startSessionLocked()
	case StateConnecting, StateRecording:
		if v.mode == "toggle" {
			v.beginStopDelayLocked()
		}
	case StateStoppingDelayed:
		v.cancelStopDelayLocked()
	}
}

func (v *Voice) onPrimaryRelease() {
	if v.mode != "hold" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped {
		return
	}
	switch v.machine.State() {
	case StateConnecting, StateRecording:
		v.beginStopDelayLocked()
	}
}

func (v *Voice) startSessionLocked() {
	if v.current != nil || v.machine.State() != StateIdle || v.stopped {
		return
	}
	v.nextID++
	s := newSession(v.nextID)
	v.current = s
	if err := v.transitionLocked(StateConnecting); err != nil {
		v.current = nil
		log.Printf("voice: start transition failed: %v", err)
		return
	}
	v.sessionWG.Add(1)
	go v.runSession(s)
}

func (v *Voice) beginStopDelayLocked() {
	s := v.current
	if s == nil {
		return
	}
	if err := v.transitionLocked(StateStoppingDelayed); err != nil {
		return
	}
	id := s.id
	s.delayTimer = time.AfterFunc(v.stopDelay, func() { v.commitDelayedStop(id) })
}

func (v *Voice) cancelStopDelayLocked() {
	s := v.current
	if s == nil || v.machine.State() != StateStoppingDelayed {
		return
	}
	if s.delayTimer != nil {
		s.delayTimer.Stop()
		s.delayTimer = nil
	}
	to := StateConnecting
	if s.connected.Load() {
		to = StateRecording
	}
	v.mustTransitionLocked(to)
}

func (v *Voice) commitDelayedStop(id uint64) {
	v.mu.Lock()
	s := v.current
	if v.stopped || s == nil || s.id != id || v.machine.State() != StateStoppingDelayed {
		v.mu.Unlock()
		return
	}
	s.delayTimer = nil
	v.mustTransitionLocked(StateStopping)
	v.mu.Unlock()
	s.requestStop()
}

func (v *Voice) cancelCurrent() {
	v.mu.Lock()
	s := v.current
	state := v.machine.State()
	if s == nil || (state != StateConnecting && state != StateRecording && state != StateStoppingDelayed && state != StateStopping) {
		v.mu.Unlock()
		return
	}
	s.cancelled.Store(true)
	if s.delayTimer != nil {
		s.delayTimer.Stop()
		s.delayTimer = nil
	}
	v.enterStoppingLocked()
	v.mu.Unlock()
	s.requestStop()
	s.cancelASR()
}

func (v *Voice) retryLast() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped || v.machine.State() != StateError || v.current != nil {
		return
	}
	if v.errorTimer != nil {
		v.errorTimer.Stop()
		v.errorTimer = nil
	}
	v.mustTransitionLocked(StateIdle)
	// A retry is a completely new session: factories are invoked again by
	// runSession, so no stopped/closed recorder or ASR instance is reused.
	v.startSessionLocked()
}

func (v *Voice) runSession(s *session) {
	if v.newRecorder == nil || v.newASR == nil {
		s.addErr(errors.New("voice: recorder and ASR factories are required"))
		close(s.recorderReady)
		v.finishSession(s)
		return
	}

	s.recorder = v.newRecorder()
	client := v.newASR()
	s.attachASR(client)
	if s.recorder == nil || client == nil {
		s.addErr(errors.New("voice: factory returned a nil dependency"))
		close(s.recorderReady)
		v.closeResources(s)
		v.finishSession(s)
		return
	}

	startErr := s.recorder.Start()
	if startErr == nil {
		s.recorderStarted.Store(true)
		s.markRecordingStarted()
	}
	close(s.recorderReady)
	if startErr != nil {
		s.addErr(fmt.Errorf("start recorder: %w", startErr))
		v.closeResources(s)
		v.finishSession(s)
		return
	}

	client.SetResultHandler(func(result platform.ASRResult) {
		if !result.Final || result.Text == "" {
			return
		}
		s.offerFinal(result.Text)
		if s.outputReady.Load() {
			v.deliverFinal(s)
		}
	})

	if s.cancelled.Load() {
		v.enterStopping(s)
		v.finalizeSession(s)
		return
	}
	if err := client.Connect(); err != nil {
		s.addErr(fmt.Errorf("connect ASR: %w", err))
		v.enterStopping(s)
		v.finalizeSession(s)
		return
	}
	s.connected.Store(true)
	v.markConnected(s)

	if s.stopRequested.Load() {
		v.enterStopping(s)
		v.finalizeSession(s)
		return
	}
	v.streamSession(s)
}

func (v *Voice) markConnected(s *session) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.current != s {
		return
	}
	if v.machine.State() == StateConnecting {
		v.mustTransitionLocked(StateRecording)
	}
}

func (v *Voice) streamSession(s *session) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.stopRequestedCh:
			v.enterStopping(s)
			v.finalizeSession(s)
			return
		default:
		}

		n, err := s.recorder.Read(buf)
		if n > 0 && !s.cancelled.Load() {
			if sendErr := s.asr.Send(buf[:n]); sendErr != nil {
				s.addErr(fmt.Errorf("send audio: %w", sendErr))
				v.enterStopping(s)
				s.requestStop()
				v.finalizeSession(s)
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.addErr(fmt.Errorf("read recorder: %w", err))
			} else if !s.stopRequested.Load() {
				s.addErr(errors.New("voice: recorder ended unexpectedly"))
			}
			v.enterStopping(s)
			s.requestStop()
			v.finalizeSession(s)
			return
		}
		if n == 0 {
			select {
			case <-s.stopRequestedCh:
				v.enterStopping(s)
				v.finalizeSession(s)
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}

func (v *Voice) enterStopping(s *session) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.current == s {
		v.enterStoppingLocked()
	}
}

func (v *Voice) enterStoppingLocked() {
	switch v.machine.State() {
	case StateConnecting, StateRecording, StateStoppingDelayed:
		v.mustTransitionLocked(StateStopping)
	}
}

func (v *Voice) finalizeSession(s *session) {
	s.requestStop()
	result := <-s.stopCh
	if result.err != nil {
		s.addErr(fmt.Errorf("stop recorder: %w", result.err))
	}
	if len(result.tail) > 0 && !s.cancelled.Load() && s.connected.Load() && s.failure() == nil {
		if err := s.asr.Send(result.tail); err != nil {
			s.addErr(fmt.Errorf("send final audio: %w", err))
		}
	}

	if !s.cancelled.Load() && s.connected.Load() && s.failure() == nil {
		text, err := s.asr.Finalize()
		if err != nil {
			if !s.cancelled.Load() {
				s.addErr(fmt.Errorf("finalize ASR: %w", err))
			}
		} else {
			s.offerFinal(text)
			s.outputReady.Store(true)
			v.deliverFinal(s)
			if s.outputClaimed.Load() {
				<-s.outputDone
			}
		}
	}

	v.closeResources(s)
	v.finishSession(s)
}

func (v *Voice) deliverFinal(s *session) {
	if !s.outputReady.Load() || s.cancelled.Load() || s.failure() != nil {
		return
	}
	text := s.resultText()
	if text == "" || !s.outputClaimed.CompareAndSwap(false, true) {
		return
	}
	defer close(s.outputDone)
	// Escape may race the final callback. Re-check after winning the atomic
	// claim so cancellation never leaves a second path able to output later.
	if s.cancelled.Load() {
		return
	}
	if err := v.output(text); err != nil {
		s.addErr(err)
	}
}

func (v *Voice) closeResources(s *session) {
	if s.asr != nil {
		if err := s.closeASR(); err != nil {
			s.addErr(fmt.Errorf("close ASR: %w", err))
		}
	}
	if s.recorder != nil {
		if err := s.recorder.Close(); err != nil {
			s.addErr(fmt.Errorf("close recorder: %w", err))
		}
	}
}

func (v *Voice) finishSession(s *session) {
	s.finishOnce.Do(func() {
		defer v.sessionWG.Done()
		failure := s.failure()
		cancelled := s.cancelled.Load()
		text := s.resultText()
		if cancelled {
			text = ""
		}

		v.mu.Lock()
		if v.current == s {
			v.current = nil
			if cancelled {
				v.enterStoppingLocked()
				v.mustTransitionLocked(StateIdle)
			} else if failure != nil {
				v.mustTransitionLocked(StateError)
				v.scheduleErrorResetLocked(s.id)
			} else {
				v.enterStoppingLocked()
				v.mustTransitionLocked(StateIdle)
			}
		}
		v.mu.Unlock()

		result := SessionResult{
			SessionID: s.id,
			Text:      text,
			Duration:  s.recordedDuration(),
			Err:       failure,
			Cancelled: cancelled,
		}
		if failure != nil && !cancelled {
			log.Printf("voice: session %d failed: %v", s.id, failure)
		}
		if h := v.OnResult; h != nil {
			h(result.Text, result.Err)
		}
		if h := v.OnSession; h != nil {
			h(result)
		}
		close(s.done)
	})
}

func (v *Voice) scheduleErrorResetLocked(token uint64) {
	if v.errorTimer != nil {
		v.errorTimer.Stop()
	}
	v.errorToken = token
	v.errorTimer = time.AfterFunc(v.errorHold, func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.stopped || v.machine.State() != StateError || v.errorToken != token {
			return
		}
		v.errorTimer = nil
		v.mustTransitionLocked(StateIdle)
	})
}

// output delivers text and reports delivery errors. If autotype fails, a
// clipboard fallback is attempted and both errors are retained when needed.
func (v *Voice) output(text string) error {
	if v.autoType && v.autotype != nil {
		err := v.autotype.Type(text)
		if err == nil {
			return nil
		}
		if v.clipboard != nil {
			return errors.Join(err, v.clipboard.Write(text))
		}
		return err
	}
	if v.clipboard != nil {
		return v.clipboard.Write(text)
	}
	return errors.New("voice: no output backend configured")
}

func (v *Voice) mustTransitionLocked(to State) {
	if err := v.transitionLocked(to); err != nil {
		log.Printf("voice: state transition failed: %v", err)
	}
}

func (v *Voice) transitionLocked(to State) error {
	if err := v.machine.Transition(to); err != nil {
		return err
	}
	if err := v.setAuxLocked(auxiliaryKey(to)); err != nil {
		log.Printf("voice: auxiliary hotkey for %s: %v", to, err)
	}
	v.stateQueue.enqueue(to)
	return nil
}

func auxiliaryKey(state State) platform.Key {
	switch state {
	case StateConnecting, StateRecording, StateStoppingDelayed, StateStopping:
		return escapeKey
	case StateError:
		return retryKey
	default:
		return platform.Key{}
	}
}

// setAuxLocked updates the temporary Escape/R binding. Voice.mu serializes
// this method with all state transitions.
func (v *Voice) setAuxLocked(next platform.Key) error {
	if v.auxKey == next {
		return nil
	}
	var errs []error
	if v.auxKey != (platform.Key{}) {
		if err := v.hotkey.Unregister(v.auxKey); err != nil {
			errs = append(errs, err)
		}
		v.auxKey = platform.Key{}
	}
	if next != (platform.Key{}) {
		if err := v.hotkey.Register(next); err != nil {
			errs = append(errs, err)
		} else {
			v.auxKey = next
		}
	}
	return errors.Join(errs...)
}
