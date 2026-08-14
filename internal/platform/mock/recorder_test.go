package mock

import (
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestRecorderStreamsAllData(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5}
	r := &Recorder{Data: data}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if !r.Started() {
		t.Fatal("Started() = false, want true")
	}

	buf := make([]byte, 16)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := buf[:n]; !reflect.DeepEqual(got, data) {
		t.Fatalf("Read = %v, want %v", got, data)
	}

	remaining, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("Stop remaining = %v, want empty", remaining)
	}

	if _, err := r.Read(buf); err != io.EOF {
		t.Fatalf("Read after stop err = %v, want io.EOF", err)
	}
}

func TestRecorderChunkedReads(t *testing.T) {
	data := []byte{10, 20, 30, 40, 50}
	r := &Recorder{Data: data, ChunkSize: 2}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}

	want := [][]byte{{10, 20}, {30, 40}, {50}}
	buf := make([]byte, 2)
	for i, chunk := range want {
		n, err := r.Read(buf)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if got := buf[:n]; !reflect.DeepEqual(got, chunk) {
			t.Fatalf("chunk %d = %v, want %v", i, got, chunk)
		}
	}

	// All data consumed but not stopped: Read reports "no data ready".
	if n, err := r.Read(buf); n != 0 || err != nil {
		t.Fatalf("Read before stop = (%d, %v), want (0, nil)", n, err)
	}
}

func TestRecorderStopReturnsRemaining(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5}
	r := &Recorder{Data: data, ChunkSize: 2}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 2)
	if n, err := r.Read(buf); err != nil || n != 2 {
		t.Fatalf("first Read = (%d, %v), want (2, nil)", n, err)
	}

	remaining, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{3, 4, 5}; !reflect.DeepEqual(remaining, want) {
		t.Fatalf("Stop remaining = %v, want %v", remaining, want)
	}
	if !r.Stopped() {
		t.Fatal("Stopped() = false, want true")
	}
}

func TestRecorderReadBeforeStart(t *testing.T) {
	r := &Recorder{Data: []byte{1}}
	if _, err := r.Read(make([]byte, 1)); err == nil {
		t.Fatal("Read before Start should fail")
	}
}

func TestRecorderDoubleStart(t *testing.T) {
	r := &Recorder{Data: []byte{1}}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err == nil {
		t.Fatal("second Start should fail")
	}
}

func TestRecorderInjectedErrors(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		want := errors.New("no device")
		r := &Recorder{StartErr: want}
		if err := r.Start(); err != want {
			t.Fatalf("Start err = %v, want %v", err, want)
		}
	})
	t.Run("read", func(t *testing.T) {
		want := errors.New("read failure")
		r := &Recorder{Data: []byte{1}, ReadErr: want}
		if err := r.Start(); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1)
		if _, err := r.Read(buf); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Stop(); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Read(buf); err != want {
			t.Fatalf("Read after drain err = %v, want %v", err, want)
		}
	})
	t.Run("stop", func(t *testing.T) {
		want := errors.New("stop failure")
		r := &Recorder{Data: []byte{1}, StopErr: want}
		if err := r.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Stop(); err != want {
			t.Fatalf("Stop err = %v, want %v", err, want)
		}
	})
}

func TestRecorderClose(t *testing.T) {
	r := &Recorder{Data: []byte{1}}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !r.Closed() {
		t.Fatal("Closed() = false, want true")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close should be idempotent, got %v", err)
	}
}
