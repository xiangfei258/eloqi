// Package asr provides speech-recognition clients that implement
// platform.ASRClient.
//
// The OpenAI client is a non-streaming implementation: audio chunks sent via
// Send are buffered in memory, and Finalize uploads the complete recording as
// a single WAV file to an OpenAI-compatible /v1/audio/transcriptions endpoint.
package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sync"
	"time"

	"github.com/xiangchang24/eloqi/internal/hotwords"
	"github.com/xiangchang24/eloqi/internal/httpendpoint"
	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	defaultMaxAudioBytes  = 64 << 20
	maxASRResponseBytes   = 1 << 20
	maxASRErrorBytes      = 4 << 10
	maxHotwordPromptBytes = hotwords.MaxPromptBytes
)

// OpenAIClientConfig holds the parameters for an OpenAI-compatible
// transcription request.
type OpenAIClientConfig struct {
	// Endpoint is the full URL of the transcription API, e.g.
	//   https://api.openai.com/v1/audio/transcriptions
	Endpoint string

	// APIKey is the bearer token sent in the Authorization header.
	APIKey string

	// Model identifies the transcription model, e.g. "whisper-1".
	Model string

	// Language is an optional BCP-47 language hint (e.g. "zh-CN").
	// When empty the server auto-detects the language.
	Language string

	// Hotwords contains domain-specific names and terms that should be passed
	// to compatible transcription backends as a prompt. Empty entries are
	// ignored and duplicate entries are sent only once.
	Hotwords []string

	// SampleRate, Channels and BitsPerSample describe the PCM format
	// captured by the recorder. They are used to build the WAV header.
	SampleRate    int
	Channels      int
	BitsPerSample int

	// Timeout is the per-request deadline for the transcription HTTP call.
	// Zero means 60 seconds.
	Timeout time.Duration

	// MaxAudioBytes bounds raw PCM retained for one request. Zero selects
	// 64 MiB (roughly 35 minutes of 16 kHz, 16-bit mono audio).
	MaxAudioBytes int

	// StripDiarization enables removal of timestamp/speaker annotations from
	// backends that return diarized transcripts. It is opt-in because many
	// OpenAI-compatible backends return ordinary prose containing bracketed
	// numbers such as "references[1]".
	StripDiarization bool

	// HTTPClient, when non-nil, is used for the transcription request.
	// When nil, http.DefaultClient is used. Injecting a client with a
	// custom Transport makes the client fully testable without a real
	// network listener.
	HTTPClient *http.Client
}

// OpenAIClient is a non-streaming platform.ASRClient that buffers audio and
// uploads it in a single request during Finalize.
//
// The client is safe for concurrent use of Send and SetResultHandler, but
// Finalize must be called at most once per session.
type OpenAIClient struct {
	cfg     OpenAIClientConfig
	handler platform.ResultHandler
	http    *http.Client

	mu        sync.Mutex
	buffer    []byte
	connected bool
	finalized bool
	closed    bool

	requestID    uint64
	activeID     uint64
	activeCancel context.CancelFunc
}

var _ platform.ASRClient = (*OpenAIClient)(nil)

// NewOpenAIClient returns a client with the given configuration. Default PCM
// parameters (16 kHz / 16-bit / mono) are applied when the config leaves them
// at zero.
func NewOpenAIClient(cfg OpenAIClientConfig) *OpenAIClient {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = platform.DefaultSampleRate
	}
	if cfg.Channels == 0 {
		cfg.Channels = platform.DefaultChannels
	}
	if cfg.BitsPerSample == 0 {
		cfg.BitsPerSample = platform.DefaultBitDepth
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.MaxAudioBytes == 0 {
		cfg.MaxAudioBytes = defaultMaxAudioBytes
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &OpenAIClient{cfg: cfg, http: hc}
}

// Connect validates the configuration. It does not open a network connection;
// the actual HTTP request is deferred to Finalize.
func (c *OpenAIClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return fmt.Errorf("asr: already connected")
	}
	if c.cfg.Endpoint == "" {
		return fmt.Errorf("asr: endpoint is required")
	}
	if err := httpendpoint.Validate(c.cfg.Endpoint); err != nil {
		return fmt.Errorf("asr: endpoint %w", err)
	}
	if c.cfg.APIKey == "" {
		return fmt.Errorf("asr: API key is required")
	}
	if c.cfg.Model == "" {
		return fmt.Errorf("asr: model is required")
	}
	c.connected = true
	return nil
}

// Send appends audio to the internal buffer. The audio must be raw PCM matching
// the configured sample rate, channels and bit depth.
func (c *OpenAIClient) Send(audio []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("asr: client is closed")
	}
	if len(audio) > c.cfg.MaxAudioBytes-len(c.buffer) {
		return fmt.Errorf("asr: recording exceeds %d-byte session limit", c.cfg.MaxAudioBytes)
	}
	c.buffer = append(c.buffer, audio...)
	return nil
}

// SetResultHandler registers the handler that receives the final recognition
// result. For this non-streaming client the handler is invoked exactly once,
// from Finalize, with Final set to true.
func (c *OpenAIClient) SetResultHandler(h platform.ResultHandler) {
	c.mu.Lock()
	c.handler = h
	c.mu.Unlock()
}

// Finalize uploads the buffered audio as a WAV file to the transcription
// endpoint, waits for the response, delivers the final result to the handler
// and returns the recognized text.
func (c *OpenAIClient) Finalize() (string, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", fmt.Errorf("asr: client is closed")
	}
	if c.finalized {
		c.mu.Unlock()
		return "", fmt.Errorf("asr: already finalized")
	}
	c.finalized = true
	audio := c.buffer
	handler := c.handler
	cfg := c.cfg
	c.mu.Unlock()

	wav := wrapWAV(audio, cfg.SampleRate, cfg.Channels, cfg.BitsPerSample)

	text, err := c.transcribe(wav)
	if err != nil {
		return "", err
	}

	if cfg.StripDiarization {
		text = stripDiarizationMarkers(text)
	}

	if handler != nil {
		handler(platform.ASRResult{Text: text, Final: true})
	}
	return text, nil
}

// transcribe performs the HTTP multipart upload and parses the response.
func (c *OpenAIClient) transcribe(wav []byte) (string, error) {
	cfg := c.cfg

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	fw, err := w.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("asr: create form file: %w", err)
	}
	if _, err := fw.Write(wav); err != nil {
		return "", fmt.Errorf("asr: write audio: %w", err)
	}
	if err := w.WriteField("model", cfg.Model); err != nil {
		return "", fmt.Errorf("asr: write model field: %w", err)
	}
	if err := w.WriteField("response_format", "json"); err != nil {
		return "", fmt.Errorf("asr: write response_format field: %w", err)
	}
	if cfg.Language != "" {
		if err := w.WriteField("language", cfg.Language); err != nil {
			return "", fmt.Errorf("asr: write language field: %w", err)
		}
	}
	if prompt := hotwordPrompt(cfg.Hotwords); prompt != "" {
		if len(prompt) > maxHotwordPromptBytes {
			return "", fmt.Errorf("asr: hotword prompt exceeds %d bytes", maxHotwordPromptBytes)
		}
		if err := w.WriteField("prompt", prompt); err != nil {
			return "", fmt.Errorf("asr: write prompt field: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("asr: close multipart writer: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return "", fmt.Errorf("asr: client is closed")
	}
	c.requestID++
	requestID := c.requestID
	c.activeID = requestID
	c.activeCancel = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		if c.activeID == requestID {
			c.activeID = 0
			c.activeCancel = nil
		}
		c.mu.Unlock()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, &body)
	if err != nil {
		return "", sanitizedBuildRequestError(err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", sanitizedSendRequestError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	responseLimit := int64(maxASRResponseBytes)
	if resp.StatusCode != http.StatusOK {
		responseLimit = maxASRErrorBytes
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return "", fmt.Errorf("asr: read response: %w", err)
	}
	if int64(len(respBody)) > responseLimit {
		if resp.StatusCode != http.StatusOK {
			return "", describeMultipartError(resp.StatusCode, respBody[:responseLimit], true)
		}
		return "", fmt.Errorf("asr: response exceeds %d-byte limit", responseLimit)
	}
	if resp.StatusCode != http.StatusOK {
		return "", describeMultipartError(resp.StatusCode, respBody, false)
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("asr: parse response: %w", err)
	}
	return result.Text, nil
}

func sanitizedBuildRequestError(_ error) error {
	// URL parser errors can quote the raw endpoint, which may contain query
	// tokens or userinfo. Configuration validation already provides the useful
	// diagnosis, so never echo the value from this defensive boundary.
	return errors.New("asr: build request: invalid endpoint")
}

func sanitizedSendRequestError(err error) error {
	// http.Client wraps transport failures in *url.Error, whose Error method
	// includes the full request URL. Preserve cancellation semantics needed by
	// Voice while keeping endpoint query/userinfo out of logs and overlays.
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("asr: send request canceled: %w", context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("asr: send request timed out: %w", context.DeadlineExceeded)
	default:
		return errors.New("asr: send request failed")
	}
}

func hotwordPrompt(words []string) string {
	return hotwords.Prompt(words)
}

// Close releases resources. It is idempotent. After Close, Send returns an
// error.
func (c *OpenAIClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.buffer = nil
	cancel := c.activeCancel
	c.activeCancel = nil
	c.activeID = 0
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// BufferedLen returns the number of audio bytes buffered so far. It is intended
// for diagnostics and tests.
func (c *OpenAIClient) BufferedLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.buffer)
}
