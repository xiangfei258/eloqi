package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/config"
)

func TestFileStoreSaveTriggersValidatedHotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.toml")
	store := FileStore{Path: path}
	settings := validSettings()
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}

	reloads := make(chan config.Config, 2)
	failures := make(chan error, 2)
	watcher, err := config.Watch(path, config.WatchOptions{
		PollInterval: 10 * time.Millisecond,
		Debounce:     25 * time.Millisecond,
		OnReload:     func(cfg config.Config) { reloads <- cfg },
		OnError:      func(err error) { failures <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()

	settings.Mode = "toggle"
	settings.HotkeyKey = "F8"
	settings.StopDelay = 1200 * time.Millisecond
	settings.AutoType = false
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-reloads:
		if got.Hotkey.Mode != "toggle" || got.Hotkey.Key != "F8" || got.Hotkey.StopDelayMS != 1200 || got.Output.AutoType {
			t.Fatalf("hot-reloaded config = %#v", got)
		}
	case err := <-failures:
		t.Fatalf("hot reload rejected TUI save: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TUI save hot reload")
	}
}
