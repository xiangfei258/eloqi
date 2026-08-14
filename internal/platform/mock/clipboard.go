package mock

import (
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

var _ platform.Clipboard = (*Clipboard)(nil)

// Clipboard is an in-memory platform.Clipboard for tests. It stores a single
// string and counts reads and writes.
type Clipboard struct {
	mu sync.Mutex

	// Text is the current clipboard content.
	Text string

	// ReadErr and WriteErr, when non-nil, are returned by Read and Write.
	ReadErr  error
	WriteErr error

	reads  int
	writes int
}

// Read implements platform.Clipboard.
func (c *Clipboard) Read() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ReadErr != nil {
		return "", c.ReadErr
	}
	c.reads++
	return c.Text, nil
}

// Write implements platform.Clipboard.
func (c *Clipboard) Write(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.WriteErr != nil {
		return c.WriteErr
	}
	c.writes++
	c.Text = text
	return nil
}

// ReadCount returns the number of successful Read calls.
func (c *Clipboard) ReadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

// WriteCount returns the number of successful Write calls.
func (c *Clipboard) WriteCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}
