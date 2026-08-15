package tui

import (
	"bufio"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	settings  Settings
	loadErr   error
	saveErr   error
	saveCount int
}

func (s *memoryStore) Load() (Settings, error) {
	return s.settings, s.loadErr
}

func (s *memoryStore) Save(settings Settings) error {
	s.saveCount++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.settings = settings
	return nil
}

func TestEditorEditsAndSavesEveryProductField(t *testing.T) {
	store := &memoryStore{settings: validSettings()}
	input := strings.Join([]string{
		"Alt+Super",
		"-", // modifier-only binding
		"toggle",
		"openai-compatible",
		"http://127.0.0.1:9011/v1/audio/transcriptions",
		"new-secret",
		"moss-transcribe",
		"false",
		"1250",
		"Eloqui, MOSS, 语音输入",
		"zh-CN",
	}, "\n") + "\n"
	var output bytes.Buffer
	editor := NewEditor(strings.NewReader(input), &output, store)
	got, err := editor.Run()
	if err != nil {
		t.Fatal(err)
	}
	if store.saveCount != 1 {
		t.Fatalf("Save called %d times", store.saveCount)
	}
	if got.HotkeyModifiers != "Alt+Super" || got.HotkeyKey != "" || got.Mode != "toggle" {
		t.Fatalf("hotkey fields = %#v", got)
	}
	if got.ASREngine != "openai-compatible" || got.ASREndpoint != "http://127.0.0.1:9011/v1/audio/transcriptions" || got.ASRAPIKey != "new-secret" || got.ASRModel != "moss-transcribe" {
		t.Fatalf("ASR fields = %#v", got)
	}
	if got.AutoType || got.StopDelay != 1250*time.Millisecond || got.Language != "zh-CN" {
		t.Fatalf("output/delay/language fields = %#v", got)
	}
	if strings.Join(got.Hotwords, "|") != "Eloqui|MOSS|语音输入" {
		t.Fatalf("hotwords = %#v", got.Hotwords)
	}
	terminalText := output.String()
	if strings.Contains(terminalText, "test-secret") || strings.Contains(terminalText, "new-secret") {
		t.Fatalf("terminal leaked API key: %q", terminalText)
	}
	if !strings.Contains(terminalText, "Configuration saved.") {
		t.Fatalf("missing save confirmation: %q", terminalText)
	}
}

func TestEditorBlankInputKeepsValues(t *testing.T) {
	want := validSettings().Normalize()
	store := &memoryStore{settings: want}
	var output bytes.Buffer
	editor := NewEditor(strings.NewReader(strings.Repeat("\n", 11)), &output, store)
	got, err := editor.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.CoreConfig(), want.CoreConfig()) {
		t.Fatalf("core settings changed: got %#v want %#v", got, want)
	}
	if got.StopDelay != want.StopDelay || strings.Join(got.Hotwords, "|") != strings.Join(want.Hotwords, "|") || got.ASREngine != want.ASREngine {
		t.Fatalf("extended settings changed: got %#v want %#v", got, want)
	}
}

func TestEditorRetriesInvalidScalarInput(t *testing.T) {
	store := &memoryStore{settings: validSettings()}
	input := strings.Repeat("\n", 7) + "maybe\nno\n-1\n900\n\n\n"
	var output bytes.Buffer
	got, err := NewEditor(strings.NewReader(input), &output, store).Run()
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoType || got.StopDelay != 900*time.Millisecond {
		t.Fatalf("retried values = %#v", got)
	}
	if count := strings.Count(output.String(), "Invalid value:"); count != 2 {
		t.Fatalf("validation message count = %d, output %q", count, output.String())
	}
}

func TestEditorCancelDoesNotSave(t *testing.T) {
	store := &memoryStore{settings: validSettings()}
	_, err := NewEditor(strings.NewReader(":cancel\n"), &bytes.Buffer{}, store).Run()
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("Run() error = %v", err)
	}
	if store.saveCount != 0 {
		t.Fatalf("Save called %d times", store.saveCount)
	}
}

func TestEditorUsesProtectedSecretReader(t *testing.T) {
	called := 0
	interaction := editorInteraction{
		scanner: bufio.NewScanner(strings.NewReader("visible-fallback\n")),
		out:     &bytes.Buffer{},
		readSecret: func() (string, error) {
			called++
			return "hidden-secret", nil
		},
	}
	got, err := interaction.askSecret("ASR API key", "old-secret", requiredValue)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hidden-secret" || called != 1 {
		t.Fatalf("secret = %q, calls = %d", got, called)
	}
	if !interaction.scanner.Scan() || interaction.scanner.Text() != "visible-fallback" {
		t.Fatal("protected reader unexpectedly consumed scanner input")
	}
}

func TestEditorFinalValidationPreventsSave(t *testing.T) {
	store := &memoryStore{settings: validSettings()}
	input := "-\n-\n" + strings.Repeat("\n", 9)
	_, err := NewEditor(strings.NewReader(input), &bytes.Buffer{}, store).Run()
	if err == nil || !strings.Contains(err.Error(), "hotkey") {
		t.Fatalf("Run() error = %v", err)
	}
	if store.saveCount != 0 {
		t.Fatalf("Save called %d times", store.saveCount)
	}
}

func TestEditorPropagatesLoadSaveAndWriterErrors(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		store := &memoryStore{loadErr: errors.New("load failed")}
		if _, err := NewEditor(strings.NewReader(""), &bytes.Buffer{}, store).Run(); err == nil || !strings.Contains(err.Error(), "load failed") {
			t.Fatalf("Run() error = %v", err)
		}
	})
	t.Run("save", func(t *testing.T) {
		store := &memoryStore{settings: validSettings(), saveErr: errors.New("save failed")}
		if _, err := NewEditor(strings.NewReader(strings.Repeat("\n", 11)), &bytes.Buffer{}, store).Run(); err == nil || !strings.Contains(err.Error(), "save failed") {
			t.Fatalf("Run() error = %v", err)
		}
	})
	t.Run("heading", func(t *testing.T) {
		store := &memoryStore{settings: validSettings()}
		writer := failingWriter{err: errors.New("write failed")}
		if _, err := NewEditor(strings.NewReader(""), writer, store).Run(); err == nil || !strings.Contains(err.Error(), "write failed") {
			t.Fatalf("Run() error = %v", err)
		}
	})
}

func TestEditorRequiresStore(t *testing.T) {
	if _, err := (&Editor{}).Run(); err == nil {
		t.Fatal("expected missing-store error")
	}
	var editor *Editor
	if _, err := editor.Run(); err == nil {
		t.Fatal("expected nil-editor error")
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
