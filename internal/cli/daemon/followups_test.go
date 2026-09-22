package daemoncli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/followup"
	"zellij-with-codeagent/internal/persistence"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/zellij"
)

// Uses the production daemon bundle, monitor, SQLite writer and transport. Only
// Zellij I/O is replaced; snapshots must actually drive the detected work cycle.
func TestDaemonFollowupsDeliverThroughRuntimeAndPreserveRestartUncertainty(t *testing.T) {
	restoreDaemonFactories(t)
	backend := &followupDaemonBackend{daemonFakeBackend: newDaemonFakeBackend(), screen: "› "}
	newDaemonBackend = func() daemonBackend { return backend }
	reader, pipe := io.Pipe()
	t.Cleanup(func() { _ = pipe.Close() })
	newDaemonSubscriptionRunner = func() agentruntime.SubscriptionRunner { return daemonDetectionStream{reader: reader, writer: pipe} }
	grace := make(chan *daemonDetectionTimer, 8)
	newDaemonMonitor = func(opts codingagent.MonitorOptions) *codingagent.Monitor {
		opts.AfterFunc = func(delay time.Duration, fn func()) codingagent.Timer {
			timer := &daemonDetectionTimer{delay: delay, fn: fn}
			grace <- timer
			return timer
		}
		return codingagent.NewMonitor(opts)
	}
	path := filepath.Join(t.TempDir(), "daemon.db")
	writer, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := newRuntimeBundle(writer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { bundle.stop(); _ = writer.Close(context.Background()) }()
	ctx := context.Background()
	started, err := bundle.service.StartAgent(ctx, codingagent.StartAgentRequest{
		Kind: codingagent.KindCodex, CWD: t.TempDir(), SourceZellijSession: "physical-a", SourceZellijPaneID: "source-pane",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := string(started.Agent.Agent.ID)
	server, err := transport.NewServer(transport.ServerOptions{Service: bundle.service, Followups: bundle.followups, VoiceNotifications: voiceQueueAdapter{service: &fakeDaemonVoiceService{}}, SocketPath: "unused.sock"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) followup.Queue {
		t.Helper()
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/agents/"+id+"/followups", strings.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("followup: %d %s", recorder.Code, recorder.Body.String())
		}
		var response transport.AgentFollowupsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Queue
	}
	// Pause an empty queue before preparing several instructions.
	post(`{"action":"pause"}`)
	post(`{"action":"add","text":"first","request_id":"first"}`)
	post(`{"action":"add","text":"second","request_id":"second"}`)
	select {
	case timer := <-grace:
		timer.fire(t)
	case <-time.After(time.Second):
		t.Fatal("missing startup grace")
	}
	bundle.followups.Step(ctx)
	if got := backend.inputs(); len(got) != 0 {
		t.Fatalf("paused input=%v", got)
	}
	post(`{"action":"resume"}`)
	bundle.followups.Step(ctx)
	bundle.followups.Step(ctx)
	if got := backend.inputs(); len(got) != 1 || got[0].Text != "first\n" {
		t.Fatalf("first input=%v", got)
	}
	backend.setScreen("• Working (2s • esc to interrupt)\n› ")
	bundle.followups.Step(ctx)
	backend.setScreen("Implemented.\n› ")
	bundle.followups.Step(ctx)
	if got := backend.inputs(); len(got) != 2 || got[1].Text != "second\n" {
		t.Fatalf("work cycle input=%v", got)
	}
	// The second send was acknowledged but its work cycle is still unknown.
	bundle.stop()
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	writer, err = persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := followup.New(followup.Options{Repository: followup.SQLiteRepository{Writer: writer},
		Observe: func(context.Context, string) (followup.Observation, error) {
			t.Fatal("recovery must not inspect/send")
			return followup.Observation{}, nil
		},
		Send: func(context.Context, followup.Observation, string) error {
			t.Fatal("recovery replayed input")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, err := restored.Get(ctx, id)
	if err != nil || !q.Paused || q.Phase != followup.NeedsAttention || q.Items[1].State != followup.NeedsAttention {
		t.Fatalf("recovery=%+v err=%v", q, err)
	}
}

type followupDaemonBackend struct {
	*daemonFakeBackend
	inputMu sync.Mutex
	screen  string
	sent    []zellij.SendInputRequest
}

func (b *followupDaemonBackend) DumpScreen(context.Context, zellij.DumpScreenRequest) (string, error) {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	return b.screen, nil
}
func (b *followupDaemonBackend) SendInput(_ context.Context, req zellij.SendInputRequest) error {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	b.sent = append(b.sent, req)
	return nil
}
func (b *followupDaemonBackend) setScreen(text string) {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	b.screen = text
}
func (b *followupDaemonBackend) inputs() []zellij.SendInputRequest {
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	return append([]zellij.SendInputRequest(nil), b.sent...)
}
