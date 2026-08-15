//go:build darwin && cgo

package darwin

import (
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestDarwinUnregisterReturnsWithFullEventsAndActiveKey(t *testing.T) {
	key := platform.Key{Mods: platform.ModSuper, Code: platform.KeyR}
	hotkey := &Hotkey{
		machine: newDarwinEdgeMachine(),
		events:  make(chan platform.KeyEvent, 1),
		stop:    make(chan struct{}),
	}
	hotkey.dispatcher = newDarwinEventDispatcher(hotkey.events)
	defer hotkey.dispatcher.close()
	if err := hotkey.machine.register(key); err != nil {
		t.Fatal(err)
	}
	hotkey.machine.activeCode[0x0F] = key
	hotkey.events <- platform.KeyEvent{Key: key, Pressed: true}
	emitted := make(chan bool, 1)
	go func() {
		emitted <- hotkey.emit([]platform.KeyEvent{{Key: key, Pressed: true}})
	}()
	select {
	case ok := <-emitted:
		if !ok {
			t.Fatal("dispatcher rejected an event before shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("CGEventTap owner was blocked by public Events backpressure")
	}

	returned := make(chan error, 1)
	go func() { returned <- hotkey.Unregister(key) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Unregister: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Unregister blocked trying to synthesize a release")
	}
}
