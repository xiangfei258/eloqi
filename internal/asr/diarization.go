package asr

import (
	"regexp"
	"strings"
)

// diarizationTranscript recognizes a complete sequence of timestamped speaker
// turns, for example:
//
//	[0.65][S01]你好，一二三。[2.12]
//	[0.00][S01]第一句[1.00][1.00][S02]第二句[2.00]
//
// Requiring the whole string to match prevents the cleanup from mangling
// ordinary prose that merely contains a bracketed number, such as
// "参考文献[1]".
var diarizationTranscript = regexp.MustCompile(
	`^(?:\[\d+(?:\.\d+)?\]\[[Ss]\d+\][^\[\]]*\[\d+(?:\.\d+)?\]){1,}$`,
)

// diarizationMarker matches one timestamp or speaker label inside a transcript
// that has already passed the complete-structure validation above.
var diarizationMarker = regexp.MustCompile(`\[\d+(?:\.\d+)?\]|\[[Ss]\d+\]`)

// stripDiarizationMarkers removes timestamp and speaker markers from a
// validated diarized transcription. If the input is not a complete diarization
// transcript, it is returned unchanged.
func stripDiarizationMarkers(s string) string {
	if !diarizationTranscript.MatchString(s) {
		return s
	}
	out := diarizationMarker.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(out), " ")
}
