package mock

import (
	"sync"

	"github.com/xiangchang24/eloqi/internal/platform"
)

var _ platform.ASRClient = (*ASRClient)(nil)

// ASRClient is an in-memory platform.ASRClient for tests. It records every
// byte sent to it, delivers results through the registered handler on demand,
// and returns a fixed final text from Finalize.
//
// Finalize emits the final result through the handler as well as returning it,
// mirroring the real-world case where the final text can arrive on more than
// one path and must be deduplicated by the caller.
type ASRClient struct {
	mu sync.Mutex

	// FinalText is returned by Finalize.
	FinalText string

	// ConnectErr, SendErr and FinalizeErr, when non-nil, are returned by the
	// corresponding method.
	ConnectErr  error
	SendErr     error
	FinalizeErr error

	connected bool
	finalized bool
	closed    bool
	sent      []byte
	handler   platform.ResultHandler
}

// Connect implements platform.ASRClient.
func (c *ASRClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ConnectErr != nil {
		return c.ConnectErr
	}
	c.connected = true
	return nil
}

// Send implements platform.ASRClient.
func (c *ASRClient) Send(audio []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.SendErr != nil {
		return c.SendErr
	}
	c.sent = append(c.sent, audio...)
	return nil
}

// SetResultHandler implements platform.ASRClient.
func (c *ASRClient) SetResultHandler(h platform.ResultHandler) {
	c.mu.Lock()
	c.handler = h
	c.mu.Unlock()
}

// Finalize implements platform.ASRClient.
func (c *ASRClient) Finalize() (string, error) {
	c.mu.Lock()
	if c.FinalizeErr != nil {
		err := c.FinalizeErr
		c.mu.Unlock()
		return "", err
	}
	c.finalized = true
	h := c.handler
	text := c.FinalText
	c.mu.Unlock()

	if h != nil {
		h(platform.ASRResult{Text: text, Final: true})
	}
	return text, nil
}

// Close implements platform.ASRClient.
func (c *ASRClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// Emit delivers a result through the registered handler. It is a test helper
// used to simulate an incremental result arriving from the backend.
func (c *ASRClient) Emit(res platform.ASRResult) {
	c.mu.Lock()
	h := c.handler
	c.mu.Unlock()
	if h != nil {
		h(res)
	}
}

// SentBytes returns a copy of every byte passed to Send.
func (c *ASRClient) SentBytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.sent...)
}

// Connected reports whether Connect completed successfully.
func (c *ASRClient) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Finalized reports whether Finalize completed successfully.
func (c *ASRClient) Finalized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finalized
}

// Closed reports whether Close was called.
func (c *ASRClient) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
