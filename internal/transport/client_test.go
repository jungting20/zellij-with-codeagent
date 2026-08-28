package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

func TestClientAgentMethodsUseExactPathsMethodsAndEscaping(t *testing.T) {
	var calls []string
	var nextBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.URL.EscapedPath() == "/v1/agents/next" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read next request body: %v", err)
			}
			nextBody = string(body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch len(calls) {
		case 1, 3, 4:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"agent":{"agent":{"id":"agent/1","kind":"codex","pane_id":"agent/1","state":"unknown","created_at":"1970-01-01T00:00:10Z","state_changed_at":"1970-01-01T00:00:20Z"},"pane":{"id":"agent/1","status":"running","created_at":"1970-01-01T00:00:10Z","updated_at":"1970-01-01T00:00:30Z"}}}`))
		case 2:
			_, _ = w.Write([]byte(`{"agents":[]}`))
		case 5:
			_, _ = w.Write([]byte(`{"focused":true,"agent":{"agent":{"id":"agent/1","kind":"codex","pane_id":"agent/1","state":"unknown","created_at":"1970-01-01T00:00:10Z","state_changed_at":"1970-01-01T00:00:20Z"},"pane":{"id":"agent/1","status":"running","created_at":"1970-01-01T00:00:10Z","updated_at":"1970-01-01T00:00:30Z"}}}`))
		case 6:
			_, _ = w.Write([]byte(`{"focused":false,"agent":{}}`))
		}
	}))
	defer server.Close()
	client := NewClient(ClientOptions{})
	client.baseURL = server.URL
	client.http = server.Client()

	request := StartAgentRequest{Kind: "codex", CWD: "/workspace/project", Args: []string{"--model", "gpt-5"}, SourceSession: "physical-a", SourceZellijPaneID: "terminal_2"}
	started, err := client.StartAgent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if started.Agent.Agent.ID != "agent/1" || !started.Agent.Agent.StateChangedAt.Equal(time.Unix(20, 0)) || !started.Agent.Pane.UpdatedAt.Equal(time.Unix(30, 0)) {
		t.Fatalf("StartAgent() = %#v", started)
	}
	if _, err := client.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FocusAgent(context.Background(), "agent/1", FocusAgentRequest{SourceSession: "physical-a", SourceZellijPaneID: "terminal_2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FocusAgent(context.Background(), "agent%2F1", FocusAgentRequest{SourceSession: "physical-a", SourceZellijPaneID: "terminal_2"}); err != nil {
		t.Fatal(err)
	}
	next, err := client.FocusNextAgent(context.Background(), FocusNextAgentRequest{SourceSession: "physical-b", SourceZellijPaneID: "terminal_8"})
	if err != nil {
		t.Fatal(err)
	}
	if !next.Focused || next.Agent.Agent.ID != "agent/1" {
		t.Fatalf("FocusNextAgent() = %#v", next)
	}
	noOp, err := client.FocusNextAgent(context.Background(), FocusNextAgentRequest{SourceSession: "physical-b", SourceZellijPaneID: "terminal_8"})
	if err != nil {
		t.Fatal(err)
	}
	if noOp.Focused || noOp.Agent.Agent.ID != "" {
		t.Fatalf("FocusNextAgent() = %#v, want no-op", noOp)
	}
	want := []string{"POST /v1/agents", "GET /v1/agents", "POST /v1/agents/agent%2F1/focus", "POST /v1/agents/agent%252F1/focus", "POST /v1/agents/next", "POST /v1/agents/next"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if nextBody != `{"source_session":"physical-b","source_zellij_pane_id":"terminal_8"}` {
		t.Fatalf("FocusNextAgent request JSON = %q", nextBody)
	}
}

func TestClientFocusSessionUsesExactPathAndBody(t *testing.T) {
	var method, path, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"loop","zellij_pane_id":"terminal_0"}`))
	}))
	defer server.Close()
	client := NewClient(ClientOptions{})
	client.baseURL = server.URL
	client.http = server.Client()

	response, err := client.FocusSession(context.Background(), "loop", FocusSessionRequest{
		SourceSession: "dashboard", SourceZellijPaneID: "terminal_9",
	})
	if err != nil {
		t.Fatalf("FocusSession() error = %v", err)
	}
	if method != http.MethodPost || path != "/v1/sessions/loop/focus" {
		t.Fatalf("request = %s %s", method, path)
	}
	if body != `{"source_session":"dashboard","source_zellij_pane_id":"terminal_9"}` {
		t.Fatalf("request body = %q", body)
	}
	if response.SessionID != "loop" || response.ZellijPaneID != "terminal_0" {
		t.Fatalf("FocusSession() = %#v", response)
	}
}

func TestClientCreatePaneOverUnixSocket(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.CreatePane(context.Background(), CreatePaneRequest{
		ID:            "pane-1",
		TaskID:        "task-1",
		Role:          "test",
		ZellijSession: "physical-a",
		NewTab:        true,
		TabName:       "agentd-test",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if response.Pane.ID != "pane-1" || response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 7 {
		t.Fatalf("CreatePane() = %#v, want pane metadata", response.Pane)
	}
}

func TestClientReturnsStructuredTransportError(t *testing.T) {
	service := newFakeRuntimeService()
	service.sendErr = rt.ErrPaneNotFound
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	err := client.SendInput(context.Background(), "missing", SendInputRequest{Text: "noop"})
	var clientErr *ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("SendInput() error = %T %v, want ClientError", err, err)
	}
	if clientErr.APIError.Code != CodeNotFound || clientErr.StatusCode != 404 {
		t.Fatalf("ClientError = %#v, want not_found 404", clientErr)
	}
}

func TestClientSendMessage(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.SendMessage(context.Background(), SendMessageRequest{
		From: "planner",
		To:   "tester",
		Type: "task_request",
		Body: "run tests",
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if response.From.ID != "planner" || response.To.ID != "tester" || response.Body != "run tests" {
		t.Fatalf("SendMessage() = %#v, want planner to tester response", response)
	}
	if service.messageReq.FromPaneID != "planner" || service.messageReq.ToPaneID != "tester" {
		t.Fatalf("runtime message request = %#v, want planner to tester", service.messageReq)
	}
}

func TestClientWaitForOutputMarkerDisablesHTTPTimeout(t *testing.T) {
	service := newFakeRuntimeService()
	service.markerResponse = rt.WaitForOutputMarkerResponse{
		PaneID:      "worker-1",
		Marker:      "DONE ",
		MatchedLine: "DONE ticket_id=TICKET-123",
		MatchedAt:   time.Unix(3, 0),
	}
	service.markerBlock = make(chan struct{})
	baseClient, cleanup := startUnixTransport(t, service)
	defer cleanup()
	client := NewClient(ClientOptions{SocketPath: baseClient.socketPath, Timeout: 20 * time.Millisecond})

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(service.markerBlock)
	}()
	response, err := client.WaitForOutputMarker(context.Background(), "worker-1", WaitForOutputMarkerRequest{Marker: "DONE ", MatchPrefix: true})
	if err != nil {
		t.Fatalf("WaitForOutputMarker() error = %v", err)
	}
	if response.PaneID != "worker-1" || response.Marker != "DONE " || response.MatchedLine != "DONE ticket_id=TICKET-123" || !response.MatchedAt.Equal(time.Unix(3, 0)) {
		t.Fatalf("WaitForOutputMarker() = %#v, want structured marker", response)
	}
}

func TestClientClosePane(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.ClosePane(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
	if response.Pane.ID != "worker-1" || service.closeReq.PaneID != "worker-1" {
		t.Fatalf("ClosePane() = %#v request=%#v, want logical worker-1", response, service.closeReq)
	}
}

func TestClientStreamsEvents(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}
	defer stream.Close()

	service.publish(eventbus.Event{Type: eventbus.TypeTestPassed, PaneID: "test", Message: "ok", Time: time.Unix(1, 0)})

	select {
	case event := <-stream.Events:
		if event.Type != string(eventbus.TypeTestPassed) || event.PaneID != "test" {
			t.Fatalf("event = %#v, want test_passed for test", event)
		}
	case err := <-stream.Errors:
		t.Fatalf("stream error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for streamed event")
	}
}

func TestClientStreamsEventsByType(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.StreamEventsByType(ctx, string(eventbus.TypeAgentStateChanged))
	if err != nil {
		t.Fatalf("StreamEventsByType() error = %v", err)
	}
	defer stream.Close()

	service.publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "agent", Message: "large viewport"})
	service.publish(eventbus.Event{Type: eventbus.TypeAgentStateChanged, PaneID: "agent", AgentState: "idle"})

	select {
	case event := <-stream.Events:
		if event.Type != string(eventbus.TypeAgentStateChanged) || event.PaneID != "agent" {
			t.Fatalf("event = %#v, want agent_state_changed for agent", event)
		}
	case err := <-stream.Errors:
		t.Fatalf("stream error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for filtered event")
	}
}

func TestClientSubmitExecutionPlan(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.SubmitExecutionPlan(context.Background(), "req_123", ExecutionPlanPayload{
		Session:       "feature-auth",
		ZellijSession: "physical-a",
		Layout:        "triple-horizontal",
		Tabs: []ExecutionPlanTab{
			{
				Name: "feature-auth",
				Panes: []ExecutionPlanPane{
					{ID: "planner", Role: "planner"},
					{ID: "frontend", Role: "react-dev"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("SubmitExecutionPlan() error = %v", err)
	}
	if response.RequestID != "req_123" || len(response.Tabs) != 1 || len(response.Tabs[0].Panes) != 2 {
		t.Fatalf("SubmitExecutionPlan() = %#v, want req_123 and one tab with two panes", response)
	}
	if !service.applyPlanCalled || service.applyPlanReq.Session != "feature-auth" || service.applyPlanReq.ZellijSession != "physical-a" {
		t.Fatalf("runtime request = %#v, want execution plan applied", service.applyPlanReq)
	}
}

func TestClientRecentEvents(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()

	response, err := client.RecentEvents(context.Background(), 1, string(eventbus.TypeTestPassed))
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}
	if len(response.Events) != 1 || response.Events[0].Type != string(eventbus.TypeTestPassed) {
		t.Fatalf("RecentEvents() = %#v, want one test_passed event", response)
	}
	if service.recentReq.Limit != 1 {
		t.Fatalf("runtime recent limit = %d, want 1", service.recentReq.Limit)
	}
}

func TestClientAutoStartsDaemonOnMissingSocket(t *testing.T) {
	service := newFakeRuntimeService()
	socketPath := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	startCalls := 0

	client := NewClient(ClientOptions{
		SocketPath:    socketPath,
		Timeout:       time.Second,
		AutoStart:     true,
		StartTimeout:  time.Second,
		StartLockPath: socketPath + ".lock",
		StartDaemon: func(_ context.Context, opts DaemonStartOptions) error {
			startCalls++
			if opts.SocketPath != socketPath {
				t.Fatalf("SocketPath = %q, want %q", opts.SocketPath, socketPath)
			}
			server, err := NewServer(ServerOptions{
				Service:            service,
				VoiceNotifications: noopVoiceNotificationService{},
				SocketPath:         socketPath,
				RequestTimeout:     time.Second,
				Version:            "test",
			})
			if err != nil {
				return err
			}
			go func() {
				errCh <- server.ListenAndServe(ctx)
			}()
			return nil
		},
	})
	defer func() {
		cancel()
		if startCalls == 0 {
			return
		}
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("ListenAndServe() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out stopping transport server")
		}
	}()

	response, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("Health() status = %q, want ok", response.Status)
	}
	if startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", startCalls)
	}
}

func startUnixTransport(t *testing.T, service *fakeRuntimeService) (*Client, func()) {
	t.Helper()
	socketPath := shortSocketPath(t)
	server, err := NewServer(ServerOptions{
		Service:            service,
		VoiceNotifications: noopVoiceNotificationService{},
		SocketPath:         socketPath,
		RequestTimeout:     time.Second,
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()

	client := NewClient(ClientOptions{SocketPath: socketPath, Timeout: time.Second})
	deadline := time.After(time.Second)
	for {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for unix transport health")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cleanup := func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("ListenAndServe() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out stopping transport server")
		}
	}
	return client, cleanup
}
