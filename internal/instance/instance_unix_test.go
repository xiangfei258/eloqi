//go:build linux || darwin

package instance

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFileLockRejectsSecondInstanceAndCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eloqi.lock")
	first, err := acquireFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireFile(path)
	if !errors.Is(err, ErrAlreadyRunning) || second != nil {
		t.Fatalf("second acquire = (%v, %v), want ErrAlreadyRunning", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
}
