//go:build linux

package linux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func sleepingCommand(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "5")
}

func TestClipboardCommandsHaveDeadline(t *testing.T) {
	for _, session := range []string{"wayland", "x11"} {
		t.Run(session+" read", func(t *testing.T) {
			clipboard := &Clipboard{session: session, timeout: 20 * time.Millisecond, command: sleepingCommand}
			started := time.Now()
			_, err := clipboard.Read()
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("Read error = %v", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("Read took %s", elapsed)
			}
		})
		t.Run(session+" write", func(t *testing.T) {
			clipboard := &Clipboard{session: session, timeout: 20 * time.Millisecond, command: sleepingCommand}
			started := time.Now()
			err := clipboard.Write("text")
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("Write error = %v", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("Write took %s", elapsed)
			}
		})
	}
}

func TestAutotypeCommandHasDeadline(t *testing.T) {
	for _, session := range []string{"wayland", "x11"} {
		autotype := &Autotype{session: session, timeout: 20 * time.Millisecond, command: sleepingCommand}
		started := time.Now()
		err := autotype.simulatePaste()
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("%s simulatePaste error = %v", session, err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("%s simulatePaste took %s", session, elapsed)
		}
	}
}
