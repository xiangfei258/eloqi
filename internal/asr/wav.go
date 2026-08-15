package asr

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode"
)

// wavHeader builds a canonical 44-byte WAV header for raw PCM data with the
// given sample rate, channel count and bit depth. The returned slice is meant
// to be prepended to the PCM payload so the result is a complete WAV file.
func wavHeader(pcmLen, sampleRate, channels, bitsPerSample int) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	totalSize := 36 + pcmLen

	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(totalSize))
	copy(h[8:12], "WAVE")

	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(h[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(h[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(h[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(h[34:36], uint16(bitsPerSample))

	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(pcmLen))
	return h
}

// wrapWAV prepends a WAV header to pcm, producing a complete WAV file.
func wrapWAV(pcm []byte, sampleRate, channels, bitsPerSample int) []byte {
	header := wavHeader(len(pcm), sampleRate, channels, bitsPerSample)
	out := make([]byte, len(header)+len(pcm))
	copy(out, header)
	copy(out[len(header):], pcm)
	return out
}

// describeMultipartError formats a common failure message when the HTTP
// response is not what the client expected.
func describeMultipartError(status int, body []byte, truncated bool) error {
	message := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, string(body))
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		message = "empty response body"
	}
	if truncated {
		message += " ... (truncated)"
	}
	return fmt.Errorf("asr: HTTP %d: %s", status, message)
}
