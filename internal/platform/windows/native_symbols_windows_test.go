//go:build windows

package windows

import (
	"syscall"
	"testing"
)

func TestRequiredWindowsSymbolsResolve(t *testing.T) {
	t.Parallel()
	procedures := map[string]*syscall.LazyProc{
		"RtlMoveMemory":              procRtlMoveMemory,
		"GetAsyncKeyState":           procGetAsyncKeyState,
		"SetWindowsHookExW":          procSetWindowsHookExW,
		"CallNextHookEx":             procCallNextHookEx,
		"waveInOpen":                 procWaveInOpen,
		"waveInAddBuffer":            procWaveInAddBuffer,
		"SendInput":                  procSendInput,
		"OpenClipboard":              procOpenClipboard,
		"SetClipboardData":           procSetClipboardData,
		"RegisterClassExW":           procRegisterClassExW,
		"SetLayeredWindowAttributes": procSetLayeredWindowAttributes,
	}
	for name, procedure := range procedures {
		if err := procedure.Find(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
