//go:build darwin && cgo

package darwin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const overlayHelperEnvironment = "ELOQI_INTERNAL_NSPANEL_HELPER"

// init turns a re-executed Eloqui binary into the AppKit helper before the
// application entry point starts. This keeps NSApplication/NSPanel on the
// process main thread, as required by AppKit, while the parent remains a
// regular Go command-line process.
func init() {
	if os.Getenv(overlayHelperEnvironment) != "1" {
		return
	}
	runNativeOverlayHelper()
	os.Exit(0)
}

type Overlay struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	command *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{}
	waitErr error
	closed  bool
	timeout time.Duration
}

var _ platform.Overlay = (*Overlay)(nil)

func NewOverlay() (*Overlay, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("darwin overlay: locate executable: %w", err)
	}
	command := exec.Command(executable)
	command.Env = replaceEnvironment(os.Environ(), overlayHelperEnvironment, "1")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("darwin overlay: helper stdin: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("darwin overlay: start NSPanel helper: %w", err)
	}
	overlay := &Overlay{
		command: command,
		stdin:   stdin,
		done:    make(chan struct{}),
	}
	go func() {
		err := command.Wait()
		overlay.mu.Lock()
		overlay.waitErr = err
		overlay.mu.Unlock()
		close(overlay.done)
	}()
	return overlay, nil
}

func (o *Overlay) Show(state platform.OverlayState, message string) error {
	command, err := darwinOverlayCommand(state, message)
	if err != nil {
		return err
	}
	return o.write(command)
}

func (o *Overlay) Hide() error {
	return o.write("hide\n")
}

func (o *Overlay) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return o.waitForExit(nil, nil)
	}
	o.closed = true
	stdin := o.stdin
	var process *os.Process
	if o.command != nil {
		process = o.command.Process
	}
	o.mu.Unlock()

	// EOF is a native helper shutdown command too. Closing the pipe directly
	// avoids ever waiting behind a Show/Hide write that filled the pipe.
	var closePipe func() error
	if stdin != nil {
		closePipe = stdin.Close
	}
	return o.waitForExit(process, closePipe)
}

func (o *Overlay) operationTimeout() time.Duration {
	if o.timeout > 0 {
		return o.timeout
	}
	return 2 * time.Second
}

func (o *Overlay) waitForExit(process *os.Process, closePipe func() error) error {
	timeout := o.operationTimeout()
	shutdown := waitDarwinOverlayShutdown(o.done, timeout, closePipe, func() error {
		if process == nil {
			return nil
		}
		return process.Kill()
	})
	var failures []error
	if shutdown.closeErr != nil {
		failures = append(failures, fmt.Errorf("darwin overlay: close helper stdin: %w", shutdown.closeErr))
	}
	if shutdown.killErr != nil {
		failures = append(failures, fmt.Errorf("darwin overlay: kill helper: %w", shutdown.killErr))
	}
	if shutdown.timedOut {
		failures = append(failures, fmt.Errorf("darwin overlay: NSPanel helper did not exit within %s", timeout))
	} else if shutdown.exited {
		o.mu.Lock()
		waitErr := o.waitErr
		o.mu.Unlock()
		if waitErr != nil {
			failures = append(failures, fmt.Errorf("darwin overlay: helper exit: %w", waitErr))
		}
	}
	return errors.Join(failures...)
}

func (o *Overlay) write(command string) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return fmt.Errorf("darwin overlay: closed")
	}
	select {
	case <-o.done:
		waitErr := o.waitErr
		o.mu.Unlock()
		if waitErr != nil {
			return fmt.Errorf("darwin overlay: helper exited: %w", waitErr)
		}
		return fmt.Errorf("darwin overlay: helper exited")
	default:
	}
	stdin := o.stdin
	o.mu.Unlock()

	o.writeMu.Lock()
	defer o.writeMu.Unlock()
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return fmt.Errorf("darwin overlay: closed")
	}
	o.mu.Unlock()
	if deadliner, ok := stdin.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := deadliner.SetWriteDeadline(time.Now().Add(o.operationTimeout())); err == nil {
			defer deadliner.SetWriteDeadline(time.Time{})
		}
	}
	if _, err := io.WriteString(stdin, command); err != nil {
		return fmt.Errorf("darwin overlay: send helper command: %w", err)
	}
	return nil
}
