// Package stats persists aggregate voice-input usage statistics.
package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const stateDirectoryEnv = "ELOQUI_STATE_DIR"

// Snapshot is the durable statistics schema. Durations are stored as
// milliseconds so the JSON file remains portable and human-readable.
type Snapshot struct {
	Recordings      uint64    `json:"recordings"`
	TotalCharacters uint64    `json:"total_characters"`
	TotalDurationMS int64     `json:"total_duration_ms"`
	LastCharacters  uint64    `json:"last_characters"`
	LastDurationMS  int64     `json:"last_duration_ms"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Store serializes updates and atomically replaces the state file after every
// successful recording.
type Store struct {
	mu   sync.Mutex
	path string
	data Snapshot
	now  func() time.Time
}

// DefaultPath returns the platform-appropriate durable statistics path.
// ELOQUI_STATE_DIR overrides the parent directory, which is useful for
// portable installations and tests.
func DefaultPath() (string, error) {
	return defaultPath(runtime.GOOS, os.Getenv, os.UserHomeDir, os.UserConfigDir)
}

func defaultPath(
	goos string,
	getenv func(string) string,
	userHomeDir func() (string, error),
	userConfigDir func() (string, error),
) (string, error) {
	if dir := strings.TrimSpace(getenv(stateDirectoryEnv)); dir != "" {
		return filepath.Join(dir, "stats.json"), nil
	}
	if goos == "linux" {
		if dir := strings.TrimSpace(getenv("XDG_STATE_HOME")); dir != "" {
			if !filepath.IsAbs(dir) {
				return "", fmt.Errorf("stats: XDG_STATE_HOME must be an absolute path")
			}
			return filepath.Join(dir, "eloqi", "stats.json"), nil
		}
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("stats: locate home directory: %w", err)
		}
		return filepath.Join(home, ".local", "state", "eloqi", "stats.json"), nil
	}
	configDir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("stats: locate application data directory: %w", err)
	}
	return filepath.Join(configDir, "eloqi", "stats.json"), nil
}

// Open loads path. A missing file starts with an empty Snapshot.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("stats: path is required")
	}
	s := &Store{path: path, now: time.Now}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("stats: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("stats: decode %s: %w", path, err)
	}
	return s, nil
}

// Record adds one completed recording and persists the resulting snapshot.
// If persistence fails, the in-memory value is rolled back.
func (s *Store) Record(text string, duration time.Duration) error {
	if duration < 0 {
		return fmt.Errorf("stats: duration must not be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := s.data
	characters := uint64(utf8.RuneCountInString(text))
	s.data.Recordings++
	s.data.TotalCharacters += characters
	s.data.TotalDurationMS += duration.Milliseconds()
	s.data.LastCharacters = characters
	s.data.LastDurationMS = duration.Milliseconds()
	s.data.UpdatedAt = s.now().UTC()
	if err := persist(s.path, s.data); err != nil {
		s.data = previous
		return err
	}
	return nil
}

// Snapshot returns a consistent copy of the current counters.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

func persist(path string, data Snapshot) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("stats: create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".eloqi-stats-*.tmp")
	if err != nil {
		return fmt.Errorf("stats: create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("stats: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("stats: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("stats: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("stats: replace %s: %w", path, err)
	}
	cleanup = false
	return nil
}
