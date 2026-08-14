// Package config defines Eloqui's configuration model and a minimal TOML
// loader. The configuration is intentionally flat for P1; richer validation
// and hot-reload are added in later phases.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// Config is the top-level configuration object.
type Config struct {
	Hotkey HotkeyConfig
	ASR    ASRConfig
	Output OutputConfig
}

// HotkeyConfig describes the global hotkey binding and trigger mode.
type HotkeyConfig struct {
	// Mods is a +-separated list of modifier names, e.g. "Ctrl+Alt".
	// May be empty for a bare-key binding.
	Mods string

	// Key is the non-modifier key name, e.g. "F1", "Tab", or "" for a
	// modifier-only binding such as "Alt+Super".
	Key string

	// Mode is "hold" (press-and-hold) or "toggle" (press to start, press
	// again to stop). Default is "hold".
	Mode string
}

// ASRConfig holds the speech-recognition backend parameters.
type ASRConfig struct {
	Endpoint string
	APIKey   string
	Model    string
	Language string
	// StripDiarization opts in to removing timestamp/speaker annotations from
	// backends that return diarized transcripts. It is false by default so
	// ordinary bracketed references are never rewritten.
	StripDiarization bool
}

// OutputConfig controls how recognized text is delivered to the user.
type OutputConfig struct {
	// AutoType is true to inject text into the focused window (simulate
	// paste); false to only copy to the clipboard.
	AutoType bool
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() Config {
	return Config{
		Hotkey: HotkeyConfig{
			Mods: "Ctrl+Alt",
			Key:  "F1",
			Mode: "hold",
		},
		ASR: ASRConfig{
			Model: "whisper-1",
		},
		Output: OutputConfig{
			AutoType: true,
		},
	}.Normalize()
}

// Load reads and parses a TOML configuration file, applying defaults for any
// missing values. If the file does not exist, Defaults() is returned.
func Load(path string) (Config, error) {
	cfg := Defaults()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	tbl, err := parseTOML(f)
	if err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}

	if hk, ok := tbl["hotkey"]; ok {
		if v, ok := hk["mods"]; ok {
			cfg.Hotkey.Mods = v
		}
		if v, ok := hk["key"]; ok {
			cfg.Hotkey.Key = v
		}
		if v, ok := hk["mode"]; ok {
			cfg.Hotkey.Mode = v
		}
	}

	if asr, ok := tbl["asr"]; ok {
		if v, ok := asr["endpoint"]; ok {
			cfg.ASR.Endpoint = v
		}
		if v, ok := asr["api_key"]; ok {
			cfg.ASR.APIKey = v
		}
		if v, ok := asr["model"]; ok {
			cfg.ASR.Model = v
		}
		if v, ok := asr["language"]; ok {
			cfg.ASR.Language = v
		}
		if v, ok := asr["strip_diarization"]; ok {
			cfg.ASR.StripDiarization = v == "true"
		}
	}

	if out, ok := tbl["output"]; ok {
		if v, ok := out["auto_type"]; ok {
			cfg.Output.AutoType = (v == "true")
		}
	}

	return cfg.Normalize(), nil
}

// Normalize trims and lowercases mode and applies the default mode when a
// caller leaves it empty. Validation and runtime comparisons then use exactly
// the same representation.
func (c Config) Normalize() Config {
	c.Hotkey.Mode = strings.ToLower(strings.TrimSpace(c.Hotkey.Mode))
	if c.Hotkey.Mode == "" {
		c.Hotkey.Mode = "hold"
	}
	return c
}

// ParseModifiers converts a +-separated modifier string (e.g. "Ctrl+Alt") into
// a platform.Modifiers bitmask. Unknown modifier names produce an error.
func ParseModifiers(s string) (platform.Modifiers, error) {
	if s == "" {
		return 0, nil
	}
	var mods platform.Modifiers
	for _, part := range strings.Split(s, "+") {
		part = strings.TrimSpace(part)
		switch strings.ToLower(part) {
		case "ctrl", "control":
			mods |= platform.ModCtrl
		case "alt", "option":
			mods |= platform.ModAlt
		case "super", "win", "windows", "cmd", "command":
			mods |= platform.ModSuper
		case "shift":
			mods |= platform.ModShift
		default:
			return 0, fmt.Errorf("config: unknown modifier %q", part)
		}
	}
	return mods, nil
}

// ParseHotkey converts a HotkeyConfig into a platform.Key, validating that the
// key and modifiers are recognised.
func ParseHotkey(hc HotkeyConfig) (platform.Key, error) {
	mods, err := ParseModifiers(hc.Mods)
	if err != nil {
		return platform.Key{}, err
	}
	code := platform.KeyCode(hc.Key)
	if hc.Key != "" && !isKnownKeyCode(code) {
		return platform.Key{}, fmt.Errorf("config: unknown key %q", hc.Key)
	}
	if mods == 0 && code == platform.KeyNone {
		return platform.Key{}, fmt.Errorf("config: hotkey must have at least one modifier or key")
	}
	return platform.Key{Mods: mods, Code: code}, nil
}

// isKnownKeyCode reports whether code is one of the well-known key codes
// defined in the platform package.
func isKnownKeyCode(code platform.KeyCode) bool {
	switch code {
	case platform.KeyTab, platform.KeyCapsLock,
		platform.KeyLeft, platform.KeyRight, platform.KeyUp, platform.KeyDown,
		platform.KeyHome, platform.KeyEnd, platform.KeyPageUp, platform.KeyPageDown,
		platform.KeyInsert, platform.KeyDelete,
		platform.KeyNum0, platform.KeyNum1, platform.KeyNum2, platform.KeyNum3,
		platform.KeyNum4, platform.KeyNum5, platform.KeyNum6, platform.KeyNum7,
		platform.KeyNum8, platform.KeyNum9:
		return true
	}
	for i := 1; i <= 24; i++ {
		if fk, ok := platform.FunctionKey(i); ok && fk == code {
			return true
		}
	}
	return false
}

// Validate checks that the configuration is internally consistent and returns
// a human-readable error for the first problem found.
func (c Config) Validate() error {
	if _, err := ParseHotkey(c.Hotkey); err != nil {
		return err
	}
	mode := strings.ToLower(c.Hotkey.Mode)
	if mode != "hold" && mode != "toggle" {
		return fmt.Errorf("config: mode must be \"hold\" or \"toggle\", got %q", c.Hotkey.Mode)
	}
	if c.ASR.Endpoint == "" {
		return fmt.Errorf("config: asr.endpoint is required")
	}
	if c.ASR.APIKey == "" {
		return fmt.Errorf("config: asr.api_key is required")
	}
	if c.ASR.Model == "" {
		return fmt.Errorf("config: asr.model is required")
	}
	return nil
}
