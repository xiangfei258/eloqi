package app

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiangchang24/eloqi/internal/config"
	"github.com/xiangchang24/eloqi/internal/platform"
	"github.com/xiangchang24/eloqi/internal/platform/mock"
	"github.com/xiangchang24/eloqi/internal/voice"
)

type runtimeStats struct {
	mu       sync.Mutex
	records  int
	text     string
	duration time.Duration
	err      error
}

func (s *runtimeStats) Record(text string, duration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records++
	s.text = text
	s.duration = duration
	return s.err
}

type runtimeOverlay struct {
	mu         sync.Mutex
	states     []voice.State
	errors     []error
	closeCount int
}

func (o *runtimeOverlay) StateChanged(state voice.State) {
	o.mu.Lock()
	o.states = append(o.states, state)
	o.mu.Unlock()
}

func (o *runtimeOverlay) ShowError(err error) {
	o.mu.Lock()
	o.errors = append(o.errors, err)
	o.mu.Unlock()
}

func (o *runtimeOverlay) Close() error {
	o.mu.Lock()
	o.closeCount++
	o.mu.Unlock()
	return nil
}

func validRuntimeConfig() config.Config {
	cfg := config.Defaults()
	cfg.ASR.Endpoint = "https://asr.example.test/v1/audio/transcriptions"
	cfg.ASR.APIKey = "secret"
	cfg.ASR.Model = "model"
	return cfg
}

func runtimeDeps(newHotkey func() (platform.Hotkey, error)) runtimeDependencies {
	return runtimeDependencies{
		newHotkey:   newHotkey,
		newRecorder: func() platform.Recorder { return &mock.Recorder{} },
		newASR:      func(config.Config) platform.ASRClient { return &mock.ASRClient{} },
		clipboard:   &mock.Clipboard{},
	}
}

func TestVoiceRuntimeStartReloadAndClose(t *testing.T) {
	var hotkeys []*mock.Hotkey
	newHotkey := func() (platform.Hotkey, error) {
		hotkey := mock.NewHotkey()
		hotkeys = append(hotkeys, hotkey)
		return hotkey, nil
	}
	overlay := &runtimeOverlay{}
	deps := runtimeDeps(newHotkey)
	deps.overlay = overlay
	r, err := newVoiceRuntime(deps)
	if err != nil {
		t.Fatal(err)
	}
	cfg := validRuntimeConfig()
	first, _ := config.ParseHotkey(cfg.Hotkey)
	if err := r.Start(cfg); err != nil {
		t.Fatal(err)
	}
	if len(hotkeys) != 1 || !hotkeys[0].Registered(first) {
		t.Fatalf("initial hotkey %s not registered", first)
	}

	nextCfg := cfg
	nextCfg.Hotkey.Key = "F2"
	next, _ := config.ParseHotkey(nextCfg.Hotkey)
	if err := r.Reload(nextCfg); err != nil {
		t.Fatal(err)
	}
	if len(hotkeys) != 2 {
		t.Fatalf("hotkey generations after reload = %d, want 2", len(hotkeys))
	}
	if !hotkeys[0].Closed() || !hotkeys[1].Registered(next) {
		t.Fatalf("hotkeys after reload: first closed=%v next registered=%v", hotkeys[0].Closed(), hotkeys[1].Registered(next))
	}
	if err := r.Reload(nextCfg); err != nil {
		t.Fatalf("identical reload: %v", err)
	}
	if len(hotkeys) != 2 {
		t.Fatalf("identical reload created generation %d", len(hotkeys))
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !hotkeys[1].Closed() {
		t.Fatal("hotkey generation remained open after Close")
	}
	if overlay.closeCount != 1 {
		t.Fatalf("overlay close count = %d, want 1", overlay.closeCount)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestVoiceRuntimeRejectsInvalidReloadWithoutStopping(t *testing.T) {
	var hotkeys []*mock.Hotkey
	r, err := newVoiceRuntime(runtimeDeps(func() (platform.Hotkey, error) {
		hotkey := mock.NewHotkey()
		hotkeys = append(hotkeys, hotkey)
		return hotkey, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	cfg := validRuntimeConfig()
	key, _ := config.ParseHotkey(cfg.Hotkey)
	if err := r.Start(cfg); err != nil {
		t.Fatal(err)
	}
	invalid := cfg
	invalid.Hotkey.Key = "ordinary-A"
	if err := r.Reload(invalid); err == nil {
		t.Fatal("invalid reload succeeded")
	}
	if len(hotkeys) != 1 || !hotkeys[0].Registered(key) || hotkeys[0].Closed() {
		t.Fatal("last valid hotkey was stopped by rejected reload")
	}
}

type selectiveHotkey struct {
	*mock.Hotkey
	fail platform.Key
}

func (h *selectiveHotkey) Register(key platform.Key) error {
	if key == h.fail {
		return errors.New("binding unavailable")
	}
	return h.Hotkey.Register(key)
}

// staleEdgeHotkey deliberately keeps its Events channel open after Close and
// queues a final press during both Unregister and Close. Native dispatchers can
// exhibit the same shape while their OS loop is winding down.
type staleEdgeHotkey struct {
	mu sync.Mutex

	events     chan platform.KeyEvent
	registered map[platform.Key]bool
	staleKey   platform.Key
	fail       platform.Key
	closed     bool

	eventsCalls atomic.Int32
	subscribed  chan struct{}
	subscribe   sync.Once
}

func newStaleEdgeHotkey(staleKey platform.Key) *staleEdgeHotkey {
	return &staleEdgeHotkey{
		events:     make(chan platform.KeyEvent, 8),
		registered: make(map[platform.Key]bool),
		staleKey:   staleKey,
		subscribed: make(chan struct{}),
	}
}

func (h *staleEdgeHotkey) Register(key platform.Key) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if key == h.fail {
		return errors.New("binding unavailable")
	}
	if h.closed {
		return errors.New("stale hotkey: closed")
	}
	h.registered[key] = true
	return nil
}

func (h *staleEdgeHotkey) Unregister(key platform.Key) error {
	h.mu.Lock()
	delete(h.registered, key)
	h.mu.Unlock()
	h.emitStale()
	return nil
}

func (h *staleEdgeHotkey) Events() <-chan platform.KeyEvent {
	h.eventsCalls.Add(1)
	h.subscribe.Do(func() { close(h.subscribed) })
	return h.events
}

func (h *staleEdgeHotkey) Close() error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.emitStale()
	return nil
}

func (h *staleEdgeHotkey) emitStale() {
	select {
	case h.events <- platform.KeyEvent{Key: h.staleKey, Pressed: true}:
	default:
	}
}

func (h *staleEdgeHotkey) Closed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

type recorderStartProbe struct {
	mock.Recorder
	starts  *atomic.Int32
	started chan<- struct{}
}

func (r *recorderStartProbe) Start() error {
	r.starts.Add(1)
	select {
	case r.started <- struct{}{}:
	default:
	}
	return r.Recorder.Start()
}

func waitForHotkeySubscription(t *testing.T, hotkey *staleEdgeHotkey) {
	t.Helper()
	select {
	case <-hotkey.subscribed:
	case <-time.After(time.Second):
		t.Fatal("voice event loop did not subscribe to hotkey provider")
	}
}

func assertNoRecorderStart(t *testing.T, started <-chan struct{}, starts *atomic.Int32) {
	t.Helper()
	select {
	case <-started:
		t.Fatalf("stale hotkey edge started %d recorder(s)", starts.Load())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestVoiceRuntimeReloadDoesNotConsumeRetiredHotkeyEdges(t *testing.T) {
	cfg := validRuntimeConfig()
	key, err := config.ParseHotkey(cfg.Hotkey)
	if err != nil {
		t.Fatal(err)
	}
	var providers []*staleEdgeHotkey
	deps := runtimeDeps(func() (platform.Hotkey, error) {
		provider := newStaleEdgeHotkey(key)
		providers = append(providers, provider)
		return provider, nil
	})
	var starts atomic.Int32
	started := make(chan struct{}, 1)
	deps.newRecorder = func() platform.Recorder {
		return &recorderStartProbe{starts: &starts, started: started}
	}
	runtime, err := newVoiceRuntime(deps)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	if err := runtime.Start(cfg); err != nil {
		t.Fatal(err)
	}
	waitForHotkeySubscription(t, providers[0])

	next := cfg
	next.ASR.Model = "new-model-only"
	if err := runtime.Reload(next); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("hotkey generations = %d, want 2", len(providers))
	}
	waitForHotkeySubscription(t, providers[1])
	providers[0].emitStale()
	if !providers[0].Closed() {
		t.Fatal("retired hotkey provider was not closed")
	}
	if got := providers[0].eventsCalls.Load(); got != 1 {
		t.Fatalf("retired Events subscriptions = %d, want 1", got)
	}
	if got := providers[1].eventsCalls.Load(); got != 1 {
		t.Fatalf("current Events subscriptions = %d, want 1", got)
	}
	assertNoRecorderStart(t, started, &starts)
}

func TestVoiceRuntimeRollbackUsesThirdHotkeyGeneration(t *testing.T) {
	cfg := validRuntimeConfig()
	initialKey, err := config.ParseHotkey(cfg.Hotkey)
	if err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Hotkey.Key = "F4"
	failingKey, err := config.ParseHotkey(next.Hotkey)
	if err != nil {
		t.Fatal(err)
	}
	var providers []*staleEdgeHotkey
	deps := runtimeDeps(func() (platform.Hotkey, error) {
		provider := newStaleEdgeHotkey(initialKey)
		providers = append(providers, provider)
		if len(providers) == 2 {
			provider.fail = failingKey
		}
		return provider, nil
	})
	var starts atomic.Int32
	started := make(chan struct{}, 1)
	deps.newRecorder = func() platform.Recorder {
		return &recorderStartProbe{starts: &starts, started: started}
	}
	runtime, err := newVoiceRuntime(deps)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	if err := runtime.Start(cfg); err != nil {
		t.Fatal(err)
	}
	waitForHotkeySubscription(t, providers[0])

	err = runtime.Reload(next)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("Reload error = %v", err)
	}
	if len(providers) != 3 {
		t.Fatalf("hotkey generations = %d, want 3", len(providers))
	}
	waitForHotkeySubscription(t, providers[2])
	providers[0].emitStale()
	providers[1].emitStale()
	if !providers[0].Closed() || !providers[1].Closed() {
		t.Fatalf("retired providers closed = %v, %v", providers[0].Closed(), providers[1].Closed())
	}
	if got := providers[0].eventsCalls.Load(); got != 1 {
		t.Fatalf("initial Events subscriptions = %d, want 1", got)
	}
	if got := providers[1].eventsCalls.Load(); got != 0 {
		t.Fatalf("failed candidate Events subscriptions = %d, want 0", got)
	}
	if got := providers[2].eventsCalls.Load(); got != 1 {
		t.Fatalf("rollback Events subscriptions = %d, want 1", got)
	}
	assertNoRecorderStart(t, started, &starts)
}

func TestVoiceRuntimeRestoresPreviousConfigWhenRegistrationFails(t *testing.T) {
	cfg := validRuntimeConfig()
	first, _ := config.ParseHotkey(cfg.Hotkey)
	nextCfg := cfg
	nextCfg.Hotkey.Key = "F3"
	failingKey, _ := config.ParseHotkey(nextCfg.Hotkey)
	var providers []*mock.Hotkey
	newHotkey := func() (platform.Hotkey, error) {
		provider := mock.NewHotkey()
		providers = append(providers, provider)
		if len(providers) == 2 {
			return &selectiveHotkey{Hotkey: provider, fail: failingKey}, nil
		}
		return provider, nil
	}
	r, err := newVoiceRuntime(runtimeDeps(newHotkey))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.Start(cfg); err != nil {
		t.Fatal(err)
	}
	err = r.Reload(nextCfg)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("Reload error = %v", err)
	}
	if len(providers) != 3 {
		t.Fatalf("hotkey generations = %d, want initial, failed candidate, and fresh rollback", len(providers))
	}
	if !providers[0].Closed() || !providers[1].Closed() {
		t.Fatalf("retired providers closed = %v, %v", providers[0].Closed(), providers[1].Closed())
	}
	if !providers[2].Registered(first) {
		t.Fatal("previous hotkey was not restored")
	}
}

func TestVoiceRuntimeSessionHooks(t *testing.T) {
	statistics := &runtimeStats{}
	overlay := &runtimeOverlay{}
	deps := runtimeDeps(func() (platform.Hotkey, error) { return mock.NewHotkey(), nil })
	deps.statistics = statistics
	deps.overlay = overlay
	r, err := newVoiceRuntime(deps)
	if err != nil {
		t.Fatal(err)
	}

	r.handleSession(voice.SessionResult{SessionID: 1, Text: "你好 Go", Duration: 1250 * time.Millisecond})
	statistics.mu.Lock()
	if statistics.records != 1 || statistics.text != "你好 Go" || statistics.duration != 1250*time.Millisecond {
		t.Fatalf("statistics = %+v", statistics)
	}
	statistics.mu.Unlock()

	want := errors.New("ASR unavailable")
	r.handleStateChange(voice.StateError)
	r.handleSession(voice.SessionResult{SessionID: 2, Err: want, Duration: time.Second})
	overlay.mu.Lock()
	if len(overlay.errors) != 1 || !errors.Is(overlay.errors[0], want) {
		t.Fatalf("overlay errors = %v", overlay.errors)
	}
	for _, state := range overlay.states {
		if state == voice.StateError {
			t.Fatalf("empty state error raced with detailed overlay error: %v", overlay.states)
		}
	}
	overlay.mu.Unlock()
	statistics.mu.Lock()
	if statistics.records != 1 {
		t.Fatalf("failed session changed records to %d", statistics.records)
	}
	statistics.mu.Unlock()

	r.handleSession(voice.SessionResult{SessionID: 3, Cancelled: true, Duration: time.Second})
	statistics.mu.Lock()
	defer statistics.mu.Unlock()
	if statistics.records != 1 {
		t.Fatalf("cancelled session changed records to %d", statistics.records)
	}
}

func TestNewVoiceRuntimeValidatesDependencies(t *testing.T) {
	if _, err := newVoiceRuntime(runtimeDependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}
