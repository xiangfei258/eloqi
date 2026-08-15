package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type fakeHost struct {
	env     map[string]string
	found   map[string]string
	looked  []string
	devices []string
	denied  map[string]error
	opened  []string
	keyCaps map[string]string
}

func (h *fakeHost) getenv(key string) string {
	return h.env[key]
}

func (h *fakeHost) lookPath(name string) (string, error) {
	h.looked = append(h.looked, name)
	if path, ok := h.found[name]; ok {
		return path, nil
	}
	return "", errors.New("not found")
}

func (h *fakeHost) glob(string) ([]string, error) {
	return append([]string(nil), h.devices...), nil
}

func (h *fakeHost) open(path string) (io.ReadCloser, error) {
	h.opened = append(h.opened, path)
	if err := h.denied[path]; err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (h *fakeHost) readFile(path string) ([]byte, error) {
	if capability, ok := h.keyCaps[path]; ok {
		return []byte(capability), nil
	}
	return []byte("80002"), nil
}

func findingByID(t *testing.T, report Report, id string) Finding {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.ID == id {
			return finding
		}
	}
	t.Fatalf("finding %q not present in %#v", id, report.Findings)
	return Finding{}
}

func TestCheckLinuxWaylandReportsActionableMissingDependencies(t *testing.T) {
	host := &fakeHost{
		env:     map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
		found:   map[string]string{"arecord": "/usr/bin/arecord", "wl-paste": "/usr/bin/wl-paste"},
		devices: []string{"/dev/input/event2"},
	}
	report := Check(Options{
		GOOS:     "linux",
		Getenv:   host.getenv,
		LookPath: host.lookPath,
		Glob:     host.glob,
		Open:     host.open,
		ReadFile: host.readFile,
	})
	if report.OK() {
		t.Fatal("report should fail when required wl-copy is missing")
	}
	missing := findingByID(t, report, "clipboard.wl-copy")
	if missing.Status != StatusError || !missing.Required {
		t.Fatalf("wl-copy finding = %#v", missing)
	}
	if !strings.Contains(missing.Hint, "Install wl-clipboard") {
		t.Fatalf("hint is not actionable: %q", missing.Hint)
	}
	optional := findingByID(t, report, "autotype.wtype")
	if optional.Status != StatusWarning || optional.Required {
		t.Fatalf("optional wtype finding = %#v", optional)
	}
	overlay := findingByID(t, report, "overlay.notify-send")
	if overlay.Status != StatusWarning || !strings.Contains(overlay.Hint, "libnotify-bin") {
		t.Fatalf("optional overlay finding = %#v", overlay)
	}
	if strings.Contains(strings.Join(host.looked, ","), "xclip") {
		t.Fatalf("Wayland check unexpectedly probed X11 tools: %v", host.looked)
	}
	if err := report.Error(); err == nil || !strings.Contains(err.Error(), "wl-copy") {
		t.Fatalf("Report.Error() = %v", err)
	}
}

func TestCheckLinuxX11WithRequiredAutoType(t *testing.T) {
	host := &fakeHost{
		env: map[string]string{"DISPLAY": ":1"},
		found: map[string]string{
			"arecord": "/bin/arecord",
			"xclip":   "/bin/xclip",
			"xdotool": "/bin/xdotool",
		},
	}
	report := Check(Options{
		GOOS:     "linux",
		Getenv:   host.getenv,
		LookPath: host.lookPath,
		Glob: func(string) ([]string, error) {
			t.Fatal("pure X11 must not probe evdev devices")
			return nil, nil
		},
		Open: func(string) (io.ReadCloser, error) {
			t.Fatal("pure X11 must not open evdev devices")
			return nil, nil
		},
		RequireAutoType: true,
	})
	if !report.OK() {
		t.Fatalf("healthy X11 report failed: %v", report.Error())
	}
	autotype := findingByID(t, report, "autotype.xdotool")
	if !autotype.Required || autotype.Status != StatusOK {
		t.Fatalf("autotype finding = %#v", autotype)
	}
	wantLooked := "arecord,xclip,xdotool"
	if got := strings.Join(host.looked, ","); got != wantLooked {
		t.Fatalf("LookPath calls = %q, want %q", got, wantLooked)
	}
}

func TestCheckLinuxWaylandRequiresReadableEvdevDevice(t *testing.T) {
	tests := []struct {
		name    string
		devices []string
		denied  map[string]error
		keyCaps map[string]string
		wantOK  bool
	}{
		{
			name:    "one readable device is enough",
			devices: []string{"/dev/input/event0", "/dev/input/event4"},
			denied:  map[string]error{"/dev/input/event0": errors.New("permission denied")},
			wantOK:  true,
		},
		{
			name:    "all devices denied",
			devices: []string{"/dev/input/event0", "/dev/input/event4"},
			denied: map[string]error{
				"/dev/input/event0": errors.New("permission denied"),
				"/dev/input/event4": errors.New("permission denied"),
			},
		},
		{
			name:    "readable mouse is not a keyboard",
			devices: []string{"/dev/input/event4"},
			keyCaps: map[string]string{
				"/sys/class/input/event4/device/capabilities/key": "2",
			},
		},
		{name: "no event devices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &fakeHost{
				env:     map[string]string{"WAYLAND_DISPLAY": "wayland-1"},
				found:   map[string]string{"arecord": "/bin/arecord", "wl-copy": "/bin/wl-copy", "wl-paste": "/bin/wl-paste"},
				devices: tt.devices,
				denied:  tt.denied,
				keyCaps: tt.keyCaps,
			}
			report := Check(Options{
				GOOS:     "linux",
				Getenv:   host.getenv,
				LookPath: host.lookPath,
				Glob:     host.glob,
				Open:     host.open,
				ReadFile: host.readFile,
			})
			finding := findingByID(t, report, "hotkey.evdev-access")
			if tt.wantOK {
				if finding.Status != StatusOK || !finding.Required || !report.OK() {
					t.Fatalf("readable evdev finding = %#v, report error = %v", finding, report.Error())
				}
				if got := strings.Join(host.opened, ","); got != strings.Join(tt.devices, ",") {
					t.Fatalf("opened devices = %q, want probes through first readable device", got)
				}
				return
			}
			if finding.Status != StatusError || !finding.Required || report.OK() {
				t.Fatalf("unreadable evdev finding = %#v", finding)
			}
			if !strings.Contains(finding.Hint, "input group") || !strings.Contains(finding.Hint, "sign out and back in") {
				t.Fatalf("evdev remediation is not actionable: %q", finding.Hint)
			}
		})
	}
}

func TestCheckLinuxWithoutDesktopSession(t *testing.T) {
	host := &fakeHost{env: map[string]string{}, found: map[string]string{"arecord": "/bin/arecord"}}
	report := Check(Options{GOOS: "linux", Getenv: host.getenv, LookPath: host.lookPath})
	finding := findingByID(t, report, "display")
	if finding.Status != StatusError || !strings.Contains(finding.Hint, "WAYLAND_DISPLAY or DISPLAY") {
		t.Fatalf("display finding = %#v", finding)
	}
}

func TestNativePlatformsDoNotProbePath(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			calls := 0
			report := Check(Options{
				GOOS:   goos,
				Getenv: func(string) string { return "" },
				LookPath: func(string) (string, error) {
					calls++
					return "", errors.New("unexpected")
				},
			})
			if !report.OK() {
				t.Fatalf("native report should not fail: %v", report.Error())
			}
			if calls != 0 {
				t.Fatalf("LookPath called %d times", calls)
			}
			if findingByID(t, report, "runtime.native").Status != StatusOK {
				t.Fatal("missing native runtime success")
			}
		})
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	report := Check(Options{GOOS: "plan9"})
	if report.OK() {
		t.Fatal("unsupported platform should fail")
	}
	if got := report.Error(); got == nil || !strings.Contains(got.Error(), "plan9") {
		t.Fatalf("Report.Error() = %v", got)
	}
}

func TestReportWriteTo(t *testing.T) {
	report := Report{Findings: []Finding{
		{ID: "one", Status: StatusOK, Message: "ready"},
		{ID: "two", Status: StatusWarning, Message: "manual check", Hint: "Do the thing."},
	}}
	var output bytes.Buffer
	written, err := report.WriteTo(&output)
	if err != nil {
		t.Fatal(err)
	}
	want := "[ok] one: ready\n[warning] two: manual check\n  Fix: Do the thing.\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if written != int64(len(want)) {
		t.Fatalf("written = %d, want %d", written, len(want))
	}

	errWriter := writerFunc(func([]byte) (int, error) { return 0, fmt.Errorf("write failed") })
	if _, err := report.WriteTo(errWriter); err == nil {
		t.Fatal("expected writer error")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
