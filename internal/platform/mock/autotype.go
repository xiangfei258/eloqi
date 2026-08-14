package mock

import (
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

var _ platform.Autotype = (*Autotype)(nil)

// Autotype is an in-memory platform.Autotype for tests. It records every
// string passed to Type.
type Autotype struct {
	mu sync.Mutex

	// Err, when non-nil, is returned by Type.
	Err error

	typed []string
}

// Type implements platform.Autotype.
func (a *Autotype) Type(text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return a.Err
	}
	a.typed = append(a.typed, text)
	return nil
}

// Typed returns a copy of every string passed to Type.
func (a *Autotype) Typed() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.typed...)
}
