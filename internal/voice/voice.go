// Package voice implements the core recording-to-text flow: it listens for
// hotkey events, drives the recorder and ASR client, and delivers the
// recognized text to the clipboard or autotype.
//
// This P1 implementation deliberately keeps one session active through
// recording and asynchronous finalization. The richer explicit state machine,
// stop-delay buffer and session IDs arrive in P2; those will build on the
// same "a session owns its lifecycle" invariant introduced here.
package voice

import (
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// RecorderFactory returns a fresh Recorder for each recording session.
type RecorderFactory func() platform.Recorder

// ASRFactory returns a fresh ASRClient for each recording session.
type ASRFactory func() platform.ASRClient

// Config holds the dependencies and parameters for Voice.
type Config struct {
	Hotkey      platform.Hotkey
	NewRecorder RecorderFactory
	NewASR      ASRFactory
	Clipboard   platform.Clipboard // required
	Autotype    platform.Autotype  // optional; nil means clipboard-only
	Key         platform.Key
	Mode        string // "hold" or "toggle"
	AutoType    bool   // true to inject text, false to only copy
}

// stopResult carries the recorder's final chunk and stop error.
type stopResult struct {
	tail []byte
	err  error
}

// session represents one complete recording/finalization cycle. It remains
// Voice.current until finalization has finished, preventing overlapping
// uploads or out-of-order output.
type session struct {
	recorder platform.Recorder
	asr      platform.ASRClient

	stopRequested   atomic.Bool
	stopRequestedCh chan struct{}
	stopOnce        sync.Once
	stopCh          chan stopResult

	mu   sync.Mutex
	errs []error
	done chan struct{}
}

func newSession(rec platform.Recorder, asr platform.ASRClient) *session {
	s := &session{
		recorder:        rec,
		asr:             asr,
		stopRequestedCh: make(chan struct{}),
		stopCh:          make(chan stopResult, 1),
		done:            make(chan struct{}),
	}
	return s
}

// requestStop starts exactly one asynchronous recorder stop. The recorder is
// expected to wake any blocked Read when Stop is called. The Linux arecord
// implementation guarantees this; the mocks satisfy it by returning io.EOF
// after Stop.
func (s *session) requestStop() bool {
	first := false
	s.stopOnce.Do(func() {
		first = true
		s.stopRequested.Store(true)
		close(s.stopRequestedCh)
		go func() {
			tail, err := s.recorder.Stop()
			s.stopCh <- stopResult{tail: tail, err: err}
		}()
	})
	return first
}

func (s *session) stopping() bool {
	return s.stopRequested.Load()
}

func (s *session) addErr(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.errs = append(s.errs, err)
	s.mu.Unlock()
}

func (s *session) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.errs...)
}

// Voice ties hotkey events to the record-recognize-output pipeline.
type Voice struct {
	hotkey      platform.Hotkey
	newRecorder RecorderFactory
	newASR      ASRFactory
	clipboard   platform.Clipboard
	autotype    platform.Autotype
	key         platform.Key
	mode        string
	autoType    bool

	mu      sync.Mutex
	current *session
	stopped bool

	// OnResult receives every terminal session outcome. It may run on a session
	// goroutine and must not call back into Voice methods that acquire mu.
	OnResult func(text string, err error)
}

// New creates a Voice from the given config.
func New(cfg Config) *Voice {
	return &Voice{
		hotkey:      cfg.Hotkey,
		newRecorder: cfg.NewRecorder,
		newASR:      cfg.NewASR,
		clipboard:   cfg.Clipboard,
		autotype:    cfg.Autotype,
		key:         cfg.Key,
		mode:        cfg.Mode,
		autoType:    cfg.AutoType,
	}
}

// Start registers the hotkey and begins listening for events.
func (v *Voice) Start() error {
	if err := v.hotkey.Register(v.key); err != nil {
		return err
	}
	go v.eventLoop()
	return nil
}

// Stop unregisters the hotkey, cancels the active session and waits until all
// recorder/ASR/output cleanup has completed.
func (v *Voice) Stop() {
	v.mu.Lock()
	if v.stopped {
		v.mu.Unlock()
		return
	}
	v.stopped = true
	cur := v.current
	v.mu.Unlock()

	_ = v.hotkey.Unregister(v.key)
	if cur != nil {
		cur.requestStop()
		<-cur.done
	}
}

func (v *Voice) eventLoop() {
	for ev := range v.hotkey.Events() {
		if ev.Pressed {
			v.onPress()
		} else {
			v.onRelease()
		}
	}
}

func (v *Voice) onPress() {
	v.mu.Lock()
	cur := v.current
	if cur != nil {
		stopping := cur.stopping()
		v.mu.Unlock()

		// Toggle mode: the second genuine press stops a still-recording
		// session. A press during finalization is ignored until the session
		// has completely drained.
		if v.mode == "toggle" && !stopping {
			cur.requestStop()
		}
		return
	}
	if v.stopped {
		v.mu.Unlock()
		return
	}
	s, startErr := v.startSession()
	if s == nil {
		v.mu.Unlock()
		v.emitResult("", startErr)
		return
	}
	v.current = s
	v.mu.Unlock()
}

func (v *Voice) onRelease() {
	if v.mode != "hold" {
		return
	}
	v.mu.Lock()
	cur := v.current
	v.mu.Unlock()
	if cur != nil && !cur.stopping() {
		cur.requestStop()
	}
}

// startSession starts a new recorder and ASR session. It must be called with
// v.mu held, but returns before launching the streaming goroutine.
func (v *Voice) startSession() (*session, error) {
	rec := v.newRecorder()
	asr := v.newASR()

	if err := rec.Start(); err != nil {
		_ = rec.Close()
		return nil, err
	}
	if err := asr.Connect(); err != nil {
		_ = rec.Close()
		_ = asr.Close()
		return nil, err
	}

	s := newSession(rec, asr)
	go v.runSession(s)
	return s, nil
}

func (v *Voice) runSession(s *session) {
	defer close(s.done)
	defer v.clearCurrent(s)

	buf := make([]byte, 4096)
	for {
		select {
		case <-s.stopRequestedCh:
			v.finalizeSession(s)
			return
		default:
		}

		n, err := s.recorder.Read(buf)
		if n > 0 {
			if sendErr := s.asr.Send(buf[:n]); sendErr != nil {
				s.addErr(sendErr)
				v.finalizeSession(s)
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.addErr(err)
			}
			v.finalizeSession(s)
			return
		}
		if n == 0 {
			select {
			case <-s.stopRequestedCh:
				v.finalizeSession(s)
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}

func (v *Voice) finalizeSession(s *session) {
	// Stop is safe to invoke while a Read is blocked. The returned tail is the
	// only authoritative remainder; do not call Stop a second time.
	s.requestStop()
	result := <-s.stopCh
	if result.err != nil {
		s.addErr(result.err)
	}
	if len(result.tail) > 0 {
		if err := s.asr.Send(result.tail); err != nil {
			s.addErr(err)
		}
	}

	text := ""
	if s.failure() == nil {
		var err error
		text, err = s.asr.Finalize()
		if err != nil {
			s.addErr(err)
		}
	}

	if text != "" && s.failure() == nil {
		if err := v.output(text); err != nil {
			s.addErr(err)
		}
	}

	if err := s.asr.Close(); err != nil {
		s.addErr(err)
	}
	if err := s.recorder.Close(); err != nil {
		s.addErr(err)
	}

	failure := s.failure()
	if failure != nil {
		log.Printf("voice: session failed: %v", failure)
	}
	v.emitResult(text, failure)
}

// output delivers text and reports the first delivery error. If autotype
// fails, it attempts clipboard fallback and reports both failures when both
// paths fail.
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

func (v *Voice) clearCurrent(s *session) {
	v.mu.Lock()
	if v.current == s {
		v.current = nil
	}
	v.mu.Unlock()
}

func (v *Voice) emitResult(text string, err error) {
	if v.OnResult != nil {
		v.OnResult(text, err)
	}
}
