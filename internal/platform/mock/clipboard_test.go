package mock

import (
	"errors"
	"testing"
)

func TestClipboardRoundTrip(t *testing.T) {
	c := &Clipboard{}
	if got, err := c.Read(); err != nil || got != "" {
		t.Fatalf("initial Read = (%q, %v), want (\"\", nil)", got, err)
	}

	if err := c.Write("copied text"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Read(); err != nil || got != "copied text" {
		t.Fatalf("Read after Write = (%q, %v)", got, err)
	}

	if c.WriteCount() != 1 {
		t.Fatalf("WriteCount = %d, want 1", c.WriteCount())
	}
	if c.ReadCount() != 2 {
		t.Fatalf("ReadCount = %d, want 2", c.ReadCount())
	}
}

func TestClipboardInjectedErrors(t *testing.T) {
	readErr := errors.New("read failed")
	writeErr := errors.New("write failed")

	c := &Clipboard{ReadErr: readErr, WriteErr: writeErr}
	if _, err := c.Read(); err != readErr {
		t.Fatalf("Read err = %v, want %v", err, readErr)
	}
	if err := c.Write("x"); err != writeErr {
		t.Fatalf("Write err = %v, want %v", err, writeErr)
	}
}
