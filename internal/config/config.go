// Package config defines Eloqui's configuration model, validation, and TOML
// loading rules.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xiangchang24/eloqi/internal/hotwords"
	"github.com/xiangchang24/eloqi/internal/httpendpoint"
	"github.com/xiangchang24/eloqi/internal/platform"
)

const DefaultASREngine = "openai-compatible"

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

	// StopDelayMS is the amount of tail audio retained after the stop
	// gesture. Zero explicitly disables the delay; Defaults supplies 800ms
	// when the setting is omitted.
	StopDelayMS int
}

// ASRConfig holds the speech-recognition backend parameters.
type ASRConfig struct {
	// Engine selects the client implementation. P2-P6 ship one
	// OpenAI-compatible HTTP engine.
	Engine   string
	Endpoint string
	APIKey   string
	Model    string
	Language string
	Hotwords []string
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
			Mods:        "Ctrl+Alt",
			Key:         "F1",
			Mode:        "hold",
			StopDelayMS: 800,
		},
		ASR: ASRConfig{
			Engine: DefaultASREngine,
			Model:  "whisper-1",
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
	defer func() { _ = f.Close() }()

	tbl, err := parseTOML(f)
	if err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := validateTOMLKeys(tbl); err != nil {
		return cfg, err
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
		if v, ok := hk["stop_delay_ms"]; ok {
			ms, err := strconv.Atoi(v)
			if err != nil {
				return cfg, fmt.Errorf("config: hotkey.stop_delay_ms must be an integer: %w", err)
			}
			cfg.Hotkey.StopDelayMS = ms
		}
	}

	if asr, ok := tbl["asr"]; ok {
		if v, ok := asr["engine"]; ok {
			cfg.ASR.Engine = v
		}
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
		if v, ok := asr["hotwords"]; ok {
			if strings.HasPrefix(strings.TrimSpace(v), "[") {
				hotwords, err := parseTOMLStringArray(v)
				if err != nil {
					return cfg, fmt.Errorf("config: asr.hotwords: %w", err)
				}
				cfg.ASR.Hotwords = hotwords
			} else if strings.TrimSpace(v) == "" {
				cfg.ASR.Hotwords = nil
			} else {
				// P4 development builds briefly wrote a comma-separated string.
				// Continue to read that representation while always saving arrays.
				cfg.ASR.Hotwords = strings.Split(v, ",")
			}
		}
		if v, ok := asr["strip_diarization"]; ok {
			value, err := parseConfigBool("asr.strip_diarization", v)
			if err != nil {
				return cfg, err
			}
			cfg.ASR.StripDiarization = value
		}
	}

	if out, ok := tbl["output"]; ok {
		if v, ok := out["auto_type"]; ok {
			value, err := parseConfigBool("output.auto_type", v)
			if err != nil {
				return cfg, err
			}
			cfg.Output.AutoType = value
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
	c.ASR.Engine = strings.ToLower(strings.TrimSpace(c.ASR.Engine))
	if c.ASR.Engine == "" {
		c.ASR.Engine = DefaultASREngine
	}
	c.ASR.Endpoint = strings.TrimSpace(c.ASR.Endpoint)
	c.ASR.APIKey = strings.TrimSpace(c.ASR.APIKey)
	c.ASR.Model = strings.TrimSpace(c.ASR.Model)
	c.ASR.Language = strings.TrimSpace(c.ASR.Language)
	c.ASR.Hotwords = hotwords.Normalize(c.ASR.Hotwords)
	return c
}

func validateTOMLKeys(tbl tomlTable) error {
	allowed := map[string]map[string]struct{}{
		"hotkey": {
			"mods": {}, "key": {}, "mode": {}, "stop_delay_ms": {},
		},
		"asr": {
			"engine": {}, "endpoint": {}, "api_key": {}, "model": {},
			"language": {}, "hotwords": {}, "strip_diarization": {},
		},
		"output": {"auto_type": {}},
	}
	for section, values := range tbl {
		keys, exists := allowed[section]
		if !exists {
			if section == "" {
				return fmt.Errorf("config: top-level keys are not supported")
			}
			if strings.HasPrefix(section, "plugin.") || strings.HasPrefix(section, "x-") {
				continue
			}
			return fmt.Errorf("config: unknown section %q", section)
		}
		for key := range values {
			if _, exists := keys[key]; !exists {
				if strings.HasPrefix(key, "x_") {
					continue
				}
				return fmt.Errorf("config: unknown key %s.%s", section, key)
			}
		}
	}
	return nil
}

func parseConfigBool(name, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("config: %s must be true or false", name)
	}
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
	c = c.Normalize()
	if _, err := ParseHotkey(c.Hotkey); err != nil {
		return err
	}
	mode := strings.ToLower(c.Hotkey.Mode)
	if mode != "hold" && mode != "toggle" {
		return fmt.Errorf("config: mode must be \"hold\" or \"toggle\", got %q", c.Hotkey.Mode)
	}
	if c.Hotkey.StopDelayMS < 0 {
		return fmt.Errorf("config: hotkey.stop_delay_ms must not be negative")
	}
	if c.ASR.Engine != DefaultASREngine {
		return fmt.Errorf("config: unsupported asr.engine %q (supported: %q)", c.ASR.Engine, DefaultASREngine)
	}
	if c.ASR.Endpoint == "" {
		return fmt.Errorf("config: asr.endpoint is required")
	}
	if err := httpendpoint.Validate(c.ASR.Endpoint); err != nil {
		return fmt.Errorf("config: asr.endpoint %w", err)
	}
	if c.ASR.APIKey == "" {
		return fmt.Errorf("config: asr.api_key is required")
	}
	if c.ASR.Model == "" {
		return fmt.Errorf("config: asr.model is required")
	}
	if promptBytes := len(hotwords.Prompt(c.ASR.Hotwords)); promptBytes > hotwords.MaxPromptBytes {
		return fmt.Errorf("config: asr.hotwords prompt exceeds %d bytes", hotwords.MaxPromptBytes)
	}
	return nil
}
