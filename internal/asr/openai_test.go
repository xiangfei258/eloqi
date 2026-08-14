package asr

import (
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/xiangchang24/eloqi/internal/platform"
)

// mockTransport is an http.RoundTripper that captures the request and returns
// a canned response, avoiding any real network listener.
type mockTransport struct {
	status   int
	respBody string

	gotMethod      string
	gotURL         string
	gotAuth        string
	gotContentType string
	gotBody        []byte
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.gotMethod = req.Method
	m.gotURL = req.URL.String()
	m.gotAuth = req.Header.Get("Authorization")
	m.gotContentType = req.Header.Get("Content-Type")
	body, _ := io.ReadAll(req.Body)
	m.gotBody = body
	req.Body.Close()

	return &http.Response{
		StatusCode: m.status,
		Body:       io.NopCloser(strings.NewReader(m.respBody)),
		Header:     make(http.Header),
	}, nil
}

func newMockClient(t *testing.T, status int, respBody string) (*OpenAIClient, *mockTransport) {
	t.Helper()
	mt := &mockTransport{status: status, respBody: respBody}
	hc := &http.Client{Transport: mt}
	c := NewOpenAIClient(OpenAIClientConfig{
		Endpoint:   "https://asr.example.com/v1/audio/transcriptions",
		APIKey:     "sk-test",
		Model:      "whisper-1",
		Language:   "zh-CN",
		HTTPClient: hc,
	})
	return c, mt
}

func TestWAVHeader(t *testing.T) {
	pcm := make([]byte, 3200) // 0.1 s of 16 kHz/16-bit/mono
	wav := wrapWAV(pcm, 16000, 1, 16)

	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatalf("missing RIFF/WAVE magic: %q", wav[:12])
	}
	if string(wav[12:16]) != "fmt " {
		t.Fatalf("missing fmt chunk: %q", wav[12:16])
	}
	if string(wav[36:40]) != "data" {
		t.Fatalf("missing data chunk: %q", wav[36:40])
	}
	if len(wav) != 44+3200 {
		t.Fatalf("wav length = %d, want %d", len(wav), 44+3200)
	}
}

func TestOpenAISuccessfulTranscription(t *testing.T) {
	client, mt := newMockClient(t, 200, `{"text":"你好世界"}`)

	var handlerText string
	var handlerFinal bool
	client.SetResultHandler(func(r platform.ASRResult) {
		handlerText = r.Text
		handlerFinal = r.Final
	})

	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := client.Send([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := client.Send([]byte{5, 6}); err != nil {
		t.Fatal(err)
	}
	if n := client.BufferedLen(); n != 6 {
		t.Fatalf("buffered = %d, want 6", n)
	}

	text, err := client.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if text != "你好世界" {
		t.Fatalf("text = %q, want %q", text, "你好世界")
	}
	if handlerText != "你好世界" || !handlerFinal {
		t.Fatalf("handler = (%q, %v), want (%q, true)", handlerText, handlerFinal, "你好世界")
	}
	if mt.gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", mt.gotMethod)
	}
	if mt.gotAuth != "Bearer sk-test" {
		t.Fatalf("auth = %q, want %q", mt.gotAuth, "Bearer sk-test")
	}
	if !strings.HasPrefix(mt.gotContentType, "multipart/form-data") {
		t.Fatalf("content-type = %q, want multipart/form-data", mt.gotContentType)
	}

	// Parse the multipart body to verify fields and file.
	r := strings.NewReader(string(mt.gotBody))
	boundary := strings.TrimPrefix(mt.gotContentType, "multipart/form-data; boundary=")
	mr := multipart.NewReader(r, boundary)
	form, err := mr.ReadForm(10 << 20)
	if err != nil {
		t.Fatalf("read multipart form: %v", err)
	}

	if got := form.Value["model"]; len(got) == 0 || got[0] != "whisper-1" {
		t.Fatalf("model = %v, want [whisper-1]", got)
	}
	if got := form.Value["response_format"]; len(got) == 0 || got[0] != "json" {
		t.Fatalf("response_format = %v, want [json]", got)
	}
	if got := form.Value["language"]; len(got) == 0 || got[0] != "zh-CN" {
		t.Fatalf("language = %v, want [zh-CN]", got)
	}

	// file part: 44-byte WAV header + 6 bytes PCM = 50 bytes
	if files := form.File["file"]; len(files) == 0 {
		t.Fatal("no file part in multipart form")
	} else {
		f, _ := files[0].Open()
		fileBytes, _ := io.ReadAll(f)
		f.Close()
		if len(fileBytes) != 50 {
			t.Fatalf("uploaded file = %d bytes, want 50", len(fileBytes))
		}
	}
}

func TestOpenAIServerError(t *testing.T) {
	client, _ := newMockClient(t, 401, `{"error":"invalid api key"}`)
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Finalize(); err == nil {
		t.Fatal("Finalize should fail on 401")
	}
}

func TestOpenAIConnectValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  OpenAIClientConfig
	}{
		{"empty endpoint", OpenAIClientConfig{APIKey: "k", Model: "m"}},
		{"empty api key", OpenAIClientConfig{Endpoint: "http://x", Model: "m"}},
		{"empty model", OpenAIClientConfig{Endpoint: "http://x", APIKey: "k"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewOpenAIClient(tt.cfg)
			if err := c.Connect(); err == nil {
				t.Fatal("Connect should fail")
			}
		})
	}
}

func TestOpenAIDoubleConnect(t *testing.T) {
	c := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: "http://x", APIKey: "k", Model: "m",
	})
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(); err == nil {
		t.Fatal("second Connect should fail")
	}
}

func TestOpenAIDoubleFinalize(t *testing.T) {
	client, _ := newMockClient(t, 200, `{"text":"ok"}`)
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Finalize(); err != nil {
		t.Fatalf("first Finalize: %v", err)
	}
	if _, err := client.Finalize(); err == nil {
		t.Fatal("second Finalize should fail")
	}
}

func TestOpenAICloseBlocksSend(t *testing.T) {
	c := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: "http://x", APIKey: "k", Model: "m",
	})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Send([]byte{1}); err == nil {
		t.Fatal("Send after Close should fail")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestOpenAIDefaults(t *testing.T) {
	c := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: "http://x", APIKey: "k", Model: "m",
	})
	if c.cfg.SampleRate != 16000 {
		t.Fatalf("default sample rate = %d, want 16000", c.cfg.SampleRate)
	}
	if c.cfg.Channels != 1 {
		t.Fatalf("default channels = %d, want 1", c.cfg.Channels)
	}
	if c.cfg.BitsPerSample != 16 {
		t.Fatalf("default bits = %d, want 16", c.cfg.BitsPerSample)
	}
}
