package stats

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStorePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "stats.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 15, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))
	s.now = func() time.Time { return fixed }
	if err := s.Record("你好 Go", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	got := s.Snapshot()
	if got.Recordings != 1 || got.TotalCharacters != 5 || got.LastCharacters != 5 {
		t.Fatalf("snapshot = %+v", got)
	}
	if got.TotalDurationMS != 1500 || got.LastDurationMS != 1500 {
		t.Fatalf("durations = %+v", got)
	}
	if !got.UpdatedAt.Equal(fixed.UTC()) {
		t.Fatalf("updated_at = %s, want %s", got.UpdatedAt, fixed.UTC())
	}

	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Snapshot() != got {
		t.Fatalf("reloaded = %+v, want %+v", reloaded.Snapshot(), got)
	}
}

func TestStoreConcurrentRecord(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 20
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Record("ab", time.Second); err != nil {
				t.Errorf("Record: %v", err)
			}
		}()
	}
	wg.Wait()
	got := s.Snapshot()
	if got.Recordings != workers || got.TotalCharacters != workers*2 || got.TotalDurationMS != workers*1000 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestStoreRejectsNegativeDurationWithoutMutation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record("x", -time.Second); err == nil {
		t.Fatal("negative duration accepted")
	}
	if got := s.Snapshot(); got != (Snapshot{}) {
		t.Fatalf("snapshot mutated: %+v", got)
	}
}

func TestOpenRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("corrupt file accepted")
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestDefaultPath(t *testing.T) {
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	homePath := filepath.Join(t.TempDir(), "home", "tester")
	configPath := filepath.Join(t.TempDir(), "config")
	home := func() (string, error) { return homePath, nil }
	configDir := func() (string, error) { return configPath, nil }

	wantLinux := filepath.Join(homePath, ".local", "state", "eloqi", "stats.json")
	got, err := defaultPath("linux", getenv, home, configDir)
	if err != nil || got != wantLinux {
		t.Fatalf("linux path = %q, %v; want %q", got, err, wantLinux)
	}

	env["XDG_STATE_HOME"] = filepath.Join(t.TempDir(), "state")
	wantXDG := filepath.Join(env["XDG_STATE_HOME"], "eloqi", "stats.json")
	got, err = defaultPath("linux", getenv, home, configDir)
	if err != nil || got != wantXDG {
		t.Fatalf("XDG path = %q, %v; want %q", got, err, wantXDG)
	}

	env[stateDirectoryEnv] = filepath.Join(t.TempDir(), "portable")
	wantOverride := filepath.Join(env[stateDirectoryEnv], "stats.json")
	got, err = defaultPath("windows", getenv, home, configDir)
	if err != nil || got != wantOverride {
		t.Fatalf("override path = %q, %v; want %q", got, err, wantOverride)
	}
}

func TestDefaultPathRejectsRelativeXDGStateHome(t *testing.T) {
	getenv := func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "relative/state"
		}
		return ""
	}
	_, err := defaultPath("linux", getenv,
		func() (string, error) { return "", errors.New("unused") },
		func() (string, error) { return "", errors.New("unused") },
	)
	if err == nil {
		t.Fatal("relative XDG_STATE_HOME accepted")
	}
}

func TestDefaultPathPropagatesDirectoryError(t *testing.T) {
	want := errors.New("no config")
	_, err := defaultPath("windows", func(string) string { return "" },
		func() (string, error) { return "unused", nil },
		func() (string, error) { return "", want },
	)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
