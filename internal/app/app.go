// Package app contains Eloqui's cross-platform command-line entry point and
// runtime composition root.
package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/xiangchang24/eloqi/internal/asr"
	"github.com/xiangchang24/eloqi/internal/config"
	"github.com/xiangchang24/eloqi/internal/doctor"
	"github.com/xiangchang24/eloqi/internal/instance"
	"github.com/xiangchang24/eloqi/internal/logging"
	statusoverlay "github.com/xiangchang24/eloqi/internal/overlay"
	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/stats"
	"github.com/xiangchang24/eloqi/internal/tui"
	"github.com/xiangchang24/eloqi/internal/voice"
)

// capabilities bundles the platform-specific implementations created by
// newCapabilities.
type capabilities struct {
	newHotkey   func() (platform.Hotkey, error)
	newRecorder voice.RecorderFactory
	clipboard   platform.Clipboard
	autotype    platform.Autotype // optional when output.auto_type is false
	overlay     platform.Overlay  // optional; voice input remains usable without it
	warnings    []error
}

type configWatcher interface {
	Close() error
}

// applicationServices isolates host lifecycle operations so the complete
// composition root can be exercised without real desktop devices.
type applicationServices struct {
	checkEnvironment   func(doctor.Options) doctor.Report
	newCapabilities    func() (*capabilities, error)
	watchConfig        func(string, config.WatchOptions) (configWatcher, error)
	statisticsPath     func() (string, error)
	openStatistics     func(string) (statisticsRecorder, error)
	acquireInstance    func() (io.Closer, error)
	waitForTermination func()
}

func defaultApplicationServices() applicationServices {
	return applicationServices{
		checkEnvironment: doctor.Check,
		newCapabilities:  newCapabilities,
		watchConfig: func(path string, options config.WatchOptions) (configWatcher, error) {
			return config.Watch(path, options)
		},
		statisticsPath: stats.DefaultPath,
		openStatistics: func(path string) (statisticsRecorder, error) {
			return stats.Open(path)
		},
		acquireInstance:    func() (io.Closer, error) { return instance.Acquire() },
		waitForTermination: waitForTerminationSignal,
	}
}

// Run loads configuration, wires the product features, and blocks until a
// termination signal is received.
func Run() int {
	services := defaultApplicationServices()
	return run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, services)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, services applicationServices) int {
	flags := flag.NewFlagSet("eloqi", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", defaultConfigPath(), "path to configuration file")
	tuiMode := flags.Bool("tui", false, "edit configuration in the terminal and exit")
	doctorMode := flags.Bool("doctor", false, "check runtime dependencies and exit")
	debug := flags.Bool("debug", false, "enable debug logging")
	logFile := flags.String("log-file", "", "TUI log file path (default: user cache directory)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "eloqi: unexpected arguments: %v\n", flags.Args())
		return 2
	}
	if *tuiMode && *doctorMode {
		_, _ = fmt.Fprintln(stderr, "eloqi: --tui and --doctor cannot be used together")
		return 2
	}
	if *tuiMode {
		return runTUI(*configPath, *debug, *logFile, stdin, stdout, stderr)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "eloqi: load config: %v\n", err)
		return 1
	}
	report := services.checkEnvironment(doctor.Options{RequireAutoType: cfg.Output.AutoType})
	if *doctorMode {
		if _, err := report.WriteTo(stderr); err != nil {
			_, _ = fmt.Fprintf(stderr, "eloqi: write doctor report: %v\n", err)
			return 1
		}
		if report.OK() {
			return 0
		}
		return 1
	}
	if err := cfg.Validate(); err != nil {
		_, _ = fmt.Fprintf(stderr, "eloqi: invalid config: %v\n", err)
		return 1
	}
	if _, err := report.WriteTo(stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "eloqi: write doctor report: %v\n", err)
		return 1
	}
	if !report.OK() {
		_, _ = fmt.Fprintf(stderr, "eloqi: environment check failed: %v\n", report.Error())
		return 1
	}

	logSession, err := logging.New(logging.Options{Debug: *debug, Terminal: stderr})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "eloqi: initialize logging: %v\n", err)
		return 1
	}
	defer func() { _ = logSession.Close() }()

	instanceLock, err := services.acquireInstance()
	if err != nil {
		logSession.Error("acquire single-instance lock", "error", err)
		return 1
	}
	defer func() { _ = instanceLock.Close() }()

	statisticsPath, err := services.statisticsPath()
	if err != nil {
		logSession.Error("locate statistics file", "error", err)
		return 1
	}
	statistics, err := services.openStatistics(statisticsPath)
	if err != nil {
		logSession.Error("open statistics file", "path", statisticsPath, "error", err)
		return 1
	}

	caps, err := services.newCapabilities()
	if err != nil {
		logSession.Error("initialize platform", "error", err)
		return 1
	}
	for _, warning := range caps.warnings {
		logSession.Warn("optional platform capability unavailable", "error", warning)
	}
	if cfg.Output.AutoType && caps.autotype == nil {
		logSession.Error("automatic typing is configured but unavailable", "hint", "set output.auto_type=false or run --doctor")
		return 1
	}

	var overlay overlaySink
	if caps.overlay != nil {
		controller, err := statusoverlay.New(statusoverlay.Config{
			Backend: caps.overlay,
			OnError: func(err error) {
				logSession.Warn("status overlay", "error", err)
			},
		})
		if err != nil {
			logSession.Error("initialize status overlay", "error", err)
			return 1
		}
		overlay = controller
	}

	runtime, err := newVoiceRuntime(runtimeDependencies{
		newHotkey:   caps.newHotkey,
		newRecorder: caps.newRecorder,
		newASR: func(cfg config.Config) platform.ASRClient {
			return asr.NewOpenAIClient(asrConfigFromConfig(cfg))
		},
		clipboard:  caps.clipboard,
		autotype:   caps.autotype,
		statistics: statistics,
		overlay:    overlay,
		logger:     logSession.Logger,
	})
	if err != nil {
		if overlay != nil {
			_ = overlay.Close()
		}
		logSession.Error("initialize voice runtime", "error", err)
		return 1
	}
	if err := runtime.Start(cfg); err != nil {
		_ = runtime.Close()
		logSession.Error("start voice runtime", "error", err)
		return 1
	}

	watcher, err := services.watchConfig(*configPath, config.WatchOptions{
		OnReload: func(next config.Config) {
			if err := runtime.Reload(next); err != nil {
				logSession.Error("reload configuration", "error", err)
				if overlay != nil {
					overlay.ShowError(err)
				}
			}
		},
		OnError: func(err error) {
			logSession.Error("watch configuration", "error", err)
			if overlay != nil {
				overlay.ShowError(err)
			}
		},
	})
	if err != nil {
		_ = runtime.Close()
		logSession.Error("start configuration watcher", "path", *configPath, "error", err)
		return 1
	}

	key, _ := config.ParseHotkey(cfg.Hotkey)
	logSession.Info(
		"Eloqui ready",
		"hotkey", key.String(),
		"mode", cfg.Hotkey.Mode,
		"auto_type", cfg.Output.AutoType,
		"statistics", statisticsPath,
	)
	services.waitForTermination()
	logSession.Info("shutting down")

	shutdownErr := errors.Join(watcher.Close(), runtime.Close(), instanceLock.Close())
	if shutdownErr != nil {
		logSession.Error("shutdown", "error", shutdownErr)
		return 1
	}
	return 0
}

func runTUI(configPath string, debug bool, logFile string, stdin io.Reader, stdout, stderr io.Writer) int {
	logSession, err := logging.New(logging.Options{TUI: true, Debug: debug, FilePath: logFile})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "eloqi: initialize TUI logging: %v\n", err)
		return 1
	}
	defer func() { _ = logSession.Close() }()

	editor := tui.NewEditor(stdin, stdout, tui.FileStore{Path: configPath})
	_, err = editor.Run()
	if errors.Is(err, tui.ErrCancelled) {
		logSession.Info("configuration editing cancelled")
		_, _ = fmt.Fprintln(stdout, "Configuration unchanged.")
		return 0
	}
	if err != nil {
		logSession.Error("configuration editor failed", "error", err)
		_, _ = fmt.Fprintf(stderr, "eloqi: configuration editor: %v (log: %s)\n", err, logSession.Path())
		return 1
	}
	logSession.Info("configuration saved", "path", configPath)
	return 0
}

// asrConfigFromConfig converts the user-facing configuration into the ASR
// client's constructor options.
func asrConfigFromConfig(cfg config.Config) asr.OpenAIClientConfig {
	return asr.OpenAIClientConfig{
		Endpoint:         cfg.ASR.Endpoint,
		APIKey:           cfg.ASR.APIKey,
		Model:            cfg.ASR.Model,
		Language:         cfg.ASR.Language,
		Hotwords:         append([]string(nil), cfg.ASR.Hotwords...),
		StripDiarization: cfg.ASR.StripDiarization,
	}
}

func waitForTerminationSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}

// defaultConfigPath returns the default configuration file location.
func defaultConfigPath() string {
	if path := os.Getenv("ELOQUI_CONFIG"); path != "" {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "eloqi", "config.toml")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "eloqi.toml"
}
