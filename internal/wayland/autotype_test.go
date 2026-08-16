package wayland

import "testing"

func TestAutotypeBackendForDesktop(t *testing.T) {
	tests := []struct {
		desktop string
		want    AutotypeBackend
	}{
		{"ubuntu:GNOME", AutotypeYdotool},
		{"GNOME", AutotypeYdotool},
		{"KDE", AutotypeYdotool},
		{"KDE;Plasma", AutotypeYdotool},
		{"kwin", AutotypeYdotool},
		{"ubuntu", AutotypeYdotool},
		{"sway", AutotypeWtype},
		{"Hyprland", AutotypeWtype},
		{"genome", AutotypeWtype},
		{"", AutotypeWtype},
	}
	for _, tt := range tests {
		t.Run(tt.desktop, func(t *testing.T) {
			if got := AutotypeBackendForDesktop(tt.desktop); got != tt.want {
				t.Fatalf("AutotypeBackendForDesktop(%q) = %q, want %q", tt.desktop, got, tt.want)
			}
		})
	}
}
