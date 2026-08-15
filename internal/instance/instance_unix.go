//go:build linux || darwin

package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

type fileLock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire obtains Eloqui's per-user non-blocking advisory lock.
func Acquire() (*fileLock, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("instance lock: locate user cache: %w", err)
	}
	return acquireFile(filepath.Join(cacheDir, "eloqi", "instance.lock"))
}

func acquireFile(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("instance lock: create directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("instance lock: open %s: %w", path, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("instance lock: lock %s: %w", path, err)
	}
	return &fileLock{file: file}, nil
}

func (l *fileLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.err = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
		if err := l.file.Close(); l.err == nil {
			l.err = err
		}
	})
	return l.err
}
