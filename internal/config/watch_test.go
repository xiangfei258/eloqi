package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const watcherTestTimeout = 2 * time.Second

func watcherOptions(reloads chan<- Config, failures chan<- error) WatchOptions {
	return WatchOptions{
		PollInterval: 10 * time.Millisecond,
		Debounce:     35 * time.Millisecond,
		Load: func(path string) (Config, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return Config{}, err
			}
			return Config{ASR: ASRConfig{Endpoint: string(data)}}, nil
		},
		Validate: func(cfg Config) error {
			if strings.TrimSpace(cfg.ASR.Endpoint) == "bad" {
				return errors.New("invalid test configuration")
			}
			return nil
		},
		OnReload: func(cfg Config) { reloads <- cfg },
		OnError:  func(err error) { failures <- err },
	}
}

func replaceFile(t *testing.T, path, content string) {
	t.Helper()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".watch-replacement-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		t.Fatal(err)
	}
}

func receiveReload(t *testing.T, reloads <-chan Config) Config {
	t.Helper()
	select {
	case cfg := <-reloads:
		return cfg
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for configuration reload")
		return Config{}
	}
}

func TestWatchDetectsAtomicReplacementAndDebounces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}

	reloads := make(chan Config, 4)
	failures := make(chan error, 4)
	watcher, err := Watch(path, watcherOptions(reloads, failures))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	replaceFile(t, path, "intermediate")
	time.Sleep(15 * time.Millisecond)
	replaceFile(t, path, "final")

	if got := receiveReload(t, reloads).ASR.Endpoint; got != "final" {
		t.Fatalf("reloaded content = %q, want final", got)
	}
	select {
	case cfg := <-reloads:
		t.Fatalf("debounce emitted an extra reload: %#v", cfg)
	case err := <-failures:
		t.Fatalf("unexpected watch error: %v", err)
	case <-time.After(120 * time.Millisecond):
	}
}

func TestWatchRejectsInvalidThenRecovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}

	reloads := make(chan Config, 4)
	failures := make(chan error, 4)
	watcher, err := Watch(path, watcherOptions(reloads, failures))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	replaceFile(t, path, "bad")
	select {
	case err := <-failures:
		if !strings.Contains(err.Error(), "invalid test configuration") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for validation error")
	}
	select {
	case cfg := <-reloads:
		t.Fatalf("invalid configuration was applied: %#v", cfg)
	default:
	}

	replaceFile(t, path, "recovered")
	if got := receiveReload(t, reloads).ASR.Endpoint; got != "recovered" {
		t.Fatalf("reloaded content = %q, want recovered", got)
	}
}

func TestWatchAllowsInitiallyMissingTargetAndCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	reloads := make(chan Config, 4)
	failures := make(chan error, 4)

	watcher, err := Watch(path, watcherOptions(reloads, failures))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("created"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := receiveReload(t, reloads).ASR.Endpoint; got != "created" {
		t.Fatalf("reloaded content = %q, want created", got)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}

	replaceFile(t, path, "after-close")
	select {
	case cfg := <-reloads:
		t.Fatalf("received reload after Close: %#v", cfg)
	case err := <-failures:
		t.Fatalf("received error after Close: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestWatchDefaultLoadAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	reloads := make(chan Config, 1)
	failures := make(chan error, 1)
	watcher, err := Watch(path, WatchOptions{
		PollInterval: 10 * time.Millisecond,
		Debounce:     20 * time.Millisecond,
		OnReload:     func(cfg Config) { reloads <- cfg },
		OnError:      func(err error) { failures <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	replaceFile(t, path, `
[hotkey]
mods = "Ctrl+Alt"
key = "F2"
mode = "toggle"

[asr]
endpoint = "https://asr.example.test/v1/transcriptions"
api_key = "test-key"
model = "whisper-test"

[output]
auto_type = false
`)
	select {
	case cfg := <-reloads:
		if cfg.Hotkey.Key != "F2" || cfg.Hotkey.Mode != "toggle" {
			t.Fatalf("unexpected reloaded config: %#v", cfg)
		}
	case err := <-failures:
		t.Fatalf("unexpected watch error: %v", err)
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for default reload")
	}
}

func TestWatchRejectsInvalidPath(t *testing.T) {
	if _, err := Watch("", WatchOptions{}); err == nil {
		t.Fatal("expected empty-path error")
	}
	if _, err := Watch(filepath.Join(t.TempDir(), "missing", "eloqi.toml"), WatchOptions{}); err == nil {
		t.Fatal("expected missing-directory error")
	}
}

func TestWatchParseErrorDoesNotLeakMalformedAPIKey(t *testing.T) {
	const secret = "sk-watcher-secret-sentinel"
	path := writeTempTOML(t, `[asr]
endpoint = "https://asr.example.test/v1/transcriptions"
api_key = "initial"
model = "model"
`)
	failures := make(chan error, 1)
	watcher, err := Watch(path, WatchOptions{
		PollInterval: 10 * time.Millisecond,
		Debounce:     20 * time.Millisecond,
		OnError:      func(err error) { failures <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	replaceFile(t, path, "[asr]\napi_key = \""+secret+"\n")
	select {
	case err := <-failures:
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("watch error leaked API key: %v", err)
		}
		if !strings.Contains(err.Error(), "unterminated string") {
			t.Fatalf("watch error = %v", err)
		}
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for watcher parse error")
	}
}

func TestWatchBlockedReloadCallbackDoesNotStopPolling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	secondReload := make(chan Config, 1)
	var loadCalls atomic.Int32
	options := watcherOptions(make(chan Config, 1), make(chan error, 1))
	options.Load = func(path string) (Config, error) {
		loadCalls.Add(1)
		data, err := os.ReadFile(path)
		return Config{ASR: ASRConfig{Endpoint: string(data)}}, err
	}
	options.OnReload = func(cfg Config) {
		if cfg.ASR.Endpoint == "first" {
			close(entered)
			<-release
			return
		}
		secondReload <- cfg
	}

	watcher, err := Watch(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	replaceFile(t, path, "first")
	select {
	case <-entered:
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for blocking reload callback")
	}
	replaceFile(t, path, "second")

	deadline := time.Now().Add(watcherTestTimeout)
	for loadCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := loadCalls.Load(); got < 2 {
		t.Fatalf("polling stopped behind callback: Load called %d times", got)
	}
	close(release)
	if got := receiveReload(t, secondReload).ASR.Endpoint; got != "second" {
		t.Fatalf("second reload = %q, want second", got)
	}
}

func TestWatchBlockedErrorCallbackDoesNotStopPolling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	reloads := make(chan Config, 1)
	var loadCalls atomic.Int32
	options := watcherOptions(reloads, make(chan error, 1))
	options.Load = func(path string) (Config, error) {
		loadCalls.Add(1)
		data, err := os.ReadFile(path)
		return Config{ASR: ASRConfig{Endpoint: string(data)}}, err
	}
	options.OnError = func(error) {
		close(entered)
		<-release
	}

	watcher, err := Watch(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	replaceFile(t, path, "bad")
	select {
	case <-entered:
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for blocking error callback")
	}
	replaceFile(t, path, "recovered")

	deadline := time.Now().Add(watcherTestTimeout)
	for loadCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := loadCalls.Load(); got < 2 {
		t.Fatalf("polling stopped behind error callback: Load called %d times", got)
	}
	close(release)
	if got := receiveReload(t, reloads).ASR.Endpoint; got != "recovered" {
		t.Fatalf("recovered reload = %q, want recovered", got)
	}
}

func TestWatchCloseDoesNotWaitForBlockedCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	options := watcherOptions(make(chan Config, 1), make(chan error, 1))
	options.OnReload = func(Config) {
		defer close(finished)
		close(entered)
		<-release
	}
	watcher, err := Watch(path, options)
	if err != nil {
		t.Fatal(err)
	}

	replaceFile(t, path, "changed")
	select {
	case <-entered:
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for blocking callback")
	}

	closed := make(chan error, 1)
	go func() { closed <- watcher.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(watcherTestTimeout):
		t.Fatal("Close waited for a user callback")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(watcherTestTimeout):
		t.Fatal("detached callback did not finish after release")
	}
}

func TestWatchCallbackCanCloseWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}

	ready := make(chan *Watcher, 1)
	closed := make(chan error, 1)
	options := watcherOptions(make(chan Config, 1), make(chan error, 1))
	options.OnReload = func(Config) {
		closed <- (<-ready).Close()
	}
	watcher, err := Watch(path, options)
	if err != nil {
		t.Fatal(err)
	}
	ready <- watcher
	replaceFile(t, path, "changed")

	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(watcherTestTimeout):
		t.Fatal("callback deadlocked while closing its watcher")
	}
}

func TestWatchConcurrentClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher, err := Watch(path, watcherOptions(make(chan Config, 1), make(chan error, 1)))
	if err != nil {
		t.Fatal(err)
	}

	const callers = 32
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- watcher.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
