package daemoncli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/eventbus"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/voice"
	"zellij-with-codeagent/internal/zellij"
)

func TestVoiceQueueAdapterConvertsNotificationAndStatuses(t *testing.T) {
	request := transport.VoiceNotificationRequest{
		RequestID: "request-42",
		Prefix:    "ticket-manager",
		TicketID:  42,
		Summary:   "tests passed",
	}
	cases := []struct {
		name   string
		status voice.EnqueueStatus
		want   string
	}{
		{name: "queued", status: voice.EnqueueStatusQueued, want: "queued"},
		{name: "duplicate", status: voice.EnqueueStatusDuplicate, want: "duplicate"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeDaemonVoiceService{enqueueStatus: tt.status}
			adapter := voiceQueueAdapter{service: service}

			response, err := adapter.QueueVoiceNotification(context.Background(), request)

			if err != nil {
				t.Fatalf("QueueVoiceNotification() error = %v", err)
			}
			if response != (transport.VoiceNotificationResponse{Status: tt.want}) {
				t.Fatalf("QueueVoiceNotification() response = %#v, want status %q", response, tt.want)
			}
			if got := service.notification(); got != (voice.Notification{
				RequestID: "request-42",
				Prefix:    "ticket-manager",
				TicketID:  42,
				Summary:   "tests passed",
			}) {
				t.Fatalf("Enqueue() notification = %#v, want converted request", got)
			}
		})
	}
}

func TestVoiceQueueAdapterMapsQueueFull(t *testing.T) {
	service := &fakeDaemonVoiceService{enqueueErr: voice.ErrQueueFull}
	adapter := voiceQueueAdapter{service: service}

	_, err := adapter.QueueVoiceNotification(context.Background(), transport.VoiceNotificationRequest{RequestID: "request-1"})

	if !errors.Is(err, transport.ErrVoiceQueueFull) {
		t.Fatalf("QueueVoiceNotification() error = %v, want transport.ErrVoiceQueueFull", err)
	}
}

func TestVoiceQueueAdapterRejectsCanceledContext(t *testing.T) {
	service := &fakeDaemonVoiceService{enqueueStatus: voice.EnqueueStatusQueued}
	adapter := voiceQueueAdapter{service: service}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.QueueVoiceNotification(ctx, transport.VoiceNotificationRequest{RequestID: "request-1"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("QueueVoiceNotification() error = %v, want context.Canceled", err)
	}
	if got := service.enqueueCalls(); got != 0 {
		t.Fatalf("Enqueue() calls = %d, want 0", got)
	}
}

func TestRunContextClosesVoiceServiceAfterSocketShutdown(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/agentd-voice-daemon-%d.sock", time.Now().UnixNano())
	defer os.Remove(socketPath)
	service := &fakeDaemonVoiceService{closeSocketPath: socketPath, closeErr: errors.New("speaker shutdown failed")}
	originalFactory := newDaemonVoiceService
	newDaemonVoiceService = func(io.Writer) daemonVoiceService { return service }
	t.Cleanup(func() { newDaemonVoiceService = originalFactory })

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	var code int
	done := make(chan struct{})
	go func() {
		code = RunContext(ctx, []string{"serve", "--socket", socketPath}, &stdout, &stderr)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("RunContext() did not stop during test cleanup")
		}
	})
	client := transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: 100 * time.Millisecond})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for daemon socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case <-done:
		if code != 0 {
			t.Fatalf("RunContext() exit code = %d, want 0; stderr=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("RunContext() did not stop after shutdown")
	}
	if got := service.closeCalls(); got != 1 {
		t.Fatalf("Close() calls = %d, want 1", got)
	}
	if err := service.socketErrorWhenClosed(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("voice service Close() socket stat error = %v, want os.ErrNotExist", err)
	}
	if !strings.Contains(stderr.String(), "close voice service: speaker shutdown failed") {
		t.Fatalf("stderr = %q, want voice Close error", stderr.String())
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after daemon shutdown: %v", err)
	}
}

type fakeDaemonVoiceService struct {
	mu               sync.Mutex
	enqueueStatus    voice.EnqueueStatus
	enqueueErr       error
	enqueued         voice.Notification
	enqueueCallCount int
	closeCallCount   int
	closeSocketPath  string
	closeSocketErr   error
	closeErr         error
}

func (f *fakeDaemonVoiceService) Enqueue(notification voice.Notification) (voice.EnqueueStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = notification
	f.enqueueCallCount++
	return f.enqueueStatus, f.enqueueErr
}

func (f *fakeDaemonVoiceService) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCallCount++
	if f.closeSocketPath != "" {
		_, err := os.Stat(f.closeSocketPath)
		f.closeSocketErr = err
	}
	return f.closeErr
}

func (f *fakeDaemonVoiceService) notification() voice.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enqueued
}

func (f *fakeDaemonVoiceService) enqueueCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enqueueCallCount
}

func (f *fakeDaemonVoiceService) closeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCallCount
}

func (f *fakeDaemonVoiceService) socketErrorWhenClosed() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeSocketErr
}

func TestNewRuntimeServiceAssemblesSharedRuntimeAgentStoreAndEventBus(t *testing.T) {
	backend := newDaemonFakeBackend()
	store := codingagent.NewMemoryStore(time.Now)
	bus := eventbus.New()
	detector, detectorErrors := codingagent.LoadEmbeddedDetector()
	detectorErrors[codingagent.KindClaude] = errors.New("claude.yaml: invalid test manifest")
	restoreDaemonFactories(t)
	newDaemonEventBus = func() *eventbus.Bus { return bus }
	newDaemonStore = func(func() time.Time) codingagent.Store { return store }
	loadDaemonDetector = func() (*codingagent.Detector, map[codingagent.Kind]error) { return detector, detectorErrors }
	newDaemonBackend = func() daemonBackend { return backend }
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return daemonFakeSubscriptionRunner{} }

	service, err := newRuntimeService()
	if err != nil {
		t.Fatalf("newRuntimeService() error = %v", err)
	}
	started, err := service.StartAgent(context.Background(), codingagent.StartAgentRequest{
		Kind: codingagent.KindCodex, CWD: t.TempDir(), SourceZellijSession: "physical-a", SourceZellijPaneID: "source-pane",
	})
	if err != nil {
		t.Fatalf("StartAgent(valid) error = %v", err)
	}
	runtimeStatus, err := service.InspectRuntime(context.Background(), agentruntime.InspectRuntimeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := service.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeStatus.Panes) != 1 || len(agents.Agents) != 1 || runtimeStatus.Panes[0].ID != started.Agent.Pane.ID || agents.Agents[0].Pane.ID != started.Agent.Pane.ID {
		t.Fatalf("runtime=%#v agents=%#v, want one shared pane", runtimeStatus.Panes, agents.Agents)
	}
	bus.Publish(eventbus.Event{Type: eventbus.TypeHealthChanged, Message: "shared-bus", Time: time.Unix(1, 0)})
	recent, err := service.RecentEvents(context.Background(), agentruntime.RecentEventsRequest{})
	if err != nil || len(recent.Events) == 0 || recent.Events[len(recent.Events)-1].Message != "shared-bus" {
		t.Fatalf("RecentEvents() = %#v, %v; want shared bus event", recent, err)
	}

	_, err = service.StartAgent(context.Background(), codingagent.StartAgentRequest{
		Kind: codingagent.KindClaude, CWD: t.TempDir(), SourceZellijSession: "physical-a", SourceZellijPaneID: "source-pane",
	})
	if err == nil || !strings.Contains(err.Error(), "claude.yaml") {
		t.Fatalf("StartAgent(invalid manifest) error = %v, want filename-qualified error", err)
	}
	if backend.createCount() != 0 {
		t.Fatalf("backend create calls = %d, want current pane claims without pane creation", backend.createCount())
	}
	if _, err := service.InspectRuntime(context.Background(), agentruntime.InspectRuntimeRequest{}); err != nil {
		t.Fatalf("daemon runtime unavailable after invalid manifest: %v", err)
	}
}

func TestNewRuntimeBundleExposesSharedStoreAndEventBus(t *testing.T) {
	backend := newDaemonFakeBackend()
	store := codingagent.NewMemoryStore(time.Now)
	bus := eventbus.New()
	restoreDaemonFactories(t)
	newDaemonEventBus = func() *eventbus.Bus { return bus }
	newDaemonStore = func(func() time.Time) codingagent.Store { return store }
	newDaemonBackend = func() daemonBackend { return backend }
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return daemonFakeSubscriptionRunner{} }

	bundle, err := newRuntimeBundle()
	if err != nil {
		t.Fatalf("newRuntimeBundle() error = %v", err)
	}
	if bundle.service == nil {
		t.Fatal("newRuntimeBundle() service is nil")
	}
	if bundle.bus != bus {
		t.Fatalf("newRuntimeBundle() bus = %p, want %p", bundle.bus, bus)
	}
	if bundle.store != store {
		t.Fatalf("newRuntimeBundle() store = %#v, want shared store %#v", bundle.store, store)
	}
}

func TestNewRuntimeServiceTreatsNilAgentServiceAsConstructionError(t *testing.T) {
	restoreDaemonFactories(t)
	newDaemonAgentService = func(codingagent.ServiceOptions) *codingagent.Service { return nil }
	service, err := newRuntimeService()
	if err == nil || service != nil {
		t.Fatalf("newRuntimeService() = %#v, %v; want construction error", service, err)
	}
}

func TestNewRuntimeServiceRejectsMissingSharedDependencies(t *testing.T) {
	tests := []struct {
		name   string
		inject func()
	}{
		{name: "event bus", inject: func() { newDaemonEventBus = func() *eventbus.Bus { return nil } }},
		{name: "store", inject: func() { newDaemonStore = func(func() time.Time) codingagent.Store { return nil } }},
		{name: "detector", inject: func() {
			loadDaemonDetector = func() (*codingagent.Detector, map[codingagent.Kind]error) { return nil, nil }
		}},
		{name: "monitor", inject: func() {
			newDaemonMonitor = func(codingagent.MonitorOptions) *codingagent.Monitor { return nil }
		}},
		{name: "backend", inject: func() { newDaemonBackend = func() daemonBackend { return nil } }},
		{name: "subscription runner", inject: func() {
			newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return nil }
		}},
		{name: "runtime service", inject: func() {
			newDaemonRuntimeService = func(agentruntime.Options) *agentruntime.Service { return nil }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreDaemonFactories(t)
			tt.inject()
			service, err := newRuntimeService()
			if err == nil || service != nil {
				t.Fatalf("newRuntimeService() = %#v, %v; want missing %s error", service, err, tt.name)
			}
		})
	}
}

func TestReconcileLoopTicksContinuesAfterErrorAndStopsTicker(t *testing.T) {
	ticker := newDaemonFakeTicker()
	service := &daemonFakeReconciler{errors: []error{errors.New("temporary failure"), nil}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runReconcileLoop(ctx, service, 2*time.Second, func(interval time.Duration) reconcileTicker {
			if interval != 2*time.Second {
				t.Errorf("interval = %s, want 2s", interval)
			}
			return ticker
		}, func(error) {})
	}()

	ticker.tick()
	service.waitCalls(t, 1)
	ticker.tick()
	service.waitCalls(t, 2)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconcile loop did not stop after cancellation")
	}
	select {
	case <-ticker.stopped:
	default:
		t.Fatal("reconcile ticker was not stopped")
	}
}

func TestReconcileLoopDoesNotOverlapCalls(t *testing.T) {
	ticker := newDaemonFakeTicker()
	service := &daemonBlockingReconciler{started: make(chan struct{}, 2), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runReconcileLoop(ctx, service, defaultReconcileInterval, func(time.Duration) reconcileTicker { return ticker }, nil)
	}()
	ticker.tick()
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("first reconcile did not start")
	}
	ticker.tick()
	select {
	case <-service.started:
		t.Fatal("second reconcile overlapped the first")
	case <-time.After(20 * time.Millisecond):
	}
	service.release <- struct{}{}
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("queued second reconcile did not start")
	}
	cancel()
	close(service.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconcile loop did not exit")
	}
}

func TestRunContextServeStartsAndJoinsReconcileLoop(t *testing.T) {
	restoreDaemonServeSeams(t)
	ticker := newDaemonFakeTicker()
	server := &daemonFakeServeServer{started: make(chan struct{}), canceled: make(chan struct{})}
	reconciler := &daemonCancelableReconciler{started: make(chan struct{}), finished: make(chan struct{})}
	newDaemonTransportServer = func(opts transport.ServerOptions) (daemonServeServer, error) {
		if opts.Service == nil || opts.SocketPath == "" {
			t.Fatalf("server options = %#v, want assembled service and socket", opts)
		}
		return server, nil
	}
	newDaemonReconcileTicker = func(interval time.Duration) reconcileTicker {
		if interval != defaultReconcileInterval {
			t.Errorf("ticker interval = %s, want %s", interval, defaultReconcileInterval)
		}
		return ticker
	}
	reconcileServiceForDaemon = func(transport.ServerRuntime) agentruntime.ReconciliationService { return reconciler }
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	socketPath := filepath.Join(t.TempDir(), "agentd.sock")
	go func() {
		result <- RunContext(ctx, []string{"serve", "--socket", socketPath}, io.Discard, io.Discard)
	}()
	select {
	case <-server.started:
	case <-time.After(time.Second):
		t.Fatal("server ListenAndServe was not called")
	}
	ticker.tick()
	select {
	case <-reconciler.started:
	case <-time.After(time.Second):
		t.Fatal("serve wiring did not run reconciliation")
	}
	cancel()
	select {
	case code := <-result:
		if code != 0 {
			t.Fatalf("RunContext() code = %d, want 0 for context cancellation", code)
		}
	case <-time.After(time.Second):
		t.Fatal("RunContext() did not return after cancellation")
	}
	for name, channel := range map[string]<-chan struct{}{
		"server cancellation":  server.canceled,
		"reconcile completion": reconciler.finished,
		"ticker stop":          ticker.stopped,
	} {
		select {
		case <-channel:
		default:
			t.Fatalf("RunContext returned before %s", name)
		}
	}
}

func TestRunContextServeStartupFailureStopsAndJoinsReconcileLoop(t *testing.T) {
	restoreDaemonServeSeams(t)
	ticker := newDaemonFakeTicker()
	tickerCreated := make(chan struct{})
	server := &daemonFakeServeServer{started: make(chan struct{}), waitFor: tickerCreated, serveErr: errors.New("listen failed")}
	newDaemonTransportServer = func(transport.ServerOptions) (daemonServeServer, error) { return server, nil }
	newDaemonReconcileTicker = func(time.Duration) reconcileTicker {
		close(tickerCreated)
		return ticker
	}

	code := RunContext(context.Background(), []string{"serve", "--socket", filepath.Join(t.TempDir(), "agentd.sock")}, io.Discard, io.Discard)
	if code != 1 {
		t.Fatalf("RunContext() code = %d, want 1", code)
	}
	select {
	case <-ticker.stopped:
	default:
		t.Fatal("RunContext returned before startup-failure ticker stop")
	}
}

func TestRunContextServeDeliversIdleVoiceFromSharedEventBus(t *testing.T) {
	restoreDaemonFactories(t)
	restoreDaemonServeSeams(t)
	store := codingagent.NewMemoryStore(time.Now)
	bus := eventbus.New()
	voiceService := &fakeDaemonVoiceService{enqueueStatus: voice.EnqueueStatusQueued}
	newDaemonStore = func(func() time.Time) codingagent.Store { return store }
	newDaemonEventBus = func() *eventbus.Bus { return bus }
	newDaemonBackend = func() daemonBackend { return newDaemonFakeBackend() }
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return daemonFakeSubscriptionRunner{} }
	originalVoiceFactory := newDaemonVoiceService
	newDaemonVoiceService = func(io.Writer) daemonVoiceService { return voiceService }
	t.Cleanup(func() { newDaemonVoiceService = originalVoiceFactory })

	changedAt := time.Unix(7, 11)
	_, err := store.Create(codingagent.Record{
		ID: "agent-9", Kind: codingagent.KindClaude, PaneID: "pane-9",
		State: codingagent.StateIdle, NotifyOnIdle: true, StateChangedAt: changedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	newDaemonTransportServer = func(transport.ServerOptions) (daemonServeServer, error) {
		return daemonServeServerFunc(func(context.Context) error {
			bus.Publish(eventbus.Event{
				Type: eventbus.TypeAgentStateChanged, AgentID: "agent-9",
				PreviousState: string(codingagent.StateWorking), AgentState: string(codingagent.StateIdle),
			})
			deadline := time.Now().Add(time.Second)
			for voiceService.enqueueCalls() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if voiceService.enqueueCalls() == 0 {
				t.Error("idle voice notification was not enqueued")
			}
			return context.Canceled
		}), nil
	}

	code := RunContext(context.Background(), []string{"serve", "--socket", filepath.Join(t.TempDir(), "agentd.sock")}, io.Discard, io.Discard)

	if code != 0 {
		t.Fatalf("RunContext() code = %d, want 0", code)
	}
	if got, want := voiceService.notification(), (voice.Notification{
		RequestID: "agent-idle:agent-9:7000000011",
		Message:   "Claude agent-9 작업이 완료되었습니다",
	}); got != want {
		t.Fatalf("notification = %#v, want %#v", got, want)
	}
	if got := voiceService.closeCalls(); got != 1 {
		t.Fatalf("voice Close() calls = %d, want 1 after idle loop shutdown", got)
	}
	bus.Publish(eventbus.Event{
		Type: eventbus.TypeAgentStateChanged, AgentID: "agent-9",
		PreviousState: string(codingagent.StateWorking), AgentState: string(codingagent.StateIdle),
	})
	time.Sleep(10 * time.Millisecond)
	if got := voiceService.enqueueCalls(); got != 1 {
		t.Fatalf("enqueue calls after RunContext returned = %d, want 1", got)
	}
}

func restoreDaemonServeSeams(t *testing.T) {
	t.Helper()
	serverFactory := newDaemonTransportServer
	tickerFactory := newDaemonReconcileTicker
	reconcilerFactory := reconcileServiceForDaemon
	t.Cleanup(func() {
		newDaemonTransportServer = serverFactory
		newDaemonReconcileTicker = tickerFactory
		reconcileServiceForDaemon = reconcilerFactory
	})
}

type daemonFakeServeServer struct {
	started  chan struct{}
	canceled chan struct{}
	waitFor  <-chan struct{}
	serveErr error
	once     sync.Once
}

type daemonServeServerFunc func(context.Context) error

func (f daemonServeServerFunc) ListenAndServe(ctx context.Context) error { return f(ctx) }

func (s *daemonFakeServeServer) ListenAndServe(ctx context.Context) error {
	s.once.Do(func() { close(s.started) })
	if s.waitFor != nil {
		select {
		case <-s.waitFor:
			return s.serveErr
		case <-ctx.Done():
			if s.canceled != nil {
				close(s.canceled)
			}
			return ctx.Err()
		}
	}
	<-ctx.Done()
	if s.canceled != nil {
		close(s.canceled)
	}
	return ctx.Err()
}

type daemonCancelableReconciler struct {
	started  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (r *daemonCancelableReconciler) Reconcile(ctx context.Context, _ agentruntime.ReconcileRequest) (agentruntime.ReconcileResponse, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.finished)
	return agentruntime.ReconcileResponse{}, ctx.Err()
}

func TestDaemonReconcileRemovesOnlyMissingManagedAgentThroughObserver(t *testing.T) {
	backend := newDaemonFakeBackend()
	backend.addPane(zellij.Pane{ID: "second-source-pane", TabID: 7, TabName: "main"})
	store := codingagent.NewMemoryStore(time.Now)
	restoreDaemonFactories(t)
	newDaemonStore = func(func() time.Time) codingagent.Store { return store }
	newDaemonBackend = func() daemonBackend { return backend }
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return daemonFakeSubscriptionRunner{} }
	service, err := newRuntimeService()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	first, err := service.StartAgent(context.Background(), codingagent.StartAgentRequest{Kind: codingagent.KindCodex, CWD: cwd, SourceZellijSession: "physical-a", SourceZellijPaneID: "source-pane"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.StartAgent(context.Background(), codingagent.StartAgentRequest{Kind: codingagent.KindGemini, CWD: cwd, SourceZellijSession: "physical-a", SourceZellijPaneID: "second-source-pane"})
	if err != nil {
		t.Fatal(err)
	}

	backend.removePane(zellij.PaneID(first.Agent.Pane.ZellijPaneID))
	if _, err := service.Reconcile(context.Background(), agentruntime.ReconcileRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(first.Agent.Agent.ID); !errors.Is(err, codingagent.ErrNotFound) {
		t.Fatalf("missing agent store Get() error = %v, want not found", err)
	}
	if _, err := store.Get(second.Agent.Agent.ID); err != nil {
		t.Fatalf("unrelated live agent %q removed: %v", second.Agent.Agent.ID, err)
	}
}

func restoreDaemonFactories(t *testing.T) {
	t.Helper()
	busFactory := newDaemonEventBus
	storeFactory := newDaemonStore
	detectorLoader := loadDaemonDetector
	monitorFactory := newDaemonMonitor
	backendFactory := newDaemonBackend
	runnerFactory := newDaemonSubscriptionRunner
	runtimeFactory := newDaemonRuntimeService
	agentFactory := newDaemonAgentService
	t.Cleanup(func() {
		newDaemonEventBus = busFactory
		newDaemonStore = storeFactory
		loadDaemonDetector = detectorLoader
		newDaemonMonitor = monitorFactory
		newDaemonBackend = backendFactory
		newDaemonSubscriptionRunner = runnerFactory
		newDaemonRuntimeService = runtimeFactory
		newDaemonAgentService = agentFactory
	})
}

type daemonFakeBackend struct {
	mu      sync.Mutex
	panes   []zellij.Pane
	created int
}

func newDaemonFakeBackend() *daemonFakeBackend {
	return &daemonFakeBackend{panes: []zellij.Pane{{ID: "source-pane", TabID: 7, TabName: "main"}}}
}

func (b *daemonFakeBackend) Session() string { return "physical-a" }
func (b *daemonFakeBackend) CreateTab(context.Context, zellij.CreateTabRequest) (zellij.TabID, error) {
	return 7, nil
}
func (b *daemonFakeBackend) CloseTab(context.Context, zellij.CloseTabRequest) error { return nil }
func (b *daemonFakeBackend) CreatePane(_ context.Context, req zellij.CreatePaneRequest) (zellij.PaneID, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created++
	id := zellij.PaneID(fmt.Sprintf("agent-pane-%d", b.created))
	tabID := 7
	if req.TabID != nil {
		tabID = int(*req.TabID)
	}
	b.panes = append(b.panes, zellij.Pane{ID: id, TabID: tabID, TabName: "main", CWD: req.CWD})
	return id, nil
}
func (b *daemonFakeBackend) ClosePane(_ context.Context, req zellij.ClosePaneRequest) error {
	b.removePane(req.PaneID)
	return nil
}
func (b *daemonFakeBackend) SendInput(context.Context, zellij.SendInputRequest) error { return nil }
func (b *daemonFakeBackend) ListPanes(context.Context, zellij.ListPanesRequest) ([]zellij.Pane, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]zellij.Pane(nil), b.panes...), nil
}
func (b *daemonFakeBackend) DumpScreen(context.Context, zellij.DumpScreenRequest) (string, error) {
	return "", nil
}
func (b *daemonFakeBackend) SubscribeCommand(zellij.SubscribeRequest) (zellij.CommandSpec, error) {
	return zellij.CommandSpec{Name: "fake-subscribe"}, nil
}
func (b *daemonFakeBackend) SwitchSession(context.Context, zellij.SwitchSessionRequest) error {
	return nil
}
func (b *daemonFakeBackend) createCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.created
}
func (b *daemonFakeBackend) removePane(id zellij.PaneID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, pane := range b.panes {
		if pane.ID == id {
			b.panes = append(b.panes[:i], b.panes[i+1:]...)
			return
		}
	}
}

func (b *daemonFakeBackend) addPane(pane zellij.Pane) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.panes = append(b.panes, pane)
}

type daemonFakeSubscriptionRunner struct{}

func (daemonFakeSubscriptionRunner) Start(context.Context, zellij.CommandSpec) (*agentruntime.SubscriptionStream, error) {
	return &agentruntime.SubscriptionStream{Stdout: io.NopCloser(strings.NewReader("")), Wait: func() error { return nil }}, nil
}

type daemonFakeTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newDaemonFakeTicker() *daemonFakeTicker {
	return &daemonFakeTicker{ticks: make(chan time.Time, 4), stopped: make(chan struct{})}
}
func (t *daemonFakeTicker) C() <-chan time.Time { return t.ticks }
func (t *daemonFakeTicker) Stop()               { t.once.Do(func() { close(t.stopped) }) }
func (t *daemonFakeTicker) tick()               { t.ticks <- time.Now() }

type daemonFakeReconciler struct {
	mu     sync.Mutex
	calls  int
	errors []error
	called chan struct{}
}

type daemonBlockingReconciler struct {
	started chan struct{}
	release chan struct{}
}

func (f *daemonBlockingReconciler) Reconcile(ctx context.Context, _ agentruntime.ReconcileRequest) (agentruntime.ReconcileResponse, error) {
	f.started <- struct{}{}
	select {
	case <-ctx.Done():
		return agentruntime.ReconcileResponse{}, ctx.Err()
	case <-f.release:
		return agentruntime.ReconcileResponse{}, nil
	}
}

func (f *daemonFakeReconciler) Reconcile(context.Context, agentruntime.ReconcileRequest) (agentruntime.ReconcileResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.called == nil {
		f.called = make(chan struct{}, 8)
	}
	select {
	case f.called <- struct{}{}:
	default:
	}
	if f.calls <= len(f.errors) {
		return agentruntime.ReconcileResponse{}, f.errors[f.calls-1]
	}
	return agentruntime.ReconcileResponse{}, nil
}

func (f *daemonFakeReconciler) waitCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		got := f.calls
		f.mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconcile calls = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRunStopFallsBackForDaemonWithoutShutdownRoute(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agentd.sock")
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("create socket placeholder: %v", err)
	}
	originalRequest := requestDaemonShutdown
	originalLegacy := stopLegacyDaemon
	t.Cleanup(func() {
		requestDaemonShutdown = originalRequest
		stopLegacyDaemon = originalLegacy
	})
	requestDaemonShutdown = func(context.Context, string, time.Duration) error {
		return &transport.ClientError{APIError: transport.APIError{Code: transport.CodeNotFound, Message: "route not found"}}
	}
	legacyCalled := false
	stopLegacyDaemon = func(_ context.Context, gotSocketPath string) error {
		legacyCalled = true
		if gotSocketPath != socketPath {
			t.Fatalf("legacy socket path = %q, want %q", gotSocketPath, socketPath)
		}
		return os.Remove(gotSocketPath)
	}
	var stdout, stderr bytes.Buffer

	code := RunContext(context.Background(), []string{"stop", "--socket", socketPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("RunContext() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !legacyCalled {
		t.Fatal("legacy stop fallback was not called")
	}
}

func TestRunStopDoesNotFallBackForOtherShutdownErrors(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agentd.sock")
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("create socket placeholder: %v", err)
	}
	originalRequest := requestDaemonShutdown
	originalLegacy := stopLegacyDaemon
	t.Cleanup(func() {
		requestDaemonShutdown = originalRequest
		stopLegacyDaemon = originalLegacy
	})
	requestDaemonShutdown = func(context.Context, string, time.Duration) error {
		return errors.New("connection failed")
	}
	stopLegacyDaemon = func(context.Context, string) error {
		t.Fatal("legacy stop fallback should not be called")
		return nil
	}
	var stdout, stderr bytes.Buffer

	code := RunContext(context.Background(), []string{"stop", "--socket", socketPath}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("RunContext() exit code = %d, want 1", code)
	}
}
