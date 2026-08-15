package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTUIModeWritesOnlyToFile(t *testing.T) {
	var terminal bytes.Buffer
	path := filepath.Join(t.TempDir(), "nested", "eloqi.log")
	session, err := New(Options{TUI: true, FilePath: path, Terminal: &terminal})
	if err != nil {
		t.Fatal(err)
	}
	session.Debug("hidden debug record")
	session.Info("configuration saved", "component", "tui")
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	if terminal.Len() != 0 {
		t.Fatalf("TUI terminal was polluted with %q", terminal.String())
	}
	if session.Path() != path {
		t.Fatalf("Path() = %q, want %q", session.Path(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	if !strings.Contains(logText, `"msg":"configuration saved"`) || !strings.Contains(logText, `"component":"tui"`) {
		t.Fatalf("structured log missing expected fields: %s", logText)
	}
	if strings.Contains(logText, "hidden debug record") {
		t.Fatalf("debug record should be filtered: %s", logText)
	}
}

func TestTUIDebugModeIncludesDebugRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.log")
	session, err := New(Options{TUI: true, Debug: true, FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	session.Debug("diagnostic detail", "attempt", 2)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"level":"DEBUG"`) || !strings.Contains(string(data), "diagnostic detail") {
		t.Fatalf("debug log not written: %s", data)
	}
}

func TestTerminalModeUsesInjectedWriter(t *testing.T) {
	var terminal bytes.Buffer
	session, err := New(Options{Terminal: &terminal})
	if err != nil {
		t.Fatal(err)
	}
	session.Debug("filtered")
	session.Warn("check environment", "missing", "arecord")
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if session.Path() != "" {
		t.Fatalf("terminal session Path() = %q", session.Path())
	}
	output := terminal.String()
	if !strings.Contains(output, "check environment") || !strings.Contains(output, `"missing":"arecord"`) {
		t.Fatalf("terminal log missing record: %s", output)
	}
	if strings.Contains(output, "filtered") {
		t.Fatalf("debug record should be filtered: %s", output)
	}
}

func TestTUIModeDoesNotFallBackWhenFileOpenFails(t *testing.T) {
	var terminal bytes.Buffer
	if _, err := New(Options{TUI: true, FilePath: t.TempDir(), Terminal: &terminal}); err == nil {
		t.Fatal("expected error when log path is a directory")
	}
	if terminal.Len() != 0 {
		t.Fatalf("terminal received fallback output: %q", terminal.String())
	}
}

func TestNilSessionCloseAndPath(t *testing.T) {
	var session *Session
	if got := session.Path(); got != "" {
		t.Fatalf("nil Path() = %q", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}
