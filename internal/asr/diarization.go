package asr

import (
	"regexp"
	"strings"
)

// diarizationMarker matches the timestamp and speaker tokens that
// diarization-capable recognizers (for example MOSS-Transcribe-Diarize)
// interleave with the recognized text, e.g.:
//
//	[0.65][S01]你好你好，一二三。[2.12]
//
// It matches both the [start]/[end] timestamps ([0.65], [2.12]) and the
// speaker labels ([S01]), so they can be stripped before the text reaches the
// caller.
var diarizationMarker = regexp.MustCompile(`\[\d+(?:\.\d+)?\]|\[[Ss]\d+\]`)

// stripDiarizationMarkers removes timestamp and speaker markers from a
// diarized transcription and collapses the surrounding whitespace, leaving
// plain text.
func stripDiarizationMarkers(s string) string {
	out := diarizationMarker.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(out), " ")
}
