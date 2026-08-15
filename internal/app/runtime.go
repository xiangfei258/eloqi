package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xiangchang24/eloqi/internal/config"
	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/voice"
)

type statisticsRecorder interface {
	Record(text string, duration time.Duration) error
}

type overlaySink interface {
	StateChanged(voice.State)
	ShowError(error)
	Close() error
}

type runtimeDependencies struct {
	newHotkey   func() (platform.Hotkey, error)
	newRecorder voice.RecorderFactory
	newASR      func(config.Config) platform.ASRClient
	clipboard   platform.Clipboard
	autotype    platform.Autotype
	statistics  statisticsRecorder
	overlay     overlaySink
	logger      *slog.Logger
}

// voiceGeneration owns one Voice instance and the hotkey provider consumed by
// its event loop. A provider is never shared across generations: platform
// backends may still have stale edges queued while Unregister/Close completes.
type voiceGeneration struct {
	voice  *voice.Voice
	hotkey platform.Hotkey
}

// stopAndClose preserves the only safe teardown order for a generation. If a
// provider reports a close failure it is retained so Close can be retried.
func (g *voiceGeneration) stopAndClose() error {
	if g == nil {
		return nil
	}
	if g.voice != nil {
		g.voice.Stop()
		g.voice = nil
	}
	if g.hotkey == nil {
		return nil
	}
	if err := g.hotkey.Close(); err != nil {
		return err
	}
	g.hotkey = nil
	return nil
}

// voiceRuntime owns the currently configured generation. Reload always
// retires the old Voice and hotkey provider before constructing a fresh pair,
// and rollback also uses a fresh provider.
type voiceRuntime struct {
	mu          sync.Mutex
	deps        runtimeDependencies
	generation  *voiceGeneration
	config      config.Config
	closed      bool
	overlayDone bool
}

func newVoiceRuntime(deps runtimeDependencies) (*voiceRuntime, error) {
	if deps.newHotkey == nil {
		return nil, errors.New("app runtime: hotkey factory is required")
	}
	if deps.newRecorder == nil || deps.newASR == nil {
		return nil, errors.New("app runtime: recorder and ASR factories are required")
	}
	if deps.logger == nil {
		deps.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &voiceRuntime{deps: deps}, nil
}

func (r *voiceRuntime) Start(cfg config.Config) error {
	cfg, err := prepareRuntimeConfig(cfg)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("app runtime: closed")
	}
	if r.generation != nil {
		return errors.New("app runtime: already started")
	}
	next, err := r.startGeneration(cfg)
	if err != nil {
		// A failed provider Close is retained for a later Close retry. No
		// subsequent generation may be started while it remains live.
		if next != nil {
			r.generation = next
		}
		return err
	}
	r.generation = next
	r.config = cfg
	return nil
}

func (r *voiceRuntime) Reload(cfg config.Config) error {
	cfg, err := prepareRuntimeConfig(cfg)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("app runtime: closed")
	}
	if r.generation != nil && r.generation.voice == nil {
		if err := r.generation.stopAndClose(); err != nil {
			return fmt.Errorf("app runtime: finish retiring hotkey generation: %w", err)
		}
		r.generation = nil
	}
	if r.generation == nil {
		next, err := r.startGeneration(cfg)
		if err != nil {
			if next != nil {
				r.generation = next
			}
			return err
		}
		r.generation, r.config = next, cfg
		if r.deps.overlay != nil {
			r.deps.overlay.StateChanged(voice.StateIdle)
		}
		return nil
	}
	if reflect.DeepEqual(cfg, r.config) {
		if r.deps.overlay != nil {
			r.deps.overlay.StateChanged(voice.StateIdle)
		}
		return nil
	}

	previousConfig := r.config
	if err := r.generation.stopAndClose(); err != nil {
		return fmt.Errorf("app runtime: retire previous hotkey generation: %w", err)
	}
	r.generation = nil

	next, applyErr := r.startGeneration(cfg)
	if applyErr == nil {
		r.generation, r.config = next, cfg
		if r.deps.overlay != nil {
			r.deps.overlay.StateChanged(voice.StateIdle)
		}
		r.deps.logger.Info("configuration reloaded")
		return nil
	}
	if next != nil {
		// The candidate provider could not be closed. Retain it for Close and
		// do not start a rollback provider alongside a possibly live backend.
		r.generation = next
		return fmt.Errorf("app runtime: apply reloaded configuration: %w", applyErr)
	}

	rollback, rollbackErr := r.startGeneration(previousConfig)
	if rollbackErr == nil {
		r.generation, r.config = rollback, previousConfig
		return fmt.Errorf("app runtime: apply reloaded configuration (previous configuration restored): %w", applyErr)
	}
	if rollback != nil {
		r.generation = rollback
	}
	return errors.Join(
		fmt.Errorf("app runtime: apply reloaded configuration: %w", applyErr),
		fmt.Errorf("app runtime: restore previous configuration: %w", rollbackErr),
	)
}

func prepareRuntimeConfig(cfg config.Config) (config.Config, error) {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("app runtime: invalid configuration: %w", err)
	}
	return cfg, nil
}

func (r *voiceRuntime) startGeneration(cfg config.Config) (*voiceGeneration, error) {
	key, err := config.ParseHotkey(cfg.Hotkey)
	if err != nil {
		return nil, fmt.Errorf("app runtime: parse hotkey: %w", err)
	}
	hotkey, err := r.deps.newHotkey()
	if err != nil {
		return nil, fmt.Errorf("app runtime: create hotkey provider: %w", err)
	}
	if hotkey == nil {
		return nil, errors.New("app runtime: hotkey factory returned nil")
	}
	asrConfig := cfg
	v := voice.New(voice.Config{
		Hotkey:       hotkey,
		NewRecorder:  r.deps.newRecorder,
		NewASR:       func() platform.ASRClient { return r.deps.newASR(asrConfig) },
		Clipboard:    r.deps.clipboard,
		Autotype:     r.deps.autotype,
		Key:          key,
		Mode:         cfg.Hotkey.Mode,
		AutoType:     cfg.Output.AutoType,
		StopDelay:    time.Duration(cfg.Hotkey.StopDelayMS) * time.Millisecond,
		StopDelaySet: true,
	})
	v.OnStateChange = r.handleStateChange
	v.OnSession = r.handleSession
	generation := &voiceGeneration{voice: v, hotkey: hotkey}
	if err := v.Start(); err != nil {
		startErr := fmt.Errorf("app runtime: start voice: %w", err)
		if closeErr := generation.stopAndClose(); closeErr != nil {
			return generation, errors.Join(startErr, fmt.Errorf("app runtime: clean up failed hotkey generation: %w", closeErr))
		}
		return nil, startErr
	}
	return generation, nil
}

func (r *voiceRuntime) handleStateChange(state voice.State) {
	if r.deps.overlay == nil || state == voice.StateError {
		// Session completion owns the error event so the detailed message cannot
		// race with and be overwritten by an empty StateError notification.
		return
	}
	r.deps.overlay.StateChanged(state)
}

func (r *voiceRuntime) handleSession(result voice.SessionResult) {
	if result.Cancelled {
		r.deps.logger.Debug("voice session cancelled", "session", result.SessionID, "duration", result.Duration)
		return
	}
	if result.Err != nil {
		r.deps.logger.Error("voice session failed", "session", result.SessionID, "error", result.Err)
		if r.deps.overlay != nil {
			r.deps.overlay.ShowError(result.Err)
		}
		return
	}
	if r.deps.statistics != nil {
		if err := r.deps.statistics.Record(result.Text, result.Duration); err != nil {
			r.deps.logger.Error("persist voice statistics", "session", result.SessionID, "error", err)
		}
	}
	r.deps.logger.Info(
		"voice session completed",
		"session", result.SessionID,
		"characters", utf8.RuneCountInString(result.Text),
		"duration", result.Duration,
	)
}

func (r *voiceRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed && r.generation == nil && (r.deps.overlay == nil || r.overlayDone) {
		return nil
	}
	r.closed = true
	var shutdownErr error
	if r.generation != nil {
		shutdownErr = r.generation.stopAndClose()
		if shutdownErr == nil {
			r.generation = nil
		}
	}
	if r.deps.overlay != nil && !r.overlayDone {
		shutdownErr = errors.Join(shutdownErr, r.deps.overlay.Close())
		r.overlayDone = true
	}
	return shutdownErr
}
