//go:build windows

package windows

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestWindowsUnregisterAndCloseReturnWithFullEventsAndActiveKey(t *testing.T) {
	key := platform.Key{Code: platform.KeyEscape}
	machine := newEdgeMachine()
	if err := machine.register(key); err != nil {
		t.Fatal(err)
	}

	hotkey := &Hotkey{
		events:   make(chan platform.KeyEvent, 1),
		raw:      make(chan rawWindowsKeyEdge),
		commands: make(chan hotkeyCommand, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	hotkey.dispatcher = newWindowsEventDispatcher(hotkey.events)
	// Voice.Stop unregisters only after its Events consumer exits. Fill the
	// channel to reproduce that shutdown boundary exactly.
	hotkey.events <- platform.KeyEvent{Key: key, Pressed: true}
	hotkey.wg.Add(1)
	go hotkey.processWithMachineTicks(machine, nil)
	go func() {
		hotkey.wg.Wait()
		close(hotkey.done)
	}()
	rawAccepted := make(chan struct{})
	go func() {
		hotkey.raw <- rawWindowsKeyEdge{vk: vkEscape, pressed: true}
		close(rawAccepted)
	}()
	select {
	case <-rawAccepted:
	case <-time.After(time.Second):
		t.Fatal("owner did not receive the raw active-key edge")
	}

	unregistered := make(chan error, 1)
	go func() { unregistered <- hotkey.Unregister(key) }()
	select {
	case err := <-unregistered:
		if err != nil {
			t.Fatalf("Unregister: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Unregister was starved by public Events backpressure")
	}
	if len(machine.activeCode) != 0 || len(machine.activeMods) != 0 || len(machine.pending) != 0 {
		t.Fatalf("active state survived unregister: code=%v mods=%v pending=%v", machine.activeCode, machine.activeMods, machine.pending)
	}

	closed := make(chan error, 1)
	go func() { closed <- hotkey.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked after unregister with an abandoned full events channel")
	}
}

func TestWindowsHotkeyCloseIsBoundedWhenThreadQuitFails(t *testing.T) {
	hotkey := &Hotkey{
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		operationTimeout: 20 * time.Millisecond,
		postThread: func(uint32, uint32) error {
			return errors.New("thread queue unavailable")
		},
	}
	hotkey.threadID.Store(42)
	started := time.Now()
	err := hotkey.Close()
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("Close error = %v, want bounded native-thread timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %s despite a 20ms timeout", elapsed)
	}
}
