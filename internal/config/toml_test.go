package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTOMLStringArray(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{name: "empty", input: "[]", want: []string{}},
		{name: "values", input: `["Eloqui", "语音,输入"]`, want: []string{"Eloqui", "语音,输入"}},
		{name: "escapes and trailing comma", input: `["say \"hi\"", "a\\b",]`, want: []string{`say "hi"`, `a\b`}},
		{name: "not array", input: `"Eloqui"`, wantErr: true},
		{name: "non string", input: `["Eloqui", 3]`, wantErr: true},
		{name: "missing comma", input: `["a" "b"]`, wantErr: true},
		{name: "unterminated", input: `["a]`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTOMLStringArray(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseTOMLStringArray(%q) succeeded: %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTOMLStringArray(%q): %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseTOMLStringArray(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseTOMLEscapedQuoteBeforeComment(t *testing.T) {
	tbl, err := parseTOML(strings.NewReader(`[asr]
endpoint = "https://example.test/a\"#fragment" # trailing comment
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := tbl["asr"]["endpoint"]; got != `https://example.test/a"#fragment` {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestParseTOMLRejectsTrailingStringGarbage(t *testing.T) {
	if _, err := parseTOML(strings.NewReader("[asr]\nmodel = \"whisper-1\" garbage\n")); err == nil {
		t.Fatal("trailing string garbage accepted")
	}
}

func TestParseTOMLErrorDoesNotEchoMalformedSecret(t *testing.T) {
	const secret = "sk-sensitive-sentinel"
	_, err := parseTOML(strings.NewReader("[asr]\napi_key = \"" + secret + "\n"))
	if err == nil {
		t.Fatal("unterminated API key was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("parse error leaked API key: %v", err)
	}
	if !strings.Contains(err.Error(), "unterminated string") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestParseTOMLTrailingTextDoesNotEchoMalformedSecret(t *testing.T) {
	const secret = "sk-sensitive-trailing-sentinel"
	_, err := parseTOML(strings.NewReader("[asr]\napi_key = \"prefix\"" + secret + "\n"))
	if err == nil {
		t.Fatal("trailing API key text was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("parse error leaked API key: %v", err)
	}
	if !strings.Contains(err.Error(), "unexpected text after string") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestParseTOMLRejectsDuplicateSectionsAndKeys(t *testing.T) {
	tests := []string{
		"[output]\nauto_type = true\nauto_type = false\n",
		"[output]\nauto_type = true\n[output]\n",
	}
	for _, input := range tests {
		if _, err := parseTOML(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("duplicate input error = %v", err)
		}
	}
}
