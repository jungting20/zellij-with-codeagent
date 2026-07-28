package codingagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/zellij"
)

type monitorRuntimeBackend struct{}

func (monitorRuntimeBackend) Session() string { return "test-session" }
func (monitorRuntimeBackend) CreateTab(context.Context, zellij.CreateTabRequest) (zellij.TabID, error) {
	return 1, nil
}
func (monitorRuntimeBackend) CloseTab(context.Context, zellij.CloseTabRequest) error { return nil }
func (monitorRuntimeBackend) CreatePane(context.Context, zellij.CreatePaneRequest) (zellij.PaneID, error) {
	return "terminal_1", nil
}
func (monitorRuntimeBackend) ClosePane(context.Context, zellij.ClosePaneRequest) error { return nil }
func (monitorRuntimeBackend) SendInput(context.Context, zellij.SendInputRequest) error { return nil }
func (monitorRuntimeBackend) ListPanes(context.Context, zellij.ListPanesRequest) ([]zellij.Pane, error) {
	return nil, nil
}
func (monitorRuntimeBackend) DumpScreen(context.Context, zellij.DumpScreenRequest) (string, error) {
	return "", nil
}
func (monitorRuntimeBackend) SubscribeCommand(zellij.SubscribeRequest) (zellij.CommandSpec, error) {
	return zellij.CommandSpec{}, nil
}

type fakeMonitorScheduler struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	timers []*fakeMonitorTimer
}

type fakeMonitorTimer struct {
	scheduler *fakeMonitorScheduler
	id        int
	due       time.Time
	fn        func()
	stopped   bool
	fired     bool
}

func newFakeMonitorScheduler() *fakeMonitorScheduler {
	return &fakeMonitorScheduler{now: time.Unix(1_000, 0)}
}

func (s *fakeMonitorScheduler) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *fakeMonitorScheduler) AfterFunc(delay time.Duration, fn func()) Timer {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	timer := &fakeMonitorTimer{scheduler: s, id: s.nextID, due: s.now.Add(delay), fn: fn}
	s.timers = append(s.timers, timer)
	return timer
}

func (t *fakeMonitorTimer) Stop() bool {
	t.scheduler.mu.Lock()
	defer t.scheduler.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func (s *fakeMonitorScheduler) Advance(delta time.Duration) {
	s.mu.Lock()
	target := s.now.Add(delta)
	s.mu.Unlock()
	for {
		s.mu.Lock()
		var next *fakeMonitorTimer
		for _, timer := range s.timers {
			if timer.stopped || timer.fired || timer.due.After(target) {
				continue
			}
			if next == nil || timer.due.Before(next.due) || (timer.due.Equal(next.due) && timer.id < next.id) {
				next = timer
			}
		}
		if next == nil {
			s.now = target
			s.mu.Unlock()
			return
		}
		s.now = next.due
		next.fired = true
		fn := next.fn
		s.mu.Unlock()
		fn()
	}
}

type monitorFixture struct {
	monitor   *Monitor
	store     Store
	bus       *eventbus.Bus
	scheduler *fakeMonitorScheduler
	record    Record
	pane      registry.PaneRecord
}

func newMonitorFixture(t *testing.T) monitorFixture {
	t.Helper()
	manifest, err := LoadManifest([]byte(`
version: 1
agent: codex
rules:
  - id: visible-idle
    priority: 500
    state: idle
    region: {type: whole_recent}
    match: {contains: [READY]}
    visible_idle: true
  - id: blocked
    priority: 400
    state: blocked
    region: {type: whole_recent}
    match: {contains: [BLOCK]}
    visible_blocker: true
  - id: working
    priority: 300
    state: working
    region: {type: whole_recent}
    match: {contains: [WORK]}
    visible_working: true
  - id: viewer
    priority: 200
    region: {type: whole_recent}
    match: {contains: [VIEW]}
    skip_state_update: true
`))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	detector, err := NewDetector(map[Kind]Manifest{KindCodex: manifest})
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}
	scheduler := newFakeMonitorScheduler()
	store := NewMemoryStore(scheduler.Now)
	record := Record{
		ID:             "agent-1",
		Kind:           KindCodex,
		PaneID:         "pane-1",
		State:          StateUnknown,
		CreatedAt:      scheduler.Now(),
		StateChangedAt: scheduler.Now(),
	}
	if _, err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	bus := eventbus.New()
	monitor := NewMonitor(MonitorOptions{
		Store:     store,
		Detector:  detector,
		EventBus:  bus,
		Now:       scheduler.Now,
		AfterFunc: scheduler.AfterFunc,
	})
	if err := monitor.Start(record); err != nil {
		t.Fatalf("Monitor.Start() error = %v", err)
	}
	fixture := monitorFixture{
		monitor:   monitor,
		store:     store,
		bus:       bus,
		scheduler: scheduler,
		record:    record,
		pane: registry.PaneRecord{
			ID:         registry.PaneID(record.PaneID),
			Generation: 1,
			Role:       "coding-agent",
		},
	}
	fixture.monitor.PaneOpened(fixture.pane)
	return fixture
}

func (f monitorFixture) state(t *testing.T) Record {
	t.Helper()
	record, err := f.store.Get(f.record.ID)
	if err != nil {
		t.Fatalf("Store.Get() error = %v", err)
	}
	return record
}

func (f monitorFixture) becomeWorking(t *testing.T) Record {
	t.Helper()
	f.monitor.PaneOutput(f.pane, "WORK")
	f.scheduler.Advance(3 * time.Second)
	record := f.state(t)
	if record.State != StateWorking {
		t.Fatalf("state after grace = %q, want working", record.State)
	}
	return record
}

func TestMonitorKeepsNewRecordUnknownDuringGraceAndEvaluatesCachedScreenAtExpiry(t *testing.T) {
	f := newMonitorFixture(t)
	f.monitor.PaneOutput(f.pane, "WORK")

	f.scheduler.Advance(3*time.Second - time.Nanosecond)
	if got := f.state(t).State; got != StateUnknown {
		t.Fatalf("state during grace = %q, want unknown", got)
	}

	f.scheduler.Advance(time.Nanosecond)
	if got := f.state(t).State; got != StateWorking {
		t.Fatalf("state at grace expiry = %q, want working from cached screen", got)
	}
}

func TestMonitorIgnoresObservationsUntilPaneGenerationIsOpened(t *testing.T) {
	f := newMonitorFixture(t)
	f.monitor.Stop(f.record.ID)
	if err := f.monitor.Start(f.record); err != nil {
		t.Fatalf("Monitor.Start() error = %v", err)
	}
	f.monitor.PaneOutput(f.pane, "WORK")
	f.monitor.PaneError(f.pane, errors.New("unbound error"))
	f.scheduler.Advance(startupGrace)

	got := f.state(t)
	if got.State != StateUnknown || got.StateReason != "" {
		t.Fatalf("unbound observations changed record: %#v", got)
	}
}

func TestMonitorRejectsLateOldGenerationOutputAndErrorAfterPaneReuse(t *testing.T) {
	f := newMonitorFixture(t)
	oldPane := f.pane
	f.monitor.PaneOpened(oldPane)
	f.monitor.PaneClosed(oldPane)

	replacement := f.record
	replacement.State = StateUnknown
	replacement.StateReason = ""
	if _, err := f.store.Create(replacement); err != nil {
		t.Fatalf("Store.Create(replacement) error = %v", err)
	}
	if err := f.monitor.Start(replacement); err != nil {
		t.Fatalf("Monitor.Start(replacement) error = %v", err)
	}
	newPane := oldPane
	newPane.Generation++
	f.monitor.PaneOpened(newPane)

	f.monitor.PaneOutput(oldPane, "WORK")
	f.monitor.PaneError(oldPane, errors.New("late old generation"))
	f.scheduler.Advance(startupGrace)

	got := f.state(t)
	if got.State != StateUnknown || got.StateReason != "" {
		t.Fatalf("late old generation changed replacement: %#v", got)
	}
}

func TestMonitorConfirmsWorkingToNonVisibleIdleThreeTimes(t *testing.T) {
	f := newMonitorFixture(t)
	f.becomeWorking(t)
	f.monitor.PaneOutput(f.pane, "quiet prompt")

	for confirmation := 1; confirmation <= 2; confirmation++ {
		f.scheduler.Advance(100 * time.Millisecond)
		if got := f.state(t).State; got != StateWorking {
			t.Fatalf("state after confirmation %d = %q, want working", confirmation, got)
		}
	}
	f.scheduler.Advance(100 * time.Millisecond)
	if got := f.state(t).State; got != StateIdle {
		t.Fatalf("state after third confirmation = %q, want idle", got)
	}
	if elapsed := f.scheduler.Now().Sub(time.Unix(1_000, 0).Add(3 * time.Second)); elapsed > 700*time.Millisecond {
		t.Fatalf("idle resolution took %v, want no later than 700ms", elapsed)
	}
}

func TestMonitorCancelsIdleCandidateOnWorkingOrBlockedScreen(t *testing.T) {
	for _, tt := range []struct {
		name   string
		screen string
		want   State
	}{
		{name: "working", screen: "WORK", want: StateWorking},
		{name: "blocked", screen: "BLOCK", want: StateBlocked},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newMonitorFixture(t)
			f.becomeWorking(t)
			f.monitor.PaneOutput(f.pane, "quiet prompt")
			f.scheduler.Advance(100 * time.Millisecond)

			f.monitor.PaneOutput(f.pane, tt.screen)
			f.scheduler.Advance(time.Second)

			if got := f.state(t).State; got != tt.want {
				t.Fatalf("state = %q, want %q after candidate cancellation", got, tt.want)
			}
		})
	}
}

func TestMonitorStaleIdleCallbacksWaitingOnMutexCannotConsumeNewCandidate(t *testing.T) {
	f := newMonitorFixture(t)
	f.becomeWorking(t)
	f.monitor.PaneOutput(f.pane, "quiet prompt")

	f.monitor.mu.Lock()
	entry := f.monitor.monitoring[f.record.ID]
	staleConfirmation := entry.idleTimer.(*fakeMonitorTimer).fn
	staleDeadline := entry.idleDeadline.(*fakeMonitorTimer).fn
	confirmationStarted := make(chan struct{})
	confirmationDone := make(chan struct{})
	go func() {
		close(confirmationStarted)
		staleConfirmation()
		close(confirmationDone)
	}()
	<-confirmationStarted
	f.monitor.cancelIdleCandidateLocked(entry)
	f.monitor.startIdleCandidateLocked(entry)
	newConfirmation := entry.idleTimer
	newDeadline := entry.idleDeadline
	f.monitor.mu.Unlock()
	<-confirmationDone

	deadlineStarted := make(chan struct{})
	deadlineDone := make(chan struct{})
	f.monitor.mu.Lock()
	go func() {
		close(deadlineStarted)
		staleDeadline()
		close(deadlineDone)
	}()
	<-deadlineStarted
	f.monitor.mu.Unlock()
	<-deadlineDone

	f.monitor.mu.Lock()
	entry = f.monitor.monitoring[f.record.ID]
	if entry.idleConfirmations != 0 || entry.idleTimer != newConfirmation || entry.idleDeadline != newDeadline {
		f.monitor.mu.Unlock()
		t.Fatalf("new idle candidate was consumed by stale callbacks: %#v", entry)
	}
	f.monitor.mu.Unlock()
	if got := f.state(t).State; got != StateWorking {
		t.Fatalf("state after stale callbacks = %q, want working", got)
	}
}

func TestMonitorSkipStateUpdatePreservesCurrentState(t *testing.T) {
	f := newMonitorFixture(t)
	working := f.becomeWorking(t)
	eventCount := len(f.bus.Recent(0))
	f.scheduler.Advance(time.Second)

	f.monitor.PaneOutput(f.pane, "VIEW")

	got := f.state(t)
	if got.State != StateWorking || !got.StateChangedAt.Equal(working.StateChangedAt) {
		t.Fatalf("record after skip = %#v, want unchanged working state", got)
	}
	if events := len(f.bus.Recent(0)); events != eventCount {
		t.Fatalf("event count after skip = %d, want %d", events, eventCount)
	}
}

func TestMonitorVisibleIdleChangesImmediately(t *testing.T) {
	f := newMonitorFixture(t)
	f.becomeWorking(t)

	f.monitor.PaneOutput(f.pane, "READY")

	if got := f.state(t).State; got != StateIdle {
		t.Fatalf("state = %q, want immediate idle", got)
	}
}

func TestMonitorIdenticalDetectionDoesNotRestampOrPublish(t *testing.T) {
	f := newMonitorFixture(t)
	working := f.becomeWorking(t)
	eventCount := len(f.bus.Recent(0))
	f.scheduler.Advance(time.Second)

	f.monitor.PaneOutput(f.pane, "WORK again")

	got := f.state(t)
	if !got.StateChangedAt.Equal(working.StateChangedAt) {
		t.Fatalf("StateChangedAt = %v, want unchanged %v", got.StateChangedAt, working.StateChangedAt)
	}
	if events := len(f.bus.Recent(0)); events != eventCount {
		t.Fatalf("event count = %d, want unchanged %d", events, eventCount)
	}
}

func TestMonitorPaneErrorSetsUnknownAndPublishesDiagnostic(t *testing.T) {
	f := newMonitorFixture(t)
	f.becomeWorking(t)
	wantErr := errors.New("subscribe stream failed")

	f.monitor.PaneError(f.pane, wantErr)

	got := f.state(t)
	if got.State != StateUnknown || !strings.Contains(got.StateReason, wantErr.Error()) {
		t.Fatalf("record after pane error = %#v, want diagnostic unknown", got)
	}
	events := f.bus.Recent(0)
	last := events[len(events)-1]
	if last.Type != eventbus.TypeAgentStateChanged || last.AgentID != string(f.record.ID) ||
		last.PaneID != string(f.record.PaneID) || last.AgentKind != string(f.record.Kind) ||
		last.PreviousState != string(StateWorking) || last.AgentState != string(StateUnknown) ||
		!strings.Contains(last.Reason, wantErr.Error()) {
		t.Fatalf("agent state event = %#v", last)
	}
}

func TestMonitorPaneErrorDuringGraceInvalidatesCachedScreenAndStaleCallback(t *testing.T) {
	f := newMonitorFixture(t)
	f.monitor.PaneOutput(f.pane, "WORK")

	f.monitor.mu.Lock()
	entry := f.monitor.monitoring[f.record.ID]
	staleGraceCallback := entry.graceTimer.(*fakeMonitorTimer).fn
	f.monitor.mu.Unlock()

	wantErr := errors.New("startup subscription failed")
	f.monitor.PaneError(f.pane, wantErr)
	staleGraceCallback()

	got := f.state(t)
	if got.State != StateUnknown || !strings.Contains(got.StateReason, wantErr.Error()) {
		t.Fatalf("record after stale grace callback = %#v, want diagnostic unknown", got)
	}
	f.monitor.mu.Lock()
	defer f.monitor.mu.Unlock()
	entry = f.monitor.monitoring[f.record.ID]
	if entry.graceTimer != nil || entry.hasInput || entry.latestInput.Screen != "" {
		t.Fatalf("entry after pane error = %#v, want grace and cached input invalidated", entry)
	}
}

func TestMonitorPaneCloseCancelsTimersAndDeletesRecord(t *testing.T) {
	f := newMonitorFixture(t)
	f.monitor.PaneOutput(f.pane, "WORK")

	f.monitor.PaneClosed(f.pane)
	f.scheduler.Advance(10 * time.Second)

	if _, err := f.store.Get(f.record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Store.Get(closed) error = %v, want ErrNotFound", err)
	}
}

func TestRuntimeServiceClosePaneDeletesMonitoredAgentRecord(t *testing.T) {
	f := newMonitorFixture(t)
	f.monitor.Stop(f.record.ID)
	if err := f.monitor.Start(f.record); err != nil {
		t.Fatalf("Monitor.Start() error = %v", err)
	}
	service := runtime.NewService(runtime.Options{
		Registry:     registry.New(),
		Backend:      monitorRuntimeBackend{},
		PaneObserver: f.monitor,
	})
	if _, err := service.CreatePane(context.Background(), runtime.CreatePaneRequest{
		ID: f.record.PaneID, ZellijSession: "test-session", Role: "coding-agent",
	}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if _, err := service.ClosePane(context.Background(), runtime.ClosePaneRequest{PaneID: f.record.PaneID}); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}

	if _, err := f.store.Get(f.record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Store.Get(closed) error = %v, want ErrNotFound", err)
	}
}

func TestMonitorIgnoresNonCodingAgentAndUnknownPanes(t *testing.T) {
	f := newMonitorFixture(t)
	nonCoding := f.pane
	nonCoding.Role = "shell"
	f.monitor.PaneOutput(nonCoding, "WORK")
	f.monitor.PaneError(nonCoding, errors.New("ignored"))
	f.monitor.PaneClosed(nonCoding)
	unknown := registry.PaneRecord{ID: "missing", Role: "coding-agent"}
	f.monitor.PaneOutput(unknown, "WORK")
	f.monitor.PaneError(unknown, errors.New("ignored"))
	f.monitor.PaneClosed(unknown)
	f.scheduler.Advance(3 * time.Second)

	if got := f.state(t); got.State != StateUnknown {
		t.Fatalf("state after ignored observations = %q, want unknown", got.State)
	}
}

func TestMonitorStartFailsOnlyForKindsWithManifestErrors(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	wantErr := errors.New("codex.yaml: invalid manifest")
	loadErrors[KindCodex] = wantErr
	store := NewMemoryStore(time.Now)
	monitor := NewMonitor(MonitorOptions{Store: store, Detector: detector, DetectorErrors: loadErrors})
	codex := Record{ID: "codex-1", Kind: KindCodex, PaneID: runtime.PaneID("pane-1"), State: StateUnknown}
	claude := Record{ID: "claude-1", Kind: KindClaude, PaneID: runtime.PaneID("pane-2"), State: StateUnknown}
	if _, err := store.Create(codex); err != nil {
		t.Fatalf("Create(codex) error = %v", err)
	}
	if _, err := store.Create(claude); err != nil {
		t.Fatalf("Create(claude) error = %v", err)
	}

	if err := monitor.Start(codex); !errors.Is(err, wantErr) {
		t.Fatalf("Start(codex) error = %v, want %v", err, wantErr)
	}
	if err := monitor.Start(claude); err != nil {
		t.Fatalf("Start(claude) error = %v, want nil", err)
	}
	defer monitor.Stop(claude.ID)
}
