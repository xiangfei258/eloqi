package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/config"
	"github.com/xiangchang24/eloqi/internal/doctor"
	"github.com/xiangchang24/eloqi/internal/instance"
	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/mock"
)

type fakeConfigWatcher struct {
	mu     sync.Mutex
	closed bool
}

func (w *fakeConfigWatcher) Close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return nil
}

func (w *fakeConfigWatcher) Closed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

type fakeStatistics struct {
	mu      sync.Mutex
	records int
}

type fakeInstanceLock struct {
	mu     sync.Mutex
	closed bool
}

func (l *fakeInstanceLock) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return nil
}

func (l *fakeInstanceLock) Closed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (s *fakeStatistics) Record(string, time.Duration) error {
	s.mu.Lock()
	s.records++
	s.mu.Unlock()
	return nil
}

func TestRunDoctorReportsActionableFailureWithoutInitializingPlatform(t *testing.T) {
	var stdout, stderr bytes.Buffer
	platformCalls := 0
	services := applicationServices{
		checkEnvironment: func(doctor.Options) doctor.Report {
			return doctor.Report{Findings: []doctor.Finding{{
				ID:       "audio.arecord",
				Status:   doctor.StatusError,
				Required: true,
				Message:  "arecord was not found in PATH",
				Hint:     "Install alsa-utils.",
			}}}
		},
		newCapabilities: func() (*capabilities, error) {
			platformCalls++
			return nil, nil
		},
	}

	code := run([]string{"--doctor", "--config", filepath.Join(t.TempDir(), "missing.toml")}, strings.NewReader(""), &stdout, &stderr, services)
	if code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if platformCalls != 0 {
		t.Fatalf("platform initialized %d times in doctor mode", platformCalls)
	}
	if output := stderr.String(); !strings.Contains(output, "arecord") || !strings.Contains(output, "Install alsa-utils") {
		t.Fatalf("doctor output is not actionable: %q", output)
	}
}

func TestRunDaemonWiresHotReloadAndOrderlyShutdown(t *testing.T) {
	configPath := writeValidConfig(t, false)
	var hotkeysMu sync.Mutex
	var hotkeys []*mock.Hotkey
	watcher := &fakeConfigWatcher{}
	statistics := &fakeStatistics{}
	instanceLock := &fakeInstanceLock{}
	var watchOptions config.WatchOptions
	var stderr bytes.Buffer

	services := applicationServices{
		checkEnvironment: func(doctor.Options) doctor.Report { return doctor.Report{} },
		newCapabilities: func() (*capabilities, error) {
			return &capabilities{
				newHotkey: func() (platform.Hotkey, error) {
					hotkey := mock.NewHotkey()
					hotkeysMu.Lock()
					hotkeys = append(hotkeys, hotkey)
					hotkeysMu.Unlock()
					return hotkey, nil
				},
				newRecorder: func() platform.Recorder { return &mock.Recorder{} },
				clipboard:   &mock.Clipboard{},
			}, nil
		},
		watchConfig: func(path string, options config.WatchOptions) (configWatcher, error) {
			if path != configPath {
				t.Fatalf("watch path = %q, want %q", path, configPath)
			}
			watchOptions = options
			return watcher, nil
		},
		statisticsPath:  func() (string, error) { return filepath.Join(t.TempDir(), "stats.json"), nil },
		openStatistics:  func(string) (statisticsRecorder, error) { return statistics, nil },
		acquireInstance: func() (io.Closer, error) { return instanceLock, nil },
		waitForTermination: func() {
			if watchOptions.OnReload == nil {
				t.Fatal("watcher did not receive an OnReload callback")
			}
			next, err := config.Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			next.Hotkey.StopDelayMS = 0
			watchOptions.OnReload(next)
		},
	}

	code := run([]string{"--config", configPath, "--debug"}, strings.NewReader(""), &bytes.Buffer{}, &stderr, services)
	if code != 0 {
		t.Fatalf("run() = %d, stderr=%s", code, stderr.String())
	}
	if !watcher.Closed() {
		t.Fatal("configuration watcher was not closed")
	}
	hotkeysMu.Lock()
	defer hotkeysMu.Unlock()
	if len(hotkeys) != 2 {
		t.Fatalf("hotkey generations = %d, want 2", len(hotkeys))
	}
	for i, hotkey := range hotkeys {
		if !hotkey.Closed() {
			t.Fatalf("platform hotkey generation %d was not closed", i+1)
		}
	}
	if !instanceLock.Closed() {
		t.Fatal("single-instance lock was not released")
	}
	if strings.Contains(stderr.String(), "test-secret") {
		t.Fatalf("runtime log leaked API key: %s", stderr.String())
	}
}

func TestRunRejectsSecondDaemonBeforeOpeningStatisticsOrPlatform(t *testing.T) {
	configPath := writeValidConfig(t, false)
	var stderr bytes.Buffer
	statisticsCalls := 0
	platformCalls := 0
	services := applicationServices{
		checkEnvironment: func(doctor.Options) doctor.Report { return doctor.Report{} },
		acquireInstance:  func() (io.Closer, error) { return nil, instance.ErrAlreadyRunning },
		statisticsPath: func() (string, error) {
			statisticsCalls++
			return "", nil
		},
		newCapabilities: func() (*capabilities, error) {
			platformCalls++
			return nil, nil
		},
	}
	if code := run([]string{"--config", configPath}, strings.NewReader(""), &bytes.Buffer{}, &stderr, services); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if statisticsCalls != 0 || platformCalls != 0 {
		t.Fatalf("second instance initialized statistics=%d platform=%d", statisticsCalls, platformCalls)
	}
	if !strings.Contains(stderr.String(), "already running") {
		t.Fatalf("second-instance error is not clear: %q", stderr.String())
	}
}

func TestRunTUIUsesFileLoggingAndDoesNotEchoSecret(t *testing.T) {
	configPath := writeValidConfig(t, false)
	logPath := filepath.Join(t.TempDir(), "logs", "eloqi.log")
	input := strings.NewReader(strings.Repeat("\n", 11))
	var stdout, stderr bytes.Buffer

	code := run(
		[]string{"--tui", "--config", configPath, "--log-file", logPath},
		input,
		&stdout,
		&stderr,
		applicationServices{},
	)
	if code != 0 {
		t.Fatalf("run() = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "test-secret") {
		t.Fatalf("TUI leaked API key: %q", combined)
	}
	logContents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logContents), "configuration saved") {
		t.Fatalf("TUI log missing save event: %s", logContents)
	}
}

func TestRunRejectsConflictingModesAndUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{{"--tui", "--doctor"}, {"unexpected"}} {
		var stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &bytes.Buffer{}, &stderr, applicationServices{}); code != 2 {
			t.Fatalf("run(%v) = %d, want 2", args, code)
		}
	}
}

func writeValidConfig(t *testing.T, autoType bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eloqi.toml")
	text := `[hotkey]
mods = "Ctrl+Alt"
key = "F1"
mode = "hold"
stop_delay_ms = 800

[asr]
engine = "openai-compatible"
endpoint = "https://asr.example.test/v1/audio/transcriptions"
api_key = "test-secret"
model = "test-model"

[output]
auto_type = ` + map[bool]string{true: "true", false: "false"}[autoType] + "\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
