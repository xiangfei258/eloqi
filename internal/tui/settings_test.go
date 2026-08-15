package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/config"
)

func validSettings() Settings {
	return Settings{
		HotkeyModifiers: "Ctrl+Alt",
		HotkeyKey:       "F1",
		Mode:            "hold",
		ASREngine:       config.DefaultASREngine,
		ASREndpoint:     "https://asr.example.test/v1/transcriptions",
		ASRAPIKey:       "test-secret",
		ASRModel:        "whisper-test",
		Language:        "en-US",
		Hotwords:        []string{"Eloqui", "Go"},
		AutoType:        true,
		StopDelay:       800 * time.Millisecond,
	}
}

func TestSettingsNormalize(t *testing.T) {
	settings := validSettings()
	settings.Mode = "  TOGGLE "
	settings.ASREngine = " openai-compatible "
	settings.Hotwords = []string{" Eloqui ", "", "Go", "Eloqui", " Go "}
	got := settings.Normalize()
	if got.Mode != "toggle" || got.ASREngine != "openai-compatible" {
		t.Fatalf("normalization failed: %#v", got)
	}
	if strings.Join(got.Hotwords, ",") != "Eloqui,Go" {
		t.Fatalf("hotwords = %#v", got.Hotwords)
	}
}

func TestSettingsValidate(t *testing.T) {
	if err := validSettings().Validate(); err != nil {
		t.Fatalf("valid settings: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Settings)
		want   string
	}{
		{"engine", func(s *Settings) { s.ASREngine = "" }, "ASR engine"},
		{"negative delay", func(s *Settings) { s.StopDelay = -time.Millisecond }, "negative"},
		{"fractional delay", func(s *Settings) { s.StopDelay = time.Millisecond + time.Nanosecond }, "whole milliseconds"},
		{"hotkey", func(s *Settings) { s.HotkeyModifiers, s.HotkeyKey = "", "" }, "hotkey"},
		{"mode", func(s *Settings) { s.Mode = "press" }, "mode"},
		{"endpoint", func(s *Settings) { s.ASREndpoint = "" }, "endpoint"},
		{"api key", func(s *Settings) { s.ASRAPIKey = "" }, "api_key"},
		{"model", func(s *Settings) { s.ASRModel = "" }, "model"},
		{"line break", func(s *Settings) { s.Language = "en\nUS" }, "line break"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validSettings()
			test.mutate(&settings)
			err := settings.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoreConfigConversion(t *testing.T) {
	settings := validSettings()
	settings.Mode = " Toggle "
	cfg := settings.CoreConfig()
	if cfg.Hotkey.Mode != "toggle" || cfg.ASR.Language != "en-US" || !cfg.Output.AutoType {
		t.Fatalf("CoreConfig() = %#v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("converted config: %v", err)
	}
}
