package mock

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestASRLifecycle(t *testing.T) {
	c := &ASRClient{FinalText: "hello world"}
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if !c.Connected() {
		t.Fatal("Connected() = false, want true")
	}

	var results []platform.ASRResult
	c.SetResultHandler(func(r platform.ASRResult) {
		results = append(results, r)
	})

	if err := c.Send([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]byte("def")); err != nil {
		t.Fatal(err)
	}
	if got := c.SentBytes(); !reflect.DeepEqual(got, []byte("abcdef")) {
		t.Fatalf("SentBytes = %q, want %q", got, "abcdef")
	}

	// An incremental result delivered through the handler.
	c.Emit(platform.ASRResult{Text: "hello", Final: false})

	// Finalize returns the text and also emits a final result.
	got, err := c.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("Finalize = %q, want %q", got, "hello world")
	}
	if !c.Finalized() {
		t.Fatal("Finalized() = false, want true")
	}

	wantResults := []platform.ASRResult{
		{Text: "hello", Final: false},
		{Text: "hello world", Final: true},
	}
	if !reflect.DeepEqual(results, wantResults) {
		t.Fatalf("results = %#v, want %#v", results, wantResults)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !c.Closed() {
		t.Fatal("Closed() = false, want true")
	}
}

func TestASRInjectedErrors(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		want := errors.New("connect failed")
		c := &ASRClient{ConnectErr: want}
		if err := c.Connect(); err != want {
			t.Fatalf("Connect err = %v, want %v", err, want)
		}
	})
	t.Run("send", func(t *testing.T) {
		want := errors.New("send failed")
		c := &ASRClient{SendErr: want}
		if err := c.Send([]byte("x")); err != want {
			t.Fatalf("Send err = %v, want %v", err, want)
		}
	})
	t.Run("finalize", func(t *testing.T) {
		want := errors.New("finalize failed")
		c := &ASRClient{FinalizeErr: want}
		if _, err := c.Finalize(); err != want {
			t.Fatalf("Finalize err = %v, want %v", err, want)
		}
	})
}

func TestASRConcurrentSend(t *testing.T) {
	c := &ASRClient{}
	const workers = 8
	const perWorker = 100

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := c.Send([]byte{byte(i)}); err != nil {
					t.Errorf("Send: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := len(c.SentBytes()); got != workers*perWorker {
		t.Fatalf("SentBytes len = %d, want %d", got, workers*perWorker)
	}
}
