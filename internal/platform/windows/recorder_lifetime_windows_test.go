//go:build windows

package windows

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testCaptureEvent = uintptr(0x101)
	testStopEvent    = uintptr(0x102)
	testWaveInHandle = uintptr(0x201)
	testStillPlaying = uint32(33) // WAVERR_STILLPLAYING
)

type fakeWaveInNative struct {
	startResult uint32
	setEventErr error

	resetCalls      int
	unprepareCalls  int
	closeCalls      int
	retryWaits      int
	closedEvents    []uintptr
	unprepareResult func(call int, header *waveHeader) uint32
	closeResult     func(call int) uint32
	waitResult      func(handles *[2]uintptr) (uint32, error)
}

func (fake *fakeWaveInNative) ops() waveInNativeOps {
	return waveInNativeOps{
		createEvent: func(manualReset bool) (uintptr, error) {
			if manualReset {
				return testStopEvent, nil
			}
			return testCaptureEvent, nil
		},
		closeEvent: func(handle uintptr) error {
			fake.closedEvents = append(fake.closedEvents, handle)
			return nil
		},
		open: func(*waveFormat, uintptr) (uintptr, uint32) {
			return testWaveInHandle, 0
		},
		prepareHeader: func(uintptr, *waveHeader) uint32 {
			return 0
		},
		unprepareHeader: func(_ uintptr, header *waveHeader) uint32 {
			fake.unprepareCalls++
			if fake.unprepareResult != nil {
				return fake.unprepareResult(fake.unprepareCalls, header)
			}
			return 0
		},
		addBuffer: func(uintptr, *waveHeader) uint32 {
			return 0
		},
		start: func(uintptr) uint32 {
			return fake.startResult
		},
		stop: func(uintptr) uint32 {
			return 0
		},
		reset: func(uintptr) uint32 {
			fake.resetCalls++
			return 0
		},
		close: func(uintptr) uint32 {
			fake.closeCalls++
			if fake.closeResult != nil {
				return fake.closeResult(fake.closeCalls)
			}
			return 0
		},
		setEvent: func(uintptr) error {
			return fake.setEventErr
		},
		waitMultiple: func(handles *[2]uintptr) (uint32, error) {
			if fake.waitResult != nil {
				return fake.waitResult(handles)
			}
			return waitFailed, errors.New("unexpected wait")
		},
		waitRetry: func(time.Duration) {
			fake.retryWaits++
		},
	}
}

func seedFakeNativeOwnership(recorder *Recorder, fake *fakeWaveInNative, prepared int) {
	recorder.ops = fake.ops()
	recorder.handle = testWaveInHandle
	recorder.captureEvent = testCaptureEvent
	recorder.stopEvent = testStopEvent
	recorder.headers = make([]waveHeader, prepared)
	recorder.samples = make([][]byte, prepared)
	recorder.processed = make([]bool, prepared)
	for index := range recorder.samples {
		recorder.samples[index] = []byte{byte(index + 1)}
	}
	recorder.prepared = prepared
	// These failure tests intentionally use the ownership marker without
	// pinning test memory. If cleanup accidentally takes the success path,
	// pinned will be cleared and the assertion below will catch it.
	recorder.pinned = true
}

func assertRecorderBackingRetained(t *testing.T, recorder *Recorder, prepared int) {
	t.Helper()
	if recorder.handle != testWaveInHandle || recorder.captureEvent != testCaptureEvent || recorder.stopEvent != testStopEvent {
		t.Fatalf("native handles were released after failed cleanup: handle=%#x capture=%#x stop=%#x", recorder.handle, recorder.captureEvent, recorder.stopEvent)
	}
	if len(recorder.headers) != prepared || len(recorder.samples) != prepared || len(recorder.processed) != prepared {
		t.Fatalf("native backing was cleared after failed cleanup: headers=%d samples=%d processed=%d", len(recorder.headers), len(recorder.samples), len(recorder.processed))
	}
	if !recorder.pinned {
		t.Fatal("native backing was unpinned after failed cleanup")
	}
	if !recorder.quarantined || recorder.quarantineErr == nil {
		t.Fatal("failed native cleanup did not quarantine the recorder")
	}
}

func TestRecorderCleanupQuarantinesOnPersistentUnprepareFailure(t *testing.T) {
	fake := &fakeWaveInNative{
		unprepareResult: func(int, *waveHeader) uint32 { return testStillPlaying },
	}
	recorder := NewRecorder()
	seedFakeNativeOwnership(recorder, fake, 2)

	recorder.mu.Lock()
	err := recorder.releaseNativeLocked()
	recorder.mu.Unlock()

	if err == nil || !strings.Contains(err.Error(), "resources quarantined") || !strings.Contains(err.Error(), "waveInUnprepareHeader") {
		t.Fatalf("cleanup error = %v, want explicit unprepare quarantine error", err)
	}
	if fake.resetCalls != waveInCleanupAttempts {
		t.Fatalf("reset calls = %d, want %d", fake.resetCalls, waveInCleanupAttempts)
	}
	if fake.unprepareCalls != 2*waveInCleanupAttempts {
		t.Fatalf("unprepare calls = %d, want %d", fake.unprepareCalls, 2*waveInCleanupAttempts)
	}
	if fake.retryWaits != waveInCleanupAttempts-1 {
		t.Fatalf("retry waits = %d, want %d", fake.retryWaits, waveInCleanupAttempts-1)
	}
	if fake.closeCalls != 0 || len(fake.closedEvents) != 0 {
		t.Fatalf("unsafe cleanup continued after unprepare failure: close=%d events=%v", fake.closeCalls, fake.closedEvents)
	}
	assertRecorderBackingRetained(t, recorder, 2)
	if recorder.prepared != 2 {
		t.Fatalf("prepared = %d, want ownership count retained", recorder.prepared)
	}

	// Quarantine is final: subsequent Close calls return the same diagnosis and
	// never make another native release attempt against uncertain ownership.
	firstCloseErr := recorder.Close()
	secondCloseErr := recorder.Close()
	if firstCloseErr == nil || secondCloseErr == nil {
		t.Fatalf("Close errors = (%v, %v), want quarantine error", firstCloseErr, secondCloseErr)
	}
	if fake.resetCalls != waveInCleanupAttempts || fake.unprepareCalls != 2*waveInCleanupAttempts {
		t.Fatal("Close retried native cleanup after recorder entered quarantine")
	}
}

func TestRecorderCleanupQuarantinesOnPersistentCloseFailure(t *testing.T) {
	fake := &fakeWaveInNative{
		closeResult: func(int) uint32 { return testStillPlaying },
	}
	recorder := NewRecorder()
	seedFakeNativeOwnership(recorder, fake, 2)

	recorder.mu.Lock()
	err := recorder.releaseNativeLocked()
	recorder.mu.Unlock()

	if err == nil || !strings.Contains(err.Error(), "resources quarantined") || !strings.Contains(err.Error(), "waveInClose") {
		t.Fatalf("cleanup error = %v, want explicit close quarantine error", err)
	}
	if fake.unprepareCalls != 2 {
		t.Fatalf("unprepare calls = %d, want 2", fake.unprepareCalls)
	}
	if fake.closeCalls != waveInCleanupAttempts {
		t.Fatalf("close calls = %d, want %d", fake.closeCalls, waveInCleanupAttempts)
	}
	if fake.retryWaits != waveInCleanupAttempts-1 {
		t.Fatalf("retry waits = %d, want %d", fake.retryWaits, waveInCleanupAttempts-1)
	}
	if len(fake.closedEvents) != 0 {
		t.Fatalf("events closed while waveInClose still failed: %v", fake.closedEvents)
	}
	assertRecorderBackingRetained(t, recorder, 2)
}

func TestRecorderStartFailureQuarantinesPinnedBacking(t *testing.T) {
	fake := &fakeWaveInNative{
		startResult:     5,
		unprepareResult: func(int, *waveHeader) uint32 { return testStillPlaying },
	}
	recorder := NewRecorder()
	recorder.ops = fake.ops()

	err := recorder.Start()
	if err == nil || !strings.Contains(err.Error(), "waveInStart") || !strings.Contains(err.Error(), "resources quarantined") {
		t.Fatalf("Start error = %v, want start failure plus quarantine diagnosis", err)
	}
	if fake.closeCalls != 0 || len(fake.closedEvents) != 0 {
		t.Fatalf("Start failure released resources still owned by WinMM: close=%d events=%v", fake.closeCalls, fake.closedEvents)
	}
	assertRecorderBackingRetained(t, recorder, windowsRecorderBuffers)
	if recorder.prepared != windowsRecorderBuffers {
		t.Fatalf("prepared = %d, want %d", recorder.prepared, windowsRecorderBuffers)
	}
}

func TestRecorderStartFailureUsesRetriedOwnershipCleanup(t *testing.T) {
	fake := &fakeWaveInNative{startResult: 5}
	fake.unprepareResult = func(call int, _ *waveHeader) uint32 {
		if call == 1 {
			return testStillPlaying
		}
		return 0
	}
	recorder := NewRecorder()
	recorder.ops = fake.ops()

	err := recorder.Start()
	if err == nil || !strings.Contains(err.Error(), "waveInStart") {
		t.Fatalf("Start error = %v, want waveInStart failure", err)
	}
	if strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("transient unprepare failure should recover, got %v", err)
	}
	if fake.resetCalls != 2 || fake.retryWaits != 1 {
		t.Fatalf("cleanup reset/waits = %d/%d, want 2/1", fake.resetCalls, fake.retryWaits)
	}
	if fake.unprepareCalls != windowsRecorderBuffers*2 {
		t.Fatalf("unprepare calls = %d, want %d", fake.unprepareCalls, windowsRecorderBuffers*2)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", fake.closeCalls)
	}
	if len(fake.closedEvents) != 2 || fake.closedEvents[0] != testCaptureEvent || fake.closedEvents[1] != testStopEvent {
		t.Fatalf("closed events = %#v, want capture then stop", fake.closedEvents)
	}
	if recorder.handle != 0 || recorder.captureEvent != 0 || recorder.stopEvent != 0 || recorder.pinned {
		t.Fatalf("recovered cleanup retained native ownership: handle=%#x capture=%#x stop=%#x pinned=%v", recorder.handle, recorder.captureEvent, recorder.stopEvent, recorder.pinned)
	}
	if recorder.headers != nil || recorder.samples != nil || recorder.processed != nil || recorder.prepared != 0 {
		t.Fatal("recovered cleanup retained Go backing")
	}
	if recorder.quarantined {
		t.Fatal("recovered cleanup quarantined recorder")
	}
}

func TestRecorderStopAndCloseAreBoundedWhenPumpCannotExit(t *testing.T) {
	waitEntered := make(chan struct{})
	releaseWait := make(chan struct{})
	fake := &fakeWaveInNative{
		setEventErr: errors.New("injected SetEvent failure"),
		waitResult: func(*[2]uintptr) (uint32, error) {
			close(waitEntered)
			<-releaseWait
			return waitFailed, errors.New("injected wait release")
		},
	}
	recorder := NewRecorder()
	recorder.ops = fake.ops()
	recorder.pumpStopTimeout = 20 * time.Millisecond
	if err := recorder.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pumpDone := recorder.pumpDone
	t.Cleanup(func() {
		close(releaseWait)
		select {
		case <-pumpDone:
		case <-time.After(time.Second):
			t.Error("blocked fake pump did not exit after test release")
		}
	})
	select {
	case <-waitEntered:
	case <-time.After(time.Second):
		t.Fatal("capture pump did not enter injected blocking wait")
	}

	started := time.Now()
	_, stopErr := recorder.Stop()
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop took %v after SetEvent failure, want bounded return", elapsed)
	}
	if stopErr == nil || !strings.Contains(stopErr.Error(), "SetEvent failure") || !strings.Contains(stopErr.Error(), "capture pump did not stop") || !strings.Contains(stopErr.Error(), "resources quarantined") {
		t.Fatalf("Stop error = %v, want signal, timeout, and quarantine diagnosis", stopErr)
	}

	started = time.Now()
	closeErr := recorder.Close()
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %v for quarantined pump, want bounded return", elapsed)
	}
	if closeErr == nil || !strings.Contains(closeErr.Error(), "resources quarantined") {
		t.Fatalf("Close error = %v, want quarantine diagnosis", closeErr)
	}
	if fake.unprepareCalls != 0 || fake.closeCalls != 0 || len(fake.closedEvents) != 0 {
		t.Fatalf("native ownership released without pump exit: unprepare=%d close=%d events=%v", fake.unprepareCalls, fake.closeCalls, fake.closedEvents)
	}
	assertRecorderBackingRetained(t, recorder, windowsRecorderBuffers)
	if recorder.prepared != windowsRecorderBuffers {
		t.Fatalf("prepared = %d, want %d while pump ownership is uncertain", recorder.prepared, windowsRecorderBuffers)
	}
}

func TestRecorderSetEventFailureCleansUpAfterPumpAlreadyExited(t *testing.T) {
	fake := &fakeWaveInNative{
		setEventErr: errors.New("injected SetEvent failure"),
		waitResult: func(*[2]uintptr) (uint32, error) {
			return waitFailed, errors.New("injected pump exit")
		},
	}
	recorder := NewRecorder()
	recorder.ops = fake.ops()
	if err := recorder.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-recorder.pumpDone:
	case <-time.After(time.Second):
		t.Fatal("capture pump did not self-exit")
	}

	err := recorder.Close()
	if err == nil || !strings.Contains(err.Error(), "SetEvent failure") || !strings.Contains(err.Error(), "injected pump exit") {
		t.Fatalf("Close error = %v, want retained signal and pump diagnostics", err)
	}
	if recorder.quarantined {
		t.Fatal("recorder was quarantined even though pump exit was confirmed")
	}
	if fake.unprepareCalls != windowsRecorderBuffers || fake.closeCalls != 1 || len(fake.closedEvents) != 2 {
		t.Fatalf("safe cleanup calls unprepare/close/events = %d/%d/%v", fake.unprepareCalls, fake.closeCalls, fake.closedEvents)
	}
	if recorder.handle != 0 || recorder.captureEvent != 0 || recorder.stopEvent != 0 || recorder.pinned {
		t.Fatal("confirmed pump exit did not release native resources")
	}
	if recorder.headers != nil || recorder.samples != nil || recorder.processed != nil {
		t.Fatal("confirmed pump exit did not clear native backing")
	}
}

func TestRecorderCloseUsesSameOwnershipCleanup(t *testing.T) {
	fake := &fakeWaveInNative{}
	recorder := NewRecorder()
	recorder.ops = fake.ops()
	recorder.handle = testWaveInHandle
	recorder.captureEvent = testCaptureEvent
	recorder.stopEvent = testStopEvent
	recorder.headers = make([]waveHeader, 1)
	recorder.samples = [][]byte{{1}}
	recorder.processed = make([]bool, 1)
	recorder.prepared = 1
	recorder.pinner.Pin(&recorder.headers[0])
	recorder.pinner.Pin(&recorder.samples[0][0])
	recorder.pinned = true

	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fake.resetCalls != 1 || fake.unprepareCalls != 1 || fake.closeCalls != 1 {
		t.Fatalf("cleanup calls reset/unprepare/close = %d/%d/%d, want 1/1/1", fake.resetCalls, fake.unprepareCalls, fake.closeCalls)
	}
	if len(fake.closedEvents) != 2 {
		t.Fatalf("closed events = %v, want both events", fake.closedEvents)
	}
	if recorder.handle != 0 || recorder.captureEvent != 0 || recorder.stopEvent != 0 || recorder.pinned {
		t.Fatal("Close retained native resources after successful cleanup")
	}
	if recorder.headers != nil || recorder.samples != nil || recorder.processed != nil {
		t.Fatal("Close retained backing after successful cleanup")
	}
}
