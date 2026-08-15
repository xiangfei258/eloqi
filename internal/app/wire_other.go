//go:build !linux && !darwin && !windows

package app

import "fmt"

// newCapabilities rejects unsupported targets while keeping cross-compilation
// failures explicit.
func newCapabilities() (*capabilities, error) {
	return nil, fmt.Errorf("eloqi: this platform is not supported")
}
