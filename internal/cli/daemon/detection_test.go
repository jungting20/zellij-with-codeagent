package daemoncli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/eventbus"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/zellij"
)

func TestDaemonDetectionExplainsReconciledTitleAndConfirmsUnrecognizedCompletion(t *testing.T) {
	restoreDaemonFactories(t)
	backend := newDaemonFakeBackend()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	timers := make(chan *daemonDetectionTimer, 16)
	loadDaemonDetector = codingagent.LoadEmbeddedDetector
	newDaemonBackend = func() daemonBackend { return backend }
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner {
		return daemonDetectionStream{reader: reader, writer: writer}
	}
	newDaemonMonitor = func(opts codingagent.MonitorOptions) *codingagent.Monitor {
		opts.AfterFunc = func(delay time.Duration, fn func()) codingagent.Timer {
			timer := &daemonDetectionTimer{delay: delay, fn: fn}
			timers <- timer
			return timer
		}
		return codingagent.NewMonitor(opts)
	}
	bundle, err := newRuntimeBundle()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bundle.stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, unsubscribe := bundle.bus.Subscribe(ctx)
	defer unsubscribe()
	started, err := bundle.service.StartAgent(ctx, codingagent.StartAgentRequest{
		Kind: codingagent.KindCodex, CWD: t.TempDir(),
		SourceZellijSession: "physical-a", SourceZellijPaneID: "source-pane",
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(transport.ServerOptions{
		Service: bundle.service, VoiceNotifications: voiceQueueAdapter{service: &fakeDaemonVoiceService{}}, SocketPath: "unused.sock",
	})
	if err != nil {
		t.Fatal(err)
	}
	explain := func() transport.ExplainAgentResponse {
		t.Helper()
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agents/"+string(started.Agent.Agent.ID)+"/explain", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("explain status = %d: %s", response.Code, response.Body.String())
		}
		var explanation transport.ExplainAgentResponse
		if err := json.Unmarshal(response.Body.Bytes(), &explanation); err != nil {
			t.Fatal(err)
		}
		return explanation
	}
	nextTimer := func(delay time.Duration) *daemonDetectionTimer {
		t.Helper()
		select {
		case timer := <-timers:
			if timer.delay != delay {
				t.Fatalf("scheduled delay = %s, want %s", timer.delay, delay)
			}
			return timer
		case <-time.After(3 * time.Second):
			t.Fatalf("missing %s detection timer", delay)
			return nil
		}
	}
	viewport := func(text string) {
		t.Helper()
		if err := json.NewEncoder(writer).Encode(map[string]any{
			"event": "pane_update", "pane_id": started.Agent.Pane.ZellijPaneID, "viewport": []string{text},
		}); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(3 * time.Second)
		for {
			select {
			case event := <-events:
				// Runtime publishes raw output after the monitor processes it.
				if event.Type == eventbus.TypeRawOutput && event.Message == text {
					return
				}
			case <-deadline:
				t.Fatal("viewport did not reach the runtime observer")
			}
		}
	}
	reconcileTitle := func(title string) {
		t.Helper()
		backend.mu.Lock()
		for i := range backend.panes {
			if backend.panes[i].ID == zellij.PaneID(started.Agent.Pane.ZellijPaneID) {
				backend.panes[i].Title = title
				backend.panes[i].TitleAvailable = true
			}
		}
		backend.mu.Unlock()
		if _, err := bundle.service.Reconcile(ctx, agentruntime.ReconcileRequest{}); err != nil {
			t.Fatal(err)
		}
	}

	viewport("• Working (12s • esc to interrupt)")
	nextTimer(3 * time.Second).fire(t)
	if got := explain(); got.State != codingagent.StateWorking || got.MatchedRule != "screen_working_fallback" {
		t.Fatalf("working screen explanation = %#v", got)
	}
	reconcileTitle("⠸ Working | project")
	if got := explain(); got.State != codingagent.StateWorking || got.MatchedRule != "osc_title_working" || !got.TitleAvailable || !got.TitleUsedForDetection || got.TitleSource != "zellij_pane_title" || got.ProgressAvailable {
		t.Fatalf("reconciled title explanation = %#v", got)
	}
	viewport("Everything is finished. Ready for another task.")
	reconcileTitle("project")
	if got := explain(); got.State != codingagent.StateWorking || !got.PendingIdle || got.TitleUsedForDetection || got.Title != "project" || got.Detection == nil || !got.Detection.Detection.Fallback {
		t.Fatalf("ordinary title bypassed idle confirmation: %#v", got)
	}
	confirmation := nextTimer(100 * time.Millisecond)
	deadline := nextTimer(700 * time.Millisecond)
	for count := 1; count <= 3; count++ {
		confirmation.fire(t)
		got := explain()
		if count < 3 {
			if got.State != codingagent.StateWorking || !got.PendingIdle || got.IdleConfirmations != count {
				t.Fatalf("confirmation %d explanation = %#v", count, got)
			}
			confirmation = nextTimer(100 * time.Millisecond)
		} else if got.State != codingagent.StateIdle || got.PendingIdle || got.Detection == nil || !got.Detection.Detection.Fallback {
			t.Fatalf("confirmed completion explanation = %#v", got)
		}
	}
	if !deadline.stopped.Load() {
		t.Fatal("confirmed idle did not cancel the fallback deadline")
	}
	stored, err := bundle.store.Get(started.Agent.Agent.ID)
	if err != nil || stored.State != codingagent.StateIdle {
		t.Fatalf("stored agent = %#v, error = %v", stored, err)
	}
}

type daemonDetectionTimer struct {
	delay   time.Duration
	fn      func()
	stopped atomic.Bool
}

func (t *daemonDetectionTimer) Stop() bool { return !t.stopped.Swap(true) }

func (timer *daemonDetectionTimer) fire(t *testing.T) {
	t.Helper()
	if timer.stopped.Swap(true) {
		t.Fatal("attempted to fire a stopped detection timer")
	}
	timer.fn()
}

type daemonDetectionStream struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (s daemonDetectionStream) Start(ctx context.Context, _ zellij.CommandSpec) (*agentruntime.SubscriptionStream, error) {
	go func() {
		<-ctx.Done()
		_ = s.writer.Close()
	}()
	return &agentruntime.SubscriptionStream{Stdout: s.reader, Wait: func() error { return nil }}, nil
}
