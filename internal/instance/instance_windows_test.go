//go:build windows

package instance

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNamedMutexRejectsSecondInstanceAndCanBeReacquired(t *testing.T) {
	name := fmt.Sprintf(`Local\Eloqui-Test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	first, err := acquireMutex(name)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireMutex(name)
	if !errors.Is(err, ErrAlreadyRunning) || second != nil {
		t.Fatalf("second acquire = (%v, %v), want ErrAlreadyRunning", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireMutex(name)
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
