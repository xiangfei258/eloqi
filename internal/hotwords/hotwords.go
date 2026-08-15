// Package hotwords contains the shared normalization and prompt rules used by
// configuration validation and OpenAI-compatible ASR clients.
package hotwords

import "strings"

// MaxPromptBytes bounds the UTF-8 prompt sent with one transcription request.
// The limit keeps configuration mistakes from creating unexpectedly large
// multipart requests.
const MaxPromptBytes = 8 << 10

// Normalize trims entries, removes empty entries, and de-duplicates while
// preserving the user's first-seen order.
func Normalize(words []string) []string {
	seen := make(map[string]struct{}, len(words))
	normalized := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if _, exists := seen[word]; exists {
			continue
		}
		seen[word] = struct{}{}
		normalized = append(normalized, word)
	}
	return normalized
}

// Prompt returns the normalized comma-separated prompt sent to compatible
// transcription APIs.
func Prompt(words []string) string {
	return strings.Join(Normalize(words), ", ")
}
