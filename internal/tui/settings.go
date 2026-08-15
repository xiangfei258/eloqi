package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/xiangchang24/eloqi/internal/config"
)

const (
	// DefaultStopDelay keeps a short tail after the stop edge so the final
	// spoken syllable is not clipped.
	DefaultStopDelay = 800 * time.Millisecond
)

// Settings is the complete editable configuration presented by the TUI.
type Settings struct {
	HotkeyModifiers string
	HotkeyKey       string
	Mode            string

	ASREngine        string
	ASREndpoint      string
	ASRAPIKey        string
	ASRModel         string
	Language         string
	Hotwords         []string
	StripDiarization bool

	AutoType  bool
	StopDelay time.Duration
}

// Defaults returns editor defaults based on the core configuration defaults.
func Defaults() Settings {
	return FromConfig(config.Defaults())
}

// FromConfig converts the core configuration into editable settings.
func FromConfig(cfg config.Config) Settings {
	cfg = cfg.Normalize()
	return Settings{
		HotkeyModifiers:  cfg.Hotkey.Mods,
		HotkeyKey:        cfg.Hotkey.Key,
		Mode:             cfg.Hotkey.Mode,
		ASREngine:        cfg.ASR.Engine,
		ASREndpoint:      cfg.ASR.Endpoint,
		ASRAPIKey:        cfg.ASR.APIKey,
		ASRModel:         cfg.ASR.Model,
		Language:         cfg.ASR.Language,
		Hotwords:         append([]string(nil), cfg.ASR.Hotwords...),
		StripDiarization: cfg.ASR.StripDiarization,
		AutoType:         cfg.Output.AutoType,
		StopDelay:        time.Duration(cfg.Hotkey.StopDelayMS) * time.Millisecond,
	}.Normalize()
}

// CoreConfig converts all editor fields into the runtime configuration.
func (s Settings) CoreConfig() config.Config {
	s = s.Normalize()
	return config.Config{
		Hotkey: config.HotkeyConfig{
			Mods:        s.HotkeyModifiers,
			Key:         s.HotkeyKey,
			Mode:        s.Mode,
			StopDelayMS: int(s.StopDelay.Milliseconds()),
		},
		ASR: config.ASRConfig{
			Engine:           s.ASREngine,
			Endpoint:         s.ASREndpoint,
			APIKey:           s.ASRAPIKey,
			Model:            s.ASRModel,
			Language:         s.Language,
			Hotwords:         append([]string(nil), s.Hotwords...),
			StripDiarization: s.StripDiarization,
		},
		Output: config.OutputConfig{AutoType: s.AutoType},
	}
}

// Normalize trims user input, canonicalizes the mode, and removes blank or
// duplicate hotwords while preserving their first-seen order.
func (s Settings) Normalize() Settings {
	s.HotkeyModifiers = strings.TrimSpace(s.HotkeyModifiers)
	s.HotkeyKey = strings.TrimSpace(s.HotkeyKey)
	s.Mode = strings.ToLower(strings.TrimSpace(s.Mode))
	s.ASREngine = strings.TrimSpace(s.ASREngine)
	s.ASREndpoint = strings.TrimSpace(s.ASREndpoint)
	s.ASRAPIKey = strings.TrimSpace(s.ASRAPIKey)
	s.ASRModel = strings.TrimSpace(s.ASRModel)
	s.Language = strings.TrimSpace(s.Language)

	seen := make(map[string]struct{}, len(s.Hotwords))
	hotwords := make([]string, 0, len(s.Hotwords))
	for _, hotword := range s.Hotwords {
		hotword = strings.TrimSpace(hotword)
		if hotword == "" {
			continue
		}
		if _, exists := seen[hotword]; exists {
			continue
		}
		seen[hotword] = struct{}{}
		hotwords = append(hotwords, hotword)
	}
	s.Hotwords = hotwords
	return s
}

// Validate checks all user-editable fields before a save is attempted.
func (s Settings) Validate() error {
	s = s.Normalize()
	if s.ASREngine == "" {
		return fmt.Errorf("tui: ASR engine is required")
	}
	if s.StopDelay < 0 {
		return fmt.Errorf("tui: stop delay cannot be negative")
	}
	if s.StopDelay%time.Millisecond != 0 {
		return fmt.Errorf("tui: stop delay must use whole milliseconds")
	}
	milliseconds := s.StopDelay.Milliseconds()
	if int64(int(milliseconds)) != milliseconds {
		return fmt.Errorf("tui: stop delay is too large for this platform")
	}
	if err := s.CoreConfig().Validate(); err != nil {
		return fmt.Errorf("tui: invalid configuration: %w", err)
	}
	for _, value := range []struct {
		name string
		text string
	}{
		{"ASR engine", s.ASREngine},
		{"ASR endpoint", s.ASREndpoint},
		{"ASR API key", s.ASRAPIKey},
		{"ASR model", s.ASRModel},
		{"language", s.Language},
	} {
		if strings.ContainsAny(value.text, "\r\n") {
			return fmt.Errorf("tui: %s cannot contain a line break", value.name)
		}
	}
	for _, hotword := range s.Hotwords {
		if strings.ContainsAny(hotword, "\r\n") {
			return fmt.Errorf("tui: hotword %q cannot contain a line break", hotword)
		}
	}
	return nil
}
