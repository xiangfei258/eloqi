package asr

import "testing"

func TestStripDiarizationMarkers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"typical turn", "[0.65][S01]你好你好，一二三。[2.12]", "你好你好，一二三。"},
		{"empty utterance", "[0.08][S01][0.96]", ""},
		{"no markers", "你好世界", "你好世界"},
		{"decimal timestamps", "[10.25][S03]内容[12.80]", "内容"},
		{"lowercase speaker", "[0.00][s02]第二句[1.00]", "第二句"},
		{"collapses whitespace", "[0.00][S01]  多   空格  [1.00]", "多 空格"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripDiarizationMarkers(tt.in); got != tt.want {
				t.Fatalf("stripDiarizationMarkers(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
