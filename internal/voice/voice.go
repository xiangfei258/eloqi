// Package voice implements the core recording-to-text flow: it listens for
// hotkey events, drives the recorder and ASR client, and delivers the
// recognized text to the clipboard or autotype.
//
// This is the P1 minimal version. The explicit state machine, stop-delay
// buffer and double-output deduplication are added in P2.
package voice

import (
	"io"
	"log"
	"sync"
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

// session represents one recording cycle.
type session struct {
	recorder platform.Recorder
	asr      platform.ASRClient
	cancel   chan struct{}
	done     chan struct{}
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

	// OnResult is called (from a session goroutine) when a session finishes
	// with a result or an error. It is optional and intended for
	// observability and tests.
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

// Start registers the hotkey and begins listening for events. It returns an
// error if the hotkey cannot be registered.
func (v *Voice) Start() error {
	if err := v.hotkey.Register(v.key); err != nil {
		return err
	}
	go v.eventLoop()
	return nil
}

// Stop unregisters the hotkey and cancels any active session.
func (v *Voice) Stop() {
	v.mu.Lock()
	if v.stopped {
		v.mu.Unlock()
		return
	}
	v.stopped = true
	cur := v.current
	v.mu.Unlock()

	v.hotkey.Unregister(v.key)
	if cur != nil {
		safeClose(cur.cancel)
		<-cur.done
	}
}

// eventLoop consumes hotkey events for the lifetime of the Voice.
func (v *Voice) eventLoop() {
	for ev := range v.hotkey.Events() {
		if ev.Pressed {
			v.onPress()
		} else {
			v.onRelease()
		}
	}
}

// onPress handles a hotkey press edge.
func (v *Voice) onPress() {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.mode == "toggle" && v.current != nil {
		// Second press in toggle mode: stop the current session.
		safeClose(v.current.cancel)
		v.current = nil
		return
	}
	if v.current != nil {
		return // already recording; ignore
	}

	v.current = v.startSession()
}

// onRelease handles a hotkey release edge (hold mode only).
func (v *Voice) onRelease() {
	if v.mode != "hold" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.current != nil {
		safeClose(v.current.cancel)
		v.current = nil
	}
}

// startSession creates a new recorder + ASR client, starts them, and launches
// the session goroutine. The caller must hold v.mu.
func (v *Voice) startSession() *session {
	rec := v.newRecorder()
	asr := v.newASR()

	if err := rec.Start(); err != nil {
		log.Printf("voice: recorder start: %v", err)
		v.emitResult("", err)
		return nil
	}
	if err := asr.Connect(); err != nil {
		log.Printf("voice: asr connect: %v", err)
		rec.Close()
		v.emitResult("", err)
		return nil
	}

	s := &session{
		recorder: rec,
		asr:      asr,
		cancel:   make(chan struct{}),
		done:     make(chan struct{}),
	}
	go v.runSession(s)
	return s
}

// runSession streams audio from the recorder to the ASR client until the
// session is cancelled, then stops the recorder, finalizes the ASR, and
// outputs the result.
func (v *Voice) runSession(s *session) {
	defer close(s.done)

	buf := make([]byte, 4096)
	for {
		select {
		case <-s.cancel:
			goto finalize
		default:
		}

		n, err := s.recorder.Read(buf)
		if n > 0 {
			if sendErr := s.asr.Send(buf[:n]); sendErr != nil {
				log.Printf("voice: asr send: %v", sendErr)
				goto finalize
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("voice: recorder read: %v", err)
			}
			goto finalize
		}
		if n == 0 {
			// No data ready (e.g. mock recorder): avoid busy-loop.
			select {
			case <-s.cancel:
				goto finalize
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

finalize:
	v.finalizeSession(s)
}

// finalizeSession stops the recorder, flushes remaining audio, gets the final
// text, and outputs it.
func (v *Voice) finalizeSession(s *session) {
	tail, err := s.recorder.Stop()
	if err != nil {
		log.Printf("voice: recorder stop: %v", err)
	}
	if len(tail) > 0 {
		if sendErr := s.asr.Send(tail); sendErr != nil {
			log.Printf("voice: asr send tail: %v", sendErr)
		}
	}

	text, err := s.asr.Finalize()
	s.asr.Close()
	s.recorder.Close()

	if err != nil {
		log.Printf("voice: asr finalize: %v", err)
		v.emitResult("", err)
		return
	}

	if text != "" {
		v.output(text)
	}
	v.emitResult(text, nil)
}

// output delivers text to the clipboard and/or autotype.
func (v *Voice) output(text string) {
	if v.autoType && v.autotype != nil {
		if err := v.autotype.Type(text); err != nil {
			log.Printf("voice: autotype: %v", err)
			// Fall back to clipboard.
			if v.clipboard != nil {
				_ = v.clipboard.Write(text)
			}
		}
		return
	}
	if v.clipboard != nil {
		if err := v.clipboard.Write(text); err != nil {
			log.Printf("voice: clipboard write: %v", err)
		}
	}
}

// emitResult calls the OnResult callback if set.
func (v *Voice) emitResult(text string, err error) {
	if v.OnResult != nil {
		v.OnResult(text, err)
	}
}

// safeClose closes a channel once, ignoring the panic if already closed.
func safeClose(ch chan struct{}) {
	defer func() { recover() }()
	close(ch)
}
