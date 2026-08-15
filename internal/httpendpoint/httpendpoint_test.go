package httpendpoint

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.example.test/v1/audio/transcriptions",
		"http://localhost:9011/transcribe?language=zh-CN",
	} {
		if err := Validate(endpoint); err != nil {
			t.Fatalf("Validate(%q): %v", endpoint, err)
		}
	}

	const secret = "secret-sentinel"
	for _, endpoint := range []string{
		"", "://bad", "localhost:9011/transcribe", "/relative",
		"ftp://example.test/transcribe", "https:///missing-host",
		"https://example.test/transcribe#fragment", "://" + secret,
	} {
		err := Validate(endpoint)
		if err == nil {
			t.Fatalf("Validate(%q) succeeded", endpoint)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error leaked endpoint content: %v", err)
		}
	}
}
