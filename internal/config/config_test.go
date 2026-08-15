package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiangchang24/eloqi/internal/hotwords"
	"github.com/xiangchang24/eloqi/internal/platform"
)

func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "eloqi.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path/eloqi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey.Mods != "Ctrl+Alt" {
		t.Fatalf("default mods = %q, want Ctrl+Alt", cfg.Hotkey.Mods)
	}
	if cfg.Hotkey.Key != "F1" {
		t.Fatalf("default key = %q, want F1", cfg.Hotkey.Key)
	}
	if cfg.Hotkey.Mode != "hold" {
		t.Fatalf("default mode = %q, want hold", cfg.Hotkey.Mode)
	}
	if cfg.Hotkey.StopDelayMS != 800 {
		t.Fatalf("default stop delay = %dms, want 800ms", cfg.Hotkey.StopDelayMS)
	}
	if cfg.ASR.Model != "whisper-1" {
		t.Fatalf("default model = %q, want whisper-1", cfg.ASR.Model)
	}
	if cfg.ASR.Engine != DefaultASREngine {
		t.Fatalf("default engine = %q, want %q", cfg.ASR.Engine, DefaultASREngine)
	}
	if !cfg.Output.AutoType {
		t.Fatal("default auto_type = false, want true")
	}
}

func TestLoadFullConfig(t *testing.T) {
	toml := `
# Eloqui configuration
[hotkey]
mods = "Alt+Super"
key = "F5"
mode = "toggle"
stop_delay_ms = 650

[asr]
engine = "openai-compatible"
endpoint = "https://api.example.com/v1/audio/transcriptions"
api_key = "sk-abc123"
model = "whisper-large"
language = "en-US"
hotwords = ["Eloqui", " 语音输入 ", "Eloqui"]
strip_diarization = true

[output]
auto_type = false
`
	path := writeTempTOML(t, toml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey.Mods != "Alt+Super" {
		t.Fatalf("mods = %q, want Alt+Super", cfg.Hotkey.Mods)
	}
	if cfg.Hotkey.Key != "F5" {
		t.Fatalf("key = %q, want F5", cfg.Hotkey.Key)
	}
	if cfg.Hotkey.Mode != "toggle" {
		t.Fatalf("mode = %q, want toggle", cfg.Hotkey.Mode)
	}
	if cfg.Hotkey.StopDelayMS != 650 {
		t.Fatalf("stop_delay_ms = %d, want 650", cfg.Hotkey.StopDelayMS)
	}
	if cfg.ASR.Endpoint != "https://api.example.com/v1/audio/transcriptions" {
		t.Fatalf("endpoint = %q", cfg.ASR.Endpoint)
	}
	if cfg.ASR.APIKey != "sk-abc123" {
		t.Fatalf("api_key = %q", cfg.ASR.APIKey)
	}
	if cfg.ASR.Model != "whisper-large" {
		t.Fatalf("model = %q", cfg.ASR.Model)
	}
	if cfg.ASR.Language != "en-US" {
		t.Fatalf("language = %q", cfg.ASR.Language)
	}
	if len(cfg.ASR.Hotwords) != 2 || cfg.ASR.Hotwords[0] != "Eloqui" || cfg.ASR.Hotwords[1] != "语音输入" {
		t.Fatalf("hotwords = %#v", cfg.ASR.Hotwords)
	}
	if !cfg.ASR.StripDiarization {
		t.Fatal("strip_diarization = false, want true")
	}
	if cfg.Output.AutoType {
		t.Fatal("auto_type = true, want false")
	}
}

func TestLoadPartialConfigAppliesDefaults(t *testing.T) {
	toml := `
[asr]
endpoint = "https://x.example.com"
api_key = "k"
`
	path := writeTempTOML(t, toml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// hotkey and output should retain defaults
	if cfg.Hotkey.Mods != "Ctrl+Alt" {
		t.Fatalf("mods = %q, want default Ctrl+Alt", cfg.Hotkey.Mods)
	}
	if cfg.ASR.Model != "whisper-1" {
		t.Fatalf("model = %q, want default whisper-1", cfg.ASR.Model)
	}
	if !cfg.Output.AutoType {
		t.Fatal("auto_type should default to true")
	}
	// but the provided values should override
	if cfg.ASR.Endpoint != "https://x.example.com" {
		t.Fatalf("endpoint = %q", cfg.ASR.Endpoint)
	}
}

func TestLoadCommentInsideQuotedString(t *testing.T) {
	toml := `
[asr]
endpoint = "https://x.example.com/v1#tag"
api_key = "k"
`
	path := writeTempTOML(t, toml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ASR.Endpoint != "https://x.example.com/v1#tag" {
		t.Fatalf("endpoint = %q, want URL with #", cfg.ASR.Endpoint)
	}
}

func TestParseModifiers(t *testing.T) {
	tests := []struct {
		input string
		want  platform.Modifiers
	}{
		{"", 0},
		{"Ctrl", platform.ModCtrl},
		{"ctrl", platform.ModCtrl},
		{"Alt", platform.ModAlt},
		{"Super", platform.ModSuper},
		{"Shift", platform.ModShift},
		{"Ctrl+Alt", platform.ModCtrl | platform.ModAlt},
		{"Ctrl+Alt+Shift", platform.ModCtrl | platform.ModAlt | platform.ModShift},
		{"Alt+Super", platform.ModAlt | platform.ModSuper},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseModifiers(tt.input)
			if err != nil {
				t.Fatalf("ParseModifiers(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseModifiers(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseModifiersError(t *testing.T) {
	_, err := ParseModifiers("Ctrl+XYZ")
	if err == nil {
		t.Fatal("expected error for unknown modifier")
	}
}

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		name    string
		hc      HotkeyConfig
		want    platform.Key
		wantErr bool
	}{
		{"ctrl+f1", HotkeyConfig{Mods: "Ctrl", Key: "F1"}, platform.Key{Mods: platform.ModCtrl, Code: "F1"}, false},
		{"modifier only", HotkeyConfig{Mods: "Alt+Super", Key: ""}, platform.Key{Mods: platform.ModAlt | platform.ModSuper, Code: platform.KeyNone}, false},
		{"bare key", HotkeyConfig{Mods: "", Key: "F12"}, platform.Key{Mods: 0, Code: "F12"}, false},
		{"unknown key", HotkeyConfig{Mods: "Ctrl", Key: "A"}, platform.Key{}, true},
		{"reserved escape", HotkeyConfig{Key: "Escape"}, platform.Key{}, true},
		{"reserved retry", HotkeyConfig{Key: "R"}, platform.Key{}, true},
		{"empty", HotkeyConfig{}, platform.Key{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHotkey(tt.hc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Mods != tt.want.Mods || got.Code != tt.want.Code {
				t.Fatalf("ParseHotkey() = {%s, %s}, want {%s, %s}", got.Mods, got.Code, tt.want.Mods, tt.want.Code)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := Config{
		Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
		ASR:    ASRConfig{Endpoint: "https://x", APIKey: "k", Model: "m"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	tests := []struct {
		name string
		cfg  Config
	}{
		{"bad mode", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "press"},
			ASR:    ASRConfig{Endpoint: "https://x", APIKey: "k", Model: "m"},
		}},
		{"missing endpoint", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
			ASR:    ASRConfig{APIKey: "k", Model: "m"},
		}},
		{"missing api key", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
			ASR:    ASRConfig{Endpoint: "https://x", Model: "m"},
		}},
		{"bad hotkey", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "A", Mode: "hold"},
			ASR:    ASRConfig{Endpoint: "https://x", APIKey: "k", Model: "m"},
		}},
		{"negative stop delay", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold", StopDelayMS: -1},
			ASR:    ASRConfig{Endpoint: "https://x", APIKey: "k", Model: "m"},
		}},
		{"unsupported engine", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
			ASR:    ASRConfig{Engine: "unknown", Endpoint: "https://x", APIKey: "k", Model: "m"},
		}},
		{"malformed endpoint", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
			ASR:    ASRConfig{Endpoint: "://bad", APIKey: "k", Model: "m"},
		}},
		{"relative endpoint", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
			ASR:    ASRConfig{Endpoint: "localhost:9011/transcribe", APIKey: "k", Model: "m"},
		}},
		{"unsupported endpoint scheme", Config{
			Hotkey: HotkeyConfig{Mods: "Ctrl", Key: "F1", Mode: "hold"},
			ASR:    ASRConfig{Endpoint: "ftp://example.test/transcribe", APIKey: "k", Model: "m"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadRejectsMalformedHotwords(t *testing.T) {
	path := writeTempTOML(t, `
[asr]
hotwords = ["Eloqui", 3]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("malformed hotwords accepted")
	}
}

func TestLoadRejectsNonIntegerStopDelay(t *testing.T) {
	path := writeTempTOML(t, `
[hotkey]
stop_delay_ms = nope
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid stop_delay_ms error")
	}
}

func TestLoadPreservesExplicitZeroStopDelay(t *testing.T) {
	path := writeTempTOML(t, `
[hotkey]
stop_delay_ms = 0
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey.StopDelayMS != 0 {
		t.Fatalf("stop_delay_ms = %d, want explicit zero", cfg.Hotkey.StopDelayMS)
	}
	if got := cfg.Normalize().Hotkey.StopDelayMS; got != 0 {
		t.Fatalf("Normalize changed explicit zero to %d", got)
	}
}

func TestLoadNormalizesModeCaseAndWhitespace(t *testing.T) {
	path := writeTempTOML(t, `
[asr]
endpoint = "https://x"
api_key = "k"
model = "m"

[hotkey]
mode = "  Toggle  "
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey.Mode != "toggle" {
		t.Fatalf("mode = %q, want toggle", cfg.Hotkey.Mode)
	}
}

func TestLoadRejectsInvalidBooleansAndUnknownKeys(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"invalid auto type", "[output]\nauto_type = flase\n", "output.auto_type must be true or false"},
		{"invalid diarization", "[asr]\nstrip_diarization = yes\n", "asr.strip_diarization must be true or false"},
		{"misspelled output key", "[output]\nauto_typ = false\n", "unknown key output.auto_typ"},
		{"unknown section", "[outputs]\nauto_type = false\n", "unknown section"},
		{"top-level key", "auto_type = false\n", "top-level keys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeTempTOML(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadAllowsExplicitExtensionNames(t *testing.T) {
	path := writeTempTOML(t, `[hotkey]
x_future_binding = "untouched"

[plugin.example]
enabled = true
`)
	if _, err := Load(path); err != nil {
		t.Fatalf("explicit extensions rejected: %v", err)
	}
}

func TestValidateHotwordPromptLimit(t *testing.T) {
	valid := Defaults()
	valid.ASR.Endpoint = "https://example.test/v1/audio/transcriptions"
	valid.ASR.APIKey = "k"
	valid.ASR.Hotwords = []string{strings.Repeat("x", hotwords.MaxPromptBytes)}
	if err := valid.Validate(); err != nil {
		t.Fatalf("prompt at limit rejected: %v", err)
	}

	invalid := valid
	invalid.ASR.Hotwords = []string{strings.Repeat("x", hotwords.MaxPromptBytes+1)}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "hotwords prompt") {
		t.Fatalf("oversized prompt error = %v", err)
	}
}
