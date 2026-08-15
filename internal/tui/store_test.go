package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/config"
)

func TestFileStoreRoundTripAndCoreCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "eloqi.toml")
	store := FileStore{Path: path}
	want := validSettings()
	want.HotkeyModifiers = "Alt+Super"
	want.HotkeyKey = ""
	want.Mode = "toggle"
	want.ASREngine = config.DefaultASREngine
	want.Hotwords = []string{"Eloqui", "MOSS, Transcribe", "语音输入"}
	want.StopDelay = 1350 * time.Millisecond
	want.AutoType = false
	want.StripDiarization = true
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want.Normalize()) {
		t.Fatalf("round trip mismatch:\n got  %#v\n want %#v", got, want.Normalize())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, fragment := range []string{
		`stop_delay_ms = 1350`,
		`engine = "openai-compatible"`,
		`hotwords = ["Eloqui", "MOSS, Transcribe", "语音输入"]`,
		`auto_type = false`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("saved TOML missing %q:\n%s", fragment, text)
		}
	}

	core, err := config.Load(path)
	if err != nil {
		t.Fatalf("core loader rejected TUI output: %v", err)
	}
	if core.Hotkey.Mods != "Alt+Super" || core.Hotkey.Key != "" || core.Hotkey.Mode != "toggle" || core.Output.AutoType {
		t.Fatalf("core config = %#v", core)
	}

	// Saving over an existing target exercises the atomic replacement path.
	want.ASRModel = "updated-model"
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if updated.ASRModel != "updated-model" {
		t.Fatalf("updated model = %q", updated.ASRModel)
	}
}

func TestFileStoreLoadsDefaultsWhenMissing(t *testing.T) {
	store := FileStore{Path: filepath.Join(t.TempDir(), "eloqi.toml")}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestFileStorePreservesExplicitZeroStopDelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.toml")
	store := FileStore{Path: path}
	want := validSettings()
	want.StopDelay = 0
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.StopDelay != 0 || got.CoreConfig().Hotkey.StopDelayMS != 0 {
		t.Fatalf("zero stop delay became settings=%s core=%d", got.StopDelay, got.CoreConfig().Hotkey.StopDelayMS)
	}
}

func TestFileStorePreservesExtensionFieldsCommentsAndLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.toml")
	existing := strings.Join([]string{
		"# user-owned comment",
		"[hotkey]",
		`mods = "Ctrl" # keep this comment`,
		`x_future_binding = "untouched"`,
		"",
		"[plugin.example]",
		`enabled = true # plugin-owned value`,
		"",
	}, "\r\n")
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := validSettings()
	settings.HotkeyModifiers = "Alt+Super"
	if err := (FileStore{Path: path}).Save(settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, fragment := range []string{
		"# user-owned comment",
		`mods = "Alt+Super" # keep this comment`,
		`x_future_binding = "untouched"`,
		"[plugin.example]",
		`enabled = true # plugin-owned value`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("saved TOML lost %q:\n%s", fragment, text)
		}
	}
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		t.Fatalf("Save changed CRLF file to mixed line endings: %q", text)
	}
}

func TestFileStoreReadsLegacyHotwordString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.toml")
	content := `
[hotkey]
mods = "Ctrl+Alt"
key = "F1"
mode = "hold"
stop_delay_ms = 500

[asr]
engine = "custom"
endpoint = "https://asr.example.test"
api_key = "secret"
model = "model"
hotwords = "one, two"

[output]
auto_type = true
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := (FileStore{Path: path}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ASREngine != "custom" || got.StopDelay != 500*time.Millisecond || strings.Join(got.Hotwords, "|") != "one|two" {
		t.Fatalf("extended settings = %#v", got)
	}
}

func TestFileStoreRejectsMalformedExtendedValues(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr string
	}{
		{"delay", "stop_delay_ms = soon", "integer"},
		{"array", `hotwords = ["one"`, "hotwords"},
		{"engine", `engine = "unterminated`, "unterminated string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "eloqi.toml")
			section := "[hotkey]\n"
			if test.name != "delay" {
				section = "[asr]\n"
			}
			if err := os.WriteFile(path, []byte(section+test.line+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := (FileStore{Path: path}).Load()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Load() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestFileStoreRejectsEmptyPathAndInvalidSettings(t *testing.T) {
	store := FileStore{}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected empty load path error")
	}
	if err := store.Save(validSettings()); err == nil {
		t.Fatal("expected empty save path error")
	}

	settings := validSettings()
	settings.StopDelay = -time.Millisecond
	if err := (FileStore{Path: filepath.Join(t.TempDir(), "eloqi.toml")}).Save(settings); err == nil {
		t.Fatal("expected settings validation error")
	}
}

func TestParseHotwordsEscapesAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.toml")
	content := `
[asr]
endpoint = "https://asr.example.test/#fragment"
api_key = "secret"
model = "model"
hotwords = ["one", "two, three", "hash#tag"] # trailing comment
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := (FileStore{Path: path}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Hotwords, "|") != "one|two, three|hash#tag" {
		t.Fatalf("hotwords = %#v", got.Hotwords)
	}
}
