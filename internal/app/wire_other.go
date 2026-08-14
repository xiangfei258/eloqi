//go:build !linux

package app

import "fmt"

// newCapabilities is a stub for platforms whose backends are not implemented
// yet. macOS and Windows are added in P3; until then Run reports a clear
// error at startup instead of leaving the package unbuildable on those
// targets.
func newCapabilities() (*capabilities, error) {
	return nil, fmt.Errorf("eloqi: this platform is not supported yet (Linux only for now)")
}
