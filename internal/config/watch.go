package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultPollInterval = 100 * time.Millisecond
	defaultDebounce     = 200 * time.Millisecond
	watchEventBuffer    = 8
)

// WatchOptions controls configuration hot reloads. The zero value uses Load,
// Config.Validate, and conservative polling/debounce intervals.
//
// Polling deliberately re-scans the containing directory and re-opens the
// configured path on every observation. It therefore continues to work when
// an editor saves by atomically replacing the file instead of modifying the
// original inode.
type WatchOptions struct {
	PollInterval time.Duration
	Debounce     time.Duration
	Load         func(path string) (Config, error)
	Validate     func(Config) error
	OnReload     func(Config)
	OnError      func(error)
}

// Watcher monitors one configuration path until Close is called.
type Watcher struct {
	stop         chan struct{}
	done         chan struct{}
	pollDone     chan struct{}
	dispatchDone chan struct{}
	events       chan watchEvent
	closeOnce    sync.Once
}

type watchEvent struct {
	config *Config
	err    error
}

// Watch starts monitoring path. The initial file is used as the baseline and
// is not emitted through OnReload; only subsequent path changes are reloaded.
// The target may be absent initially, but its containing directory must exist.
func Watch(path string, options WatchOptions) (*Watcher, error) {
	if path == "" {
		return nil, errors.New("config: watch path is empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config: resolve watch path: %w", err)
	}
	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("config: inspect watch directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("config: watch parent %s is not a directory", dir)
	}

	options = normalizeWatchOptions(options)
	initial, err := observeConfigFile(dir, base)
	if err != nil {
		return nil, fmt.Errorf("config: observe %s: %w", absPath, err)
	}

	w := &Watcher{
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		pollDone:     make(chan struct{}),
		dispatchDone: make(chan struct{}),
		events:       make(chan watchEvent, watchEventBuffer),
	}
	go w.dispatch(options)
	go w.run(absPath, dir, base, initial, options)
	go w.waitForShutdown()
	return w, nil
}

func normalizeWatchOptions(options WatchOptions) WatchOptions {
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.Debounce <= 0 {
		options.Debounce = defaultDebounce
	}
	if options.Load == nil {
		options.Load = Load
	}
	if options.Validate == nil {
		options.Validate = func(cfg Config) error { return cfg.Validate() }
	}
	return options
}

type fileObservation struct {
	exists  bool
	size    int64
	modTime int64
	digest  [sha256.Size]byte
}

func observeConfigFile(dir, base string) (fileObservation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fileObservation{}, err
	}
	for _, entry := range entries {
		if entry.Name() != base {
			continue
		}
		if entry.IsDir() {
			return fileObservation{}, fmt.Errorf("%s is a directory", filepath.Join(dir, base))
		}
		info, err := entry.Info()
		if err != nil {
			return fileObservation{}, err
		}
		content, err := os.ReadFile(filepath.Join(dir, base))
		if err != nil {
			return fileObservation{}, err
		}
		return fileObservation{
			exists:  true,
			size:    info.Size(),
			modTime: info.ModTime().UnixNano(),
			digest:  sha256.Sum256(content),
		}, nil
	}
	return fileObservation{}, nil
}

func (w *Watcher) run(path, dir, base string, observed fileObservation, options WatchOptions) {
	defer close(w.pollDone)
	ticker := time.NewTicker(options.PollInterval)
	defer ticker.Stop()

	var (
		pending       bool
		reloadAfter   time.Time
		lastScanError string
	)
	for {
		// Prefer shutdown over another tick when both are ready. This prevents a
		// callback from being queued after Close has started.
		select {
		case <-w.stop:
			return
		default:
		}
		select {
		case <-w.stop:
			return
		case now := <-ticker.C:
			current, err := observeConfigFile(dir, base)
			if err != nil {
				message := err.Error()
				if message != lastScanError {
					lastScanError = message
					w.enqueue(watchEvent{err: fmt.Errorf("config: observe %s: %w", path, err)})
				}
				continue
			}
			lastScanError = ""

			if current != observed {
				observed = current
				pending = true
				reloadAfter = now.Add(options.Debounce)
			}
			if !pending || now.Before(reloadAfter) {
				continue
			}

			pending = false
			cfg, err := options.Load(path)
			if err != nil {
				w.enqueue(watchEvent{err: fmt.Errorf("config: reload %s: %w", path, err)})
				continue
			}
			if err := options.Validate(cfg); err != nil {
				w.enqueue(watchEvent{err: fmt.Errorf("config: reject reloaded %s: %w", path, err)})
				continue
			}
			w.enqueue(watchEvent{config: &cfg})
		}
	}
}

// enqueue never waits for user callbacks. Events are delivered in order while
// capacity is available; under sustained callback backpressure the oldest
// pending event is discarded so the queue remains bounded and converges on the
// latest observed configuration state.
func (w *Watcher) enqueue(event watchEvent) {
	select {
	case <-w.stop:
		return
	default:
	}
	select {
	case w.events <- event:
		return
	default:
	}

	// The polling goroutine is the only sender, so removing one stale event
	// before retrying is safe. The dispatcher may win the receive concurrently;
	// in that case the non-blocking retry below still preserves boundedness.
	select {
	case <-w.events:
	default:
	}
	select {
	case w.events <- event:
	case <-w.stop:
	default:
	}
}

func (w *Watcher) dispatch(options WatchOptions) {
	defer close(w.dispatchDone)
	for {
		select {
		case <-w.stop:
			return
		default:
		}

		var event watchEvent
		select {
		case <-w.stop:
			return
		case queued, ok := <-w.events:
			if !ok {
				return
			}
			event = queued
		}

		callbackDone := make(chan struct{})
		go func() {
			defer close(callbackDone)
			switch {
			case event.err != nil && options.OnError != nil:
				options.OnError(event.err)
			case event.config != nil && options.OnReload != nil:
				options.OnReload(*event.config)
			}
		}()

		// User callbacks cannot stall polling or watcher shutdown. At most one
		// callback is active at a time, which keeps normal delivery ordered. If a
		// callback blocks forever, Close detaches it after all internal watcher
		// goroutines have stopped; Go cannot forcibly cancel arbitrary callbacks.
		select {
		case <-callbackDone:
		case <-w.stop:
			return
		}
	}
}

func (w *Watcher) waitForShutdown() {
	<-w.pollDone
	close(w.events)
	<-w.dispatchDone
	close(w.done)
}

// Close stops the watcher and waits for all internal watcher goroutines to
// exit. It is safe to call Close concurrently, more than once, and from inside
// OnReload or OnError. A user callback that blocks forever is detached so it
// cannot prevent shutdown.
func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() { close(w.stop) })
	<-w.done
	return nil
}
