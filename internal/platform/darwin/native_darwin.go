//go:build darwin && cgo

package darwin

/*
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore -framework ApplicationServices -framework AudioToolbox -framework CoreFoundation
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type nativeEventTap struct {
	value *C.eloqi_event_tap
}

type nativeKeyEvent struct {
	keycode uint16
	pressed bool
}

func createNativeEventTap() (*nativeEventTap, error) {
	value := C.eloqi_event_tap_create()
	if value == nil {
		return nil, fmt.Errorf("darwin hotkey: CGEventTapCreate failed (grant Input Monitoring and Accessibility access to the launching terminal)")
	}
	return &nativeEventTap{value: value}, nil
}

func (tap *nativeEventTap) next(timeoutSeconds float64) (nativeKeyEvent, bool, error) {
	var event C.eloqi_key_event
	result := int(C.eloqi_event_tap_next(tap.value, C.double(timeoutSeconds), &event))
	switch result {
	case 0:
		return nativeKeyEvent{}, false, nil
	case 1:
		return nativeKeyEvent{keycode: uint16(event.keycode), pressed: event.pressed != 0}, true, nil
	default:
		return nativeKeyEvent{}, false, fmt.Errorf("darwin hotkey: event tap stopped")
	}
}

func (tap *nativeEventTap) close() {
	if tap != nil && tap.value != nil {
		C.eloqi_event_tap_destroy(tap.value)
		tap.value = nil
	}
}

type nativeAudioCapture struct {
	value *C.eloqi_audio_capture
}

func createNativeAudioCapture() (*nativeAudioCapture, error) {
	var value *C.eloqi_audio_capture
	status := int32(C.eloqi_audio_create(&value))
	if status != 0 {
		return nil, darwinStatusError("AudioQueueNewInput", status)
	}
	return &nativeAudioCapture{value: value}, nil
}

func (capture *nativeAudioCapture) start() error {
	status := int32(C.eloqi_audio_start(capture.value))
	if status != 0 {
		return darwinStatusError("AudioQueueStart", status)
	}
	return nil
}

func (capture *nativeAudioCapture) read(destination []byte) (int, bool, error) {
	if len(destination) == 0 {
		return 0, false, nil
	}
	var pointer *C.uint8_t
	pointer = (*C.uint8_t)(unsafe.Pointer(&destination[0]))
	var count C.size_t
	status := int32(C.eloqi_audio_read(
		capture.value,
		pointer,
		C.size_t(len(destination)),
		&count,
	))
	if status == 1 {
		return 0, true, nil
	}
	if status != 0 {
		return int(count), false, darwinStatusError("AudioQueue input callback", status)
	}
	return int(count), false, nil
}

func (capture *nativeAudioCapture) stop() ([]byte, error) {
	var tail *C.uint8_t
	var length C.size_t
	status := int32(C.eloqi_audio_stop(capture.value, &tail, &length))
	if tail != nil {
		defer C.free(unsafe.Pointer(tail))
	}
	var bytes []byte
	if length != 0 {
		bytes = C.GoBytes(unsafe.Pointer(tail), C.int(length))
	}
	if status != 0 {
		return bytes, darwinStatusError("AudioQueueStop", status)
	}
	return bytes, nil
}

func (capture *nativeAudioCapture) close() error {
	if capture == nil || capture.value == nil {
		return nil
	}
	status := int32(C.eloqi_audio_close(capture.value))
	capture.value = nil
	if status != 0 {
		return darwinStatusError("AudioQueueDispose", status)
	}
	return nil
}

func nativeClipboardRead() (string, error) {
	var bytes *C.uint8_t
	var length C.size_t
	if C.eloqi_clipboard_read(&bytes, &length) == 0 {
		return "", fmt.Errorf("darwin clipboard: NSPasteboard read failed")
	}
	if bytes != nil {
		defer C.free(unsafe.Pointer(bytes))
	}
	if length == 0 {
		return "", nil
	}
	return string(C.GoBytes(unsafe.Pointer(bytes), C.int(length))), nil
}

func nativeClipboardWrite(text string) error {
	bytes := []byte(text)
	var pointer *C.uint8_t
	if len(bytes) != 0 {
		pointer = (*C.uint8_t)(unsafe.Pointer(&bytes[0]))
	}
	if C.eloqi_clipboard_write(pointer, C.size_t(len(bytes))) == 0 {
		return fmt.Errorf("darwin clipboard: NSPasteboard write failed")
	}
	return nil
}

func nativePostPaste() error {
	switch int(C.eloqi_post_paste()) {
	case 0:
		return nil
	case -1:
		return fmt.Errorf("darwin autotype: Accessibility permission is required for CGEventPost")
	default:
		return fmt.Errorf("darwin autotype: could not create native keyboard events")
	}
}

func runNativeOverlayHelper() {
	C.eloqi_overlay_run_helper()
}

func darwinStatusError(operation string, status int32) error {
	if status == -66701 {
		return fmt.Errorf("darwin recorder: %s: audio buffer limit exceeded", operation)
	}
	return fmt.Errorf("darwin recorder: %s failed (OSStatus %d)", operation, status)
}
