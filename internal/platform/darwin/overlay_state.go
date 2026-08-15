package darwin

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func darwinOverlayCommand(state platform.OverlayState, message string) (string, error) {
	switch state {
	case platform.OverlayConnecting,
		platform.OverlayRecording,
		platform.OverlayStopping,
		platform.OverlayWaiting,
		platform.OverlayError:
	default:
		return "", fmt.Errorf("darwin overlay: unknown state %q", state)
	}
	message = strings.TrimSpace(message)
	encoded := base64.StdEncoding.EncodeToString([]byte(message))
	return "show\t" + string(state) + "\t" + encoded + "\n", nil
}

type darwinOverlayShutdownResult struct {
	closeErr error
	killErr  error
	timedOut bool
	exited   bool
}

// waitDarwinOverlayShutdown bounds both closing the helper pipe and waiting
// for the child. closePipe runs asynchronously because a concurrent writer can
// be inside the kernel when shutdown begins.
func waitDarwinOverlayShutdown(
	done <-chan struct{},
	timeout time.Duration,
	closePipe func() error,
	kill func() error,
) darwinOverlayShutdownResult {
	var closeResult <-chan error
	if closePipe != nil {
		result := make(chan error, 1)
		closeResult = result
		go func() { result <- closePipe() }()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var shutdown darwinOverlayShutdownResult
	for {
		select {
		case err := <-closeResult:
			shutdown.closeErr = err
			closeResult = nil
		case <-done:
			shutdown.exited = true
			if closeResult != nil {
				select {
				case shutdown.closeErr = <-closeResult:
				default:
				}
			}
			return shutdown
		case <-timer.C:
			shutdown.timedOut = true
			if kill != nil {
				shutdown.killErr = kill()
			}
			killTimer := time.NewTimer(timeout)
			select {
			case <-done:
				shutdown.exited = true
			case <-killTimer.C:
			}
			if !killTimer.Stop() {
				select {
				case <-killTimer.C:
				default:
				}
			}
			if closeResult != nil {
				select {
				case shutdown.closeErr = <-closeResult:
				default:
				}
			}
			return shutdown
		}
	}
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, prefix+value)
}
