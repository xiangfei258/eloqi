package darwin

import (
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestDarwinEventDispatcherIgnoresPublicBackpressureDuringShutdown(t *testing.T) {
	output := make(chan platform.KeyEvent, 1)
	output <- platform.KeyEvent{Pressed: true}
	dispatcher := newDarwinEventDispatcher(output)
	if ok := dispatcher.enqueue([]platform.KeyEvent{{Pressed: false}}); !ok {
		t.Fatal("dispatcher rejected event before shutdown")
	}

	closed := make(chan struct{})
	go func() {
		dispatcher.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("dispatcher Close blocked on a full public Events channel")
	}
}
