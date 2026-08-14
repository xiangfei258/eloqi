// Package app contains the platform-agnostic application entry point. It
// loads configuration, creates platform capabilities through the newCapabilities
// function (implemented per-OS via build tags), wires up the Voice pipeline,
// and runs until interrupted.
package app

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/xiangchang24/eloqi/internal/asr"
	"github.com/xiangchang24/eloqi/internal/config"
	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/voice"
)

// capabilities bundles the platform-specific implementations created by
// newCapabilities.
type capabilities struct {
	hotkey      platform.Hotkey
	newRecorder voice.RecorderFactory
	clipboard   platform.Clipboard
	autotype    platform.Autotype // may be nil
}

// Run is the main entry point. It loads config, wires up dependencies, and
// blocks until a termination signal is received.
func Run() int {
	configPath := flag.String("config", defaultConfigPath(), "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eloqi: load config: %v\n", err)
		return 1
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "eloqi: invalid config: %v\n", err)
		return 1
	}

	key, err := config.ParseHotkey(cfg.Hotkey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eloqi: parse hotkey: %v\n", err)
		return 1
	}

	caps, err := newCapabilities()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eloqi: platform init: %v\n", err)
		return 1
	}
	defer caps.hotkey.Close()

	asrFactory := func() platform.ASRClient {
		return asr.NewOpenAIClient(asr.OpenAIClientConfig{
			Endpoint: cfg.ASR.Endpoint,
			APIKey:   cfg.ASR.APIKey,
			Model:    cfg.ASR.Model,
			Language: cfg.ASR.Language,
		})
	}

	v := voice.New(voice.Config{
		Hotkey:      caps.hotkey,
		NewRecorder: caps.newRecorder,
		NewASR:      asrFactory,
		Clipboard:   caps.clipboard,
		Autotype:    caps.autotype,
		Key:         key,
		Mode:        cfg.Hotkey.Mode,
		AutoType:    cfg.Output.AutoType,
	})
	v.OnResult = func(text string, err error) {
		if err != nil {
			log.Printf("session error: %v", err)
			return
		}
		if text != "" {
			log.Printf("recognized: %s", text)
		}
	}

	if err := v.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "eloqi: start: %v\n", err)
		return 1
	}

	mode := cfg.Hotkey.Mode
	if mode == "" {
		mode = "hold"
	}
	log.Printf("eloqi ready: hotkey=%s mode=%s autoType=%v", key, mode, cfg.Output.AutoType)
	log.Println("press Ctrl+C to quit")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	v.Stop()
	return 0
}

// defaultConfigPath returns the default configuration file location.
func defaultConfigPath() string {
	if p := os.Getenv("ELOQI_CONFIG"); p != "" {
		return p
	}
	// Prefer ~/.config/eloqi/config.toml, fall back to ./eloqi.toml.
	if home, err := os.UserHomeDir(); err == nil {
		p := home + "/.config/eloqi/config.toml"
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "eloqi.toml"
}
