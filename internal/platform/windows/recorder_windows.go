//go:build windows

package windows

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

const (
	waveMapper     = ^uint32(0)
	callbackEvent  = 0x00050000
	waveHeaderDone = 0x00000001

	waitObject0 = 0
	waitFailed  = 0xFFFFFFFF
	infinite    = 0xFFFFFFFF

	waveInCleanupAttempts   = 4
	waveInCleanupRetryDelay = 10 * time.Millisecond
	waveInPumpStopTimeout   = 2 * time.Second
)

var (
	winmm                      = syscall.NewLazyDLL("winmm.dll")
	procWaveInOpen             = winmm.NewProc("waveInOpen")
	procWaveInPrepareHeader    = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepareHeader  = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer        = winmm.NewProc("waveInAddBuffer")
	procWaveInStart            = winmm.NewProc("waveInStart")
	procWaveInStop             = winmm.NewProc("waveInStop")
	procWaveInReset            = winmm.NewProc("waveInReset")
	procWaveInClose            = winmm.NewProc("waveInClose")
	procWaveInGetErrorTextW    = winmm.NewProc("waveInGetErrorTextW")
	procCreateEventW           = kernel32.NewProc("CreateEventW")
	procSetEvent               = kernel32.NewProc("SetEvent")
	procCloseHandle            = kernel32.NewProc("CloseHandle")
	procWaitForMultipleObjects = kernel32.NewProc("WaitForMultipleObjects")
)

type waveHeader struct {
	data          uintptr
	bufferLength  uint32
	bytesRecorded uint32
	user          uintptr
	flags         uint32
	loops         uint32
	next          uintptr
	reserved      uintptr
}

// waveInNativeOps is deliberately a value owned by each Recorder. Besides
// keeping the syscall boundary in one place, this lets the ownership cleanup
// paths be exercised without opening a real capture device.
type waveInNativeOps struct {
	createEvent     func(manualReset bool) (uintptr, error)
	closeEvent      func(handle uintptr) error
	open            func(format *waveFormat, captureEvent uintptr) (uintptr, uint32)
	prepareHeader   func(handle uintptr, header *waveHeader) uint32
	unprepareHeader func(handle uintptr, header *waveHeader) uint32
	addBuffer       func(handle uintptr, header *waveHeader) uint32
	start           func(handle uintptr) uint32
	stop            func(handle uintptr) uint32
	reset           func(handle uintptr) uint32
	close           func(handle uintptr) uint32
	setEvent        func(handle uintptr) error
	waitMultiple    func(handles *[2]uintptr) (uint32, error)
	waitRetry       func(time.Duration)
}

func defaultWaveInNativeOps() waveInNativeOps {
	return waveInNativeOps{
		createEvent: func(manualReset bool) (uintptr, error) {
			manual := uintptr(0)
			if manualReset {
				manual = 1
			}
			handle, _, callErr := procCreateEventW.Call(0, manual, 0, 0)
			if handle == 0 {
				if callErr == nil || callErr == syscall.Errno(0) {
					callErr = errors.New("CreateEventW returned a null handle")
				}
				return 0, callErr
			}
			return handle, nil
		},
		closeEvent: func(handle uintptr) error {
			result, _, callErr := procCloseHandle.Call(handle)
			if result == 0 {
				if callErr == nil || callErr == syscall.Errno(0) {
					callErr = errors.New("CloseHandle returned failure without an error code")
				}
				return callErr
			}
			return nil
		},
		open: func(format *waveFormat, captureEvent uintptr) (uintptr, uint32) {
			var handle uintptr
			result, _, _ := procWaveInOpen.Call(
				uintptr(unsafe.Pointer(&handle)),
				uintptr(waveMapper),
				uintptr(unsafe.Pointer(format)),
				captureEvent,
				0,
				callbackEvent,
			)
			return handle, uint32(result)
		},
		prepareHeader: func(handle uintptr, header *waveHeader) uint32 {
			result, _, _ := procWaveInPrepareHeader.Call(handle, uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header))
			return uint32(result)
		},
		unprepareHeader: func(handle uintptr, header *waveHeader) uint32 {
			result, _, _ := procWaveInUnprepareHeader.Call(handle, uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header))
			return uint32(result)
		},
		addBuffer: func(handle uintptr, header *waveHeader) uint32 {
			result, _, _ := procWaveInAddBuffer.Call(handle, uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header))
			return uint32(result)
		},
		start: func(handle uintptr) uint32 {
			result, _, _ := procWaveInStart.Call(handle)
			return uint32(result)
		},
		stop: func(handle uintptr) uint32 {
			result, _, _ := procWaveInStop.Call(handle)
			return uint32(result)
		},
		reset: func(handle uintptr) uint32 {
			result, _, _ := procWaveInReset.Call(handle)
			return uint32(result)
		},
		close: func(handle uintptr) uint32 {
			result, _, _ := procWaveInClose.Call(handle)
			return uint32(result)
		},
		setEvent: func(handle uintptr) error {
			result, _, callErr := procSetEvent.Call(handle)
			if result == 0 {
				if callErr == nil || callErr == syscall.Errno(0) {
					callErr = errors.New("SetEvent returned failure without an error code")
				}
				return callErr
			}
			return nil
		},
		waitMultiple: func(handles *[2]uintptr) (uint32, error) {
			result, _, callErr := procWaitForMultipleObjects.Call(
				uintptr(len(handles)),
				uintptr(unsafe.Pointer(&handles[0])),
				0,
				infinite,
			)
			return uint32(result), callErr
		},
		waitRetry: time.Sleep,
	}
}

var quarantinedWaveInRecorders = struct {
	sync.Mutex
	recorders []*Recorder
}{}

// Recorder captures 16 kHz, signed 16-bit, mono PCM with WinMM waveIn. Four
// prepared buffers rotate through the driver; an event-backed pump copies
// completed buffers into a bounded Go queue so Stop can wake a blocked Read.
type Recorder struct {
	mu   sync.Mutex
	cond *sync.Cond
	ops  waveInNativeOps

	handle       uintptr
	captureEvent uintptr
	stopEvent    uintptr
	headers      []waveHeader
	samples      [][]byte
	processed    []bool
	prepared     int
	pinner       runtime.Pinner
	pinned       bool

	started   bool
	stopping  bool
	closed    bool
	tailTaken bool
	buffer    []byte
	pumpErr   error
	pumpDone  chan struct{}
	stopDone  chan struct{}
	stopErr   error
	closeErr  error

	pumpStopTimeout time.Duration

	quarantined   bool
	quarantineErr error
}

var _ platform.Recorder = (*Recorder)(nil)

func NewRecorder() *Recorder {
	recorder := &Recorder{
		ops:             defaultWaveInNativeOps(),
		pumpStopTimeout: waveInPumpStopTimeout,
	}
	recorder.cond = sync.NewCond(&recorder.mu)
	return recorder
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("windows recorder: closed")
	}
	if r.quarantined {
		return r.quarantineErr
	}
	if r.started {
		return fmt.Errorf("windows recorder: already started")
	}

	captureEvent, callErr := r.ops.createEvent(false)
	if callErr != nil || captureEvent == 0 {
		if callErr == nil {
			callErr = errors.New("CreateEventW returned a null capture event")
		}
		return fmt.Errorf("windows recorder: create capture event: %v", callErr)
	}
	r.captureEvent = captureEvent
	stopEvent, callErr := r.ops.createEvent(true)
	if callErr != nil || stopEvent == 0 {
		if callErr == nil {
			callErr = errors.New("CreateEventW returned a null stop event")
		}
		startErr := fmt.Errorf("windows recorder: create stop event: %v", callErr)
		return errors.Join(startErr, r.releaseNativeLocked())
	}
	r.stopEvent = stopEvent

	format := defaultWindowsWaveFormat()
	handle, result := r.ops.open(&format, captureEvent)
	if result != 0 || handle == 0 {
		if result == 0 {
			startErr := errors.New("windows recorder: waveInOpen returned a null device handle")
			return errors.Join(startErr, r.releaseNativeLocked())
		}
		startErr := waveInError("waveInOpen", result)
		return errors.Join(startErr, r.releaseNativeLocked())
	}

	r.handle = handle
	r.headers = make([]waveHeader, windowsRecorderBuffers)
	r.samples = make([][]byte, windowsRecorderBuffers)
	r.processed = make([]bool, windowsRecorderBuffers)
	// waveIn retains both WAVEHDR and lpData pointers until unprepare/close.
	// Pin their Go backing objects for that entire native ownership window.
	r.pinner.Pin(&r.headers[0])
	r.pinned = true
	r.prepared = 0
	for index := range r.headers {
		r.samples[index] = make([]byte, windowsRecorderChunkBytes)
		r.pinner.Pin(&r.samples[index][0])
		r.headers[index].data = uintptr(unsafe.Pointer(&r.samples[index][0]))
		r.headers[index].bufferLength = uint32(len(r.samples[index]))
		if err := r.prepareAndQueueLocked(index); err != nil {
			return errors.Join(err, r.releaseNativeLocked())
		}
	}

	if result := r.ops.start(r.handle); result != 0 {
		startErr := waveInError("waveInStart", result)
		return errors.Join(startErr, r.releaseNativeLocked())
	}
	r.started = true
	r.stopping = false
	r.tailTaken = false
	r.buffer = nil
	r.pumpErr = nil
	r.stopErr = nil
	r.stopDone = nil
	r.pumpDone = make(chan struct{})
	go r.pump()
	runtime.KeepAlive(format)
	return nil
}

func (r *Recorder) prepareAndQueueLocked(index int) error {
	header := &r.headers[index]
	if result := r.ops.prepareHeader(r.handle, header); result != 0 {
		return waveInError("waveInPrepareHeader", result)
	}
	r.prepared++
	if result := r.ops.addBuffer(r.handle, header); result != 0 {
		return waveInError("waveInAddBuffer", result)
	}
	return nil
}

func (r *Recorder) Read(destination []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return 0, fmt.Errorf("windows recorder: not started")
	}
	for len(r.buffer) == 0 && !r.stopping && !r.closed && r.pumpErr == nil {
		r.cond.Wait()
	}
	if r.stopping || r.closed {
		return 0, io.EOF
	}
	if len(r.buffer) == 0 {
		if r.pumpErr != nil {
			return 0, r.pumpErr
		}
		return 0, io.EOF
	}
	count := len(destination)
	if count > len(r.buffer) {
		count = len(r.buffer)
	}
	copy(destination, r.buffer[:count])
	r.buffer = r.buffer[count:]
	r.cond.Broadcast()
	return count, nil
}

func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil, nil
	}
	first := r.stopDone == nil
	if first {
		r.stopping = true
		r.stopDone = make(chan struct{})
		r.cond.Broadcast()
	}
	done := r.stopDone
	r.mu.Unlock()

	if first {
		r.finishStop()
	}
	<-done

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tailTaken {
		return nil, errors.Join(r.stopErr, r.pumpErr)
	}
	tail := append([]byte(nil), r.buffer...)
	r.buffer = nil
	r.tailTaken = true
	return tail, errors.Join(r.stopErr, r.pumpErr)
}

func (r *Recorder) finishStop() {
	r.mu.Lock()
	handle := r.handle
	stopEvent := r.stopEvent
	pumpDone := r.pumpDone
	r.mu.Unlock()

	var errs []error
	if result := r.ops.stop(handle); result != 0 {
		errs = append(errs, waveInError("waveInStop", result))
	}
	// Reset returns every queued WAVEHDR to the application before the stop
	// event is signalled. The pump then copies all final WHDR_DONE buffers once.
	if result := r.ops.reset(handle); result != 0 {
		errs = append(errs, waveInError("waveInReset", result))
	}
	if err := r.ops.setEvent(stopEvent); err != nil {
		errs = append(errs, fmt.Errorf("windows recorder: signal stop event: %w", err))
	}
	if !r.waitForPumpStop(pumpDone) {
		r.mu.Lock()
		pumpErr := fmt.Errorf("windows recorder: capture pump did not stop within %s", r.effectivePumpStopTimeout())
		errs = append(errs, pumpErr)
		quarantineErr := r.quarantineNativeLocked(errors.Join(errs...))
		r.stopErr = quarantineErr
		close(r.stopDone)
		r.mu.Unlock()
		return
	}
	// A callback event may have been coalesced while reset returned multiple
	// buffers. Scan once more after the pump has stopped so the tail is complete.
	r.collectCompleted(true)

	r.mu.Lock()
	r.stopErr = errors.Join(errs...)
	close(r.stopDone)
	r.mu.Unlock()
}

func (r *Recorder) waitForPumpStop(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	timer := time.NewTimer(r.effectivePumpStopTimeout())
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		// Prefer a completion racing with the timer over unnecessary permanent
		// quarantine. If it is still not observable, ownership remains uncertain.
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
}

func (r *Recorder) effectivePumpStopTimeout() time.Duration {
	if r.pumpStopTimeout > 0 {
		return r.pumpStopTimeout
	}
	return waveInPumpStopTimeout
}

func (r *Recorder) Close() error {
	_, stopErr := r.Stop()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.Join(stopErr, r.closeErr)
	}
	r.closed = true
	r.cond.Broadcast()
	if r.quarantined {
		r.closeErr = r.quarantineErr
	} else {
		r.closeErr = r.releaseNativeLocked()
	}
	return errors.Join(stopErr, r.closeErr)
}

func (r *Recorder) pump() {
	defer func() {
		r.mu.Lock()
		close(r.pumpDone)
		r.cond.Broadcast()
		r.mu.Unlock()
	}()

	r.mu.Lock()
	handles := [2]uintptr{r.captureEvent, r.stopEvent}
	r.mu.Unlock()
	for {
		result, callErr := r.ops.waitMultiple(&handles)
		switch result {
		case waitObject0:
			r.collectCompleted(false)
		case waitObject0 + 1:
			r.collectCompleted(true)
			return
		case waitFailed:
			r.mu.Lock()
			r.pumpErr = errors.Join(r.pumpErr, fmt.Errorf("windows recorder: WaitForMultipleObjects: %v", callErr))
			r.mu.Unlock()
			return
		default:
			r.mu.Lock()
			r.pumpErr = errors.Join(r.pumpErr, fmt.Errorf("windows recorder: unexpected wait result %#x", result))
			r.mu.Unlock()
			return
		}
	}
}

func (r *Recorder) collectCompleted(final bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.headers {
		header := &r.headers[index]
		if header.flags&waveHeaderDone == 0 || r.processed[index] {
			continue
		}
		r.processed[index] = true
		count := int(header.bytesRecorded)
		if count > len(r.samples[index]) {
			count = len(r.samples[index])
		}
		if count > 0 {
			r.appendCapturedLocked(r.samples[index][:count])
		}
		header.bytesRecorded = 0
		if !final && !r.stopping && !r.closed {
			if result := r.ops.addBuffer(r.handle, header); result != 0 {
				r.pumpErr = errors.Join(r.pumpErr, waveInError("waveInAddBuffer", result))
			} else {
				r.processed[index] = false
			}
		}
	}
	r.cond.Broadcast()
}

func (r *Recorder) appendCapturedLocked(chunk []byte) {
	for len(r.buffer)+len(chunk) > windowsRecorderBufferLimit && !r.stopping && !r.closed {
		r.cond.Wait()
	}
	available := windowsRecorderBufferLimit - len(r.buffer)
	if available < len(chunk) {
		r.pumpErr = errors.Join(r.pumpErr, fmt.Errorf("windows recorder: audio buffer limit exceeded"))
		if available <= 0 {
			return
		}
		chunk = chunk[:available]
	}
	r.buffer = append(r.buffer, chunk...)
}

// releaseNativeLocked is the single native-ownership exit used both by Close
// and by every Start failure after the first event has been created. WinMM may
// retain WAVEHDR and lpData pointers until each header is unprepared and the
// device handle is closed. Therefore no event is closed, no Go object is
// unpinned, and no backing slice is cleared until both steps have succeeded.
//
// The caller must hold r.mu and no pump goroutine may still be running.
func (r *Recorder) releaseNativeLocked() error {
	if r.quarantined {
		return r.quarantineErr
	}

	var cleanupErr error
	if r.handle != 0 {
		resetErr, unprepareErr := r.unprepareHeadersLocked()
		if unprepareErr != nil {
			return r.quarantineNativeLocked(errors.Join(resetErr, unprepareErr))
		}
		cleanupErr = errors.Join(cleanupErr, resetErr)

		if closeErr := r.closeWaveInLocked(); closeErr != nil {
			return r.quarantineNativeLocked(errors.Join(cleanupErr, closeErr))
		}
		r.handle = 0
	}

	var eventErrs []error
	if r.captureEvent != 0 {
		if err := r.ops.closeEvent(r.captureEvent); err != nil {
			eventErrs = append(eventErrs, fmt.Errorf("windows recorder: close capture event: %w", err))
		} else {
			r.captureEvent = 0
		}
	}
	if r.stopEvent != 0 {
		if err := r.ops.closeEvent(r.stopEvent); err != nil {
			eventErrs = append(eventErrs, fmt.Errorf("windows recorder: close stop event: %w", err))
		} else {
			r.stopEvent = 0
		}
	}
	if eventErr := errors.Join(eventErrs...); eventErr != nil {
		return r.quarantineNativeLocked(errors.Join(cleanupErr, eventErr))
	}

	if r.pinned {
		r.pinner.Unpin()
		r.pinned = false
	}
	r.headers = nil
	r.samples = nil
	r.processed = nil
	r.prepared = 0
	r.buffer = nil
	return cleanupErr
}

func (r *Recorder) unprepareHeadersLocked() (error, error) {
	if r.prepared == 0 {
		return nil, nil
	}

	prepared := r.prepared
	if prepared > len(r.headers) {
		prepared = len(r.headers)
	}
	var lastResetErr error
	var lastUnprepareErr error
	for attempt := 0; attempt < waveInCleanupAttempts; attempt++ {
		if result := r.ops.reset(r.handle); result != 0 {
			lastResetErr = waveInError("waveInReset during cleanup", result)
		} else {
			lastResetErr = nil
		}

		var errs []error
		for index := 0; index < prepared; index++ {
			if result := r.ops.unprepareHeader(r.handle, &r.headers[index]); result != 0 {
				errs = append(errs, fmt.Errorf("header %d: %w", index, waveInError("waveInUnprepareHeader", result)))
			}
		}
		lastUnprepareErr = errors.Join(errs...)
		if lastUnprepareErr == nil {
			r.prepared = 0
			return lastResetErr, nil
		}
		if attempt+1 < waveInCleanupAttempts {
			r.ops.waitRetry(waveInCleanupRetryDelay)
		}
	}
	return lastResetErr, fmt.Errorf("windows recorder: headers still owned after %d cleanup attempts: %w", waveInCleanupAttempts, lastUnprepareErr)
}

func (r *Recorder) closeWaveInLocked() error {
	var lastCloseErr error
	var lastResetErr error
	for attempt := 0; attempt < waveInCleanupAttempts; attempt++ {
		if result := r.ops.close(r.handle); result == 0 {
			return nil
		} else {
			lastCloseErr = waveInError("waveInClose", result)
		}
		if attempt+1 < waveInCleanupAttempts {
			if result := r.ops.reset(r.handle); result != 0 {
				lastResetErr = waveInError("waveInReset before close retry", result)
			} else {
				lastResetErr = nil
			}
			r.ops.waitRetry(waveInCleanupRetryDelay)
		}
	}
	return fmt.Errorf("windows recorder: device still open after %d cleanup attempts: %w", waveInCleanupAttempts, errors.Join(lastResetErr, lastCloseErr))
}

func (r *Recorder) quarantineNativeLocked(cause error) error {
	if r.quarantined {
		return r.quarantineErr
	}
	r.quarantined = true
	r.quarantineErr = fmt.Errorf(
		"windows recorder: native cleanup incomplete; resources quarantined to prevent use-after-free; all still-live handles, headers, sample buffers, and pins will remain reachable: %w",
		cause,
	)
	quarantinedWaveInRecorders.Lock()
	quarantinedWaveInRecorders.recorders = append(quarantinedWaveInRecorders.recorders, r)
	quarantinedWaveInRecorders.Unlock()
	return r.quarantineErr
}

func waveInError(operation string, code uint32) error {
	buffer := make([]uint16, 256)
	result, _, _ := procWaveInGetErrorTextW.Call(uintptr(code), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if result == 0 {
		if message := syscall.UTF16ToString(buffer); message != "" {
			return fmt.Errorf("windows recorder: %s: %s (MMRESULT %d)", operation, message, code)
		}
	}
	return fmt.Errorf("windows recorder: %s failed (MMRESULT %d)", operation, code)
}
