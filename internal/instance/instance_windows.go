//go:build windows

package instance

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

const windowsMutexName = `Local\Eloqui-Desktop-Voice-Input`

type mutexLock struct {
	handle windows.Handle
	once   sync.Once
	err    error
}

// Acquire creates a named mutex whose lifetime is tied to this process handle.
func Acquire() (*mutexLock, error) {
	return acquireMutex(windowsMutexName)
}

func acquireMutex(name string) (*mutexLock, error) {
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("instance lock: encode mutex name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, false, namePointer)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("instance lock: create mutex: %w", err)
	}
	return &mutexLock{handle: handle}, nil
}

func (l *mutexLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() { l.err = windows.CloseHandle(l.handle) })
	return l.err
}
