// Package logging creates structured loggers whose destination respects
// Eloqui's interactive terminal mode.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Options configures a logging session.
type Options struct {
	// TUI routes all logs to a file so terminal rendering is never polluted.
	TUI bool
	// Debug enables debug-level records. Otherwise the minimum level is info.
	Debug bool
	// FilePath is used in TUI mode. An empty path selects DefaultPath().
	FilePath string
	// Terminal is used outside TUI mode. It defaults to os.Stderr.
	Terminal io.Writer
}

// Session embeds the configured slog.Logger and owns any opened log file.
type Session struct {
	*slog.Logger

	path      string
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

// New creates a structured JSON logger. TUI mode fails closed when its log
// file cannot be opened; it never falls back to writing into the terminal.
func New(options Options) (*Session, error) {
	level := slog.LevelInfo
	if options.Debug {
		level = slog.LevelDebug
	}
	handlerOptions := &slog.HandlerOptions{
		AddSource: options.Debug,
		Level:     level,
	}

	if !options.TUI {
		destination := options.Terminal
		if destination == nil {
			destination = os.Stderr
		}
		return &Session{Logger: slog.New(slog.NewJSONHandler(destination, handlerOptions))}, nil
	}

	path := options.FilePath
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("logging: resolve path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0700); err != nil {
		return nil, fmt.Errorf("logging: create directory for %s: %w", absPath, err)
	}
	file, err := os.OpenFile(absPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("logging: open %s: %w", absPath, err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("logging: secure %s: %w", absPath, err)
	}

	return &Session{
		Logger: slog.New(slog.NewJSONHandler(file, handlerOptions)),
		path:   absPath,
		file:   file,
	}, nil
}

// DefaultPath returns the per-user cache path used for TUI logs.
func DefaultPath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("logging: locate user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "eloqi", "eloqi.log"), nil
}

// Path returns the absolute log file path in TUI mode and an empty string in
// terminal mode.
func (s *Session) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close flushes and closes a TUI log file. It is safe to call more than once.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.file == nil {
			return
		}
		s.closeErr = errors.Join(s.file.Sync(), s.file.Close())
	})
	return s.closeErr
}
