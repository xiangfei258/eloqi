package hotwords

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAndPrompt(t *testing.T) {
	input := []string{" Eloqui ", "", "语音输入", "Eloqui", "  "}
	want := []string{"Eloqui", "语音输入"}
	if got := Normalize(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
	if got := Prompt(input); got != "Eloqui, 语音输入" {
		t.Fatalf("Prompt() = %q", got)
	}
}

func TestMaxPromptBytesBoundary(t *testing.T) {
	if got := len(Prompt([]string{strings.Repeat("x", MaxPromptBytes)})); got != MaxPromptBytes {
		t.Fatalf("prompt bytes = %d, want %d", got, MaxPromptBytes)
	}
	if got := len(Prompt([]string{strings.Repeat("x", MaxPromptBytes+1)})); got <= MaxPromptBytes {
		t.Fatalf("oversized prompt bytes = %d", got)
	}
}
