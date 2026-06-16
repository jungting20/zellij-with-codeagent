package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: agentctl") {
		t.Fatalf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHealth(t *testing.T) {
	client := &fakeAgentClient{
		healthResponse: transport.HealthResponse{Status: "ok", Version: "test"},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"health", "--socket", "/tmp/custom.sock"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" {
		t.Fatalf("socket path = %q, want custom socket", client.socketPath)
	}
	if !strings.Contains(stdout.String(), "agentd ok (test)") {
		t.Fatalf("stdout = %q, want health summary", stdout.String())
	}
}

func TestRunStatusPrintsRuntimeSummary(t *testing.T) {
	client := &fakeAgentClient{
		statusResponse: transport.InspectRuntimeResponse{
			Message: "runtime healthy",
			Counts:  transport.RuntimeCounts{Managed: 1, Active: 1, Running: 1},
			Panes: []transport.Pane{
				{ID: "tester", Role: "test", TaskID: "task-1", Status: "running", ZellijPaneID: "terminal_1"},
			},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"status"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "runtime healthy") || !strings.Contains(output, "- tester role=test task=task-1 status=running") {
		t.Fatalf("stdout = %q, want runtime summary", output)
	}
}

func TestRunPlanSubmitsExecutionPlanFile(t *testing.T) {
	planPath := writeTempFile(t, `{
		"session": "feature-auth",
		"layout": "triple-horizontal",
		"tabs": [
			{"name": "main", "panes": [{"id": "planner", "role": "planner"}]}
		]
	}`)
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--file", planPath, "--request-id", "req_123"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID != "req_123" || client.planPayload.Session != "feature-auth" {
		t.Fatalf("submitted plan = request %q payload %#v, want feature-auth", client.planRequestID, client.planPayload)
	}
	if len(client.planPayload.Tabs) != 1 || client.planPayload.Tabs[0].Panes[0].ID != "planner" {
		t.Fatalf("submitted tabs = %#v, want planner pane", client.planPayload.Tabs)
	}
	if !strings.Contains(stdout.String(), "request=req_123 session=feature-auth") {
		t.Fatalf("stdout = %q, want plan summary", stdout.String())
	}
}

func TestRunPlanAcceptsRequestEnvelopeFromStdin(t *testing.T) {
	input := `{
		"type": "execution_plan",
		"request_id": "req_from_stdin",
		"payload": {
			"session": "demo",
			"tabs": [
				{"name": "demo", "panes": [{"id": "coder", "role": "coder"}]}
			]
		}
	}`
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--file", "-"}, strings.NewReader(input), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID != "req_from_stdin" || client.planPayload.Session != "demo" {
		t.Fatalf("submitted plan = request %q payload %#v, want stdin envelope", client.planRequestID, client.planPayload)
	}
}

func TestRunPlanAcceptsCanonicalBadgeCategoryEnvelope(t *testing.T) {
	planPath := filepath.Join("..", "..", "examples", "plans", "badge-category-source-lsp.json")
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--file", planPath}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID != "req_badge_category_source_lsp" {
		t.Fatalf("request id = %q, want req_badge_category_source_lsp", client.planRequestID)
	}
	if client.planPayload.Session != "badge-category-source-lsp" || client.planPayload.Layout != "triple-horizontal" {
		t.Fatalf("payload = %#v, want canonical badge-category payload", client.planPayload)
	}
	if len(client.planPayload.Tabs) != 1 || len(client.planPayload.Tabs[0].Panes) != 2 {
		t.Fatalf("tabs = %#v, want one tab with editor and lsp panes", client.planPayload.Tabs)
	}
	if client.planPayload.Tabs[0].Panes[0].ID != "badge-category-editor" || client.planPayload.Tabs[0].Panes[1].ID != "badge-category-lsp" {
		t.Fatalf("panes = %#v, want editor and lsp", client.planPayload.Tabs[0].Panes)
	}
}

func TestRunEventsPassesFilters(t *testing.T) {
	client := &fakeAgentClient{
		eventsResponse: transport.RecentEventsResponse{
			Events: []transport.Event{
				{Type: "test_passed", PaneID: "test", TaskID: "task-1", Message: "ok", Time: time.Unix(1, 0)},
			},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"events", "--limit", "5", "--type", "test_passed"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.eventsLimit != 5 || len(client.eventTypes) != 1 || client.eventTypes[0] != "test_passed" {
		t.Fatalf("event filters = limit %d types %#v, want test_passed", client.eventsLimit, client.eventTypes)
	}
	if !strings.Contains(stdout.String(), "type=test_passed pane=test") {
		t.Fatalf("stdout = %q, want event summary", stdout.String())
	}
}

func TestRunEventsFollowStreamsAndFilters(t *testing.T) {
	events := make(chan transport.Event, 2)
	events <- transport.Event{Type: "raw_output", PaneID: "coder", Message: "ignored", Time: time.Unix(1, 0)}
	events <- transport.Event{Type: "message_sent", PaneID: "tester", Message: "delivered", Time: time.Unix(2, 0)}
	close(events)
	errs := make(chan error)
	client := &fakeAgentClient{
		eventStream: &transport.EventStream{
			Events: events,
			Errors: errs,
			Close:  func() error { return nil },
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"events", "--follow", "--type", "message_sent"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !client.streamEventsCalled {
		t.Fatal("StreamEvents was not called")
	}
	if strings.Contains(output, "raw_output") || !strings.Contains(output, "type=message_sent pane=tester") {
		t.Fatalf("stdout = %q, want only filtered message_sent event", output)
	}
}

func TestRunInputSendsText(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"input", "pane-1", "--text", "hello\n"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.inputPaneID != "pane-1" || client.inputRequest.Text != "hello\n" {
		t.Fatalf("input request = pane %q %#v, want pane-1 hello", client.inputPaneID, client.inputRequest)
	}
	if !strings.Contains(stdout.String(), "sent input pane=pane-1 bytes=6") {
		t.Fatalf("stdout = %q, want input summary", stdout.String())
	}
}

func TestRunInputReadsStdin(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"input", "pane-1", "--file", "-"}, strings.NewReader("from stdin"), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.inputRequest.Text != "from stdin" {
		t.Fatalf("input text = %q, want stdin payload", client.inputRequest.Text)
	}
}

func TestRunSnapshotPrintsOutput(t *testing.T) {
	client := &fakeAgentClient{
		snapshotResponse: transport.SnapshotOutputResponse{
			Output: "pane output\n",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"snapshot", "pane-1", "--full", "--ansi"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.snapshotPaneID != "pane-1" || !client.snapshotRequest.Full || !client.snapshotRequest.ANSI {
		t.Fatalf("snapshot request = pane %q %#v, want full ansi", client.snapshotPaneID, client.snapshotRequest)
	}
	if stdout.String() != "pane output\n" {
		t.Fatalf("stdout = %q, want raw output", stdout.String())
	}
}

func TestRunMessageSendsBody(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"message", "--from", "planner", "--to", "tester", "--type", "task", "--body", "run tests"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.messageRequest.From != "planner" || client.messageRequest.To != "tester" || client.messageRequest.Type != "task" || client.messageRequest.Body != "run tests" {
		t.Fatalf("message request = %#v, want planner to tester task", client.messageRequest)
	}
	if !strings.Contains(stdout.String(), "delivered from=planner to=tester type=task bytes=9") {
		t.Fatalf("stdout = %q, want delivery summary", stdout.String())
	}
}

func TestRunForwardSnapshotSendsSnapshotOutput(t *testing.T) {
	client := &fakeAgentClient{
		snapshotResponse: transport.SnapshotOutputResponse{
			Output: "screen dump",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"forward-snapshot", "coder", "reviewer", "--full"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.snapshotPaneID != "coder" || !client.snapshotRequest.Full {
		t.Fatalf("snapshot request = pane %q %#v, want coder full", client.snapshotPaneID, client.snapshotRequest)
	}
	if client.messageRequest.From != "coder" || client.messageRequest.To != "reviewer" || client.messageRequest.Type != "screen_dump" || client.messageRequest.Body != "screen dump" {
		t.Fatalf("message request = %#v, want forwarded snapshot", client.messageRequest)
	}
	if !strings.Contains(stdout.String(), "delivered from=coder to=reviewer type=screen_dump bytes=11") {
		t.Fatalf("stdout = %q, want delivery summary", stdout.String())
	}
}

func TestRunCleanupPassesFilters(t *testing.T) {
	client := &fakeAgentClient{
		cleanupResponse: transport.CleanupResponse{
			Closed: []transport.Pane{{ID: "pane-1", Status: "closed"}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"cleanup", "--pane", "pane-1", "--task", "task-1", "--role", "test"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(client.cleanupRequest.PaneIDs) != 1 || client.cleanupRequest.PaneIDs[0] != "pane-1" || client.cleanupRequest.TaskID != "task-1" || client.cleanupRequest.Role != "test" {
		t.Fatalf("cleanup request = %#v, want pane/task/role filters", client.cleanupRequest)
	}
	if !strings.Contains(stdout.String(), "closed=1 failed=0 skipped=0") {
		t.Fatalf("stdout = %q, want cleanup summary", stdout.String())
	}
}

func TestRunPlanRequiresFile(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "plan requires --file") {
		t.Fatalf("stderr = %q, want missing file error", stderr.String())
	}
}

func writeTempFile(t *testing.T, contents string) string {
	t.Helper()
	path := t.TempDir() + "/plan.json"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func fakeFactory(client *fakeAgentClient) clientFactory {
	return func(socketPath string, timeout time.Duration) agentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration

	healthResponse   transport.HealthResponse
	statusResponse   transport.InspectRuntimeResponse
	snapshotResponse transport.SnapshotOutputResponse
	eventsResponse   transport.RecentEventsResponse
	cleanupResponse  transport.CleanupResponse
	messageResponse  transport.SendMessageResponse
	eventStream      *transport.EventStream

	planRequestID string
	planPayload   transport.ExecutionPlanPayload

	inputPaneID     string
	inputRequest    transport.SendInputRequest
	snapshotPaneID  string
	snapshotRequest transport.SnapshotOutputRequest
	messageRequest  transport.SendMessageRequest

	eventsLimit        int
	eventTypes         []string
	streamEventsCalled bool

	cleanupRequest transport.CleanupRequest
}

func (c *fakeAgentClient) Health(context.Context) (transport.HealthResponse, error) {
	return c.healthResponse, nil
}

func (c *fakeAgentClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	return c.statusResponse, nil
}

func (c *fakeAgentClient) SendInput(_ context.Context, paneID string, req transport.SendInputRequest) error {
	c.inputPaneID = paneID
	c.inputRequest = req
	return nil
}

func (c *fakeAgentClient) SnapshotOutput(_ context.Context, paneID string, req transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	c.snapshotPaneID = paneID
	c.snapshotRequest = req
	return c.snapshotResponse, nil
}

func (c *fakeAgentClient) SendMessage(_ context.Context, req transport.SendMessageRequest) (transport.SendMessageResponse, error) {
	c.messageRequest = req
	if c.messageResponse.From.ID != "" || c.messageResponse.To.ID != "" {
		return c.messageResponse, nil
	}
	return transport.SendMessageResponse{
		From: transport.Pane{ID: req.From},
		To:   transport.Pane{ID: req.To},
		Type: req.Type,
		Body: req.Body,
	}, nil
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.planRequestID = requestID
	c.planPayload = payload
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name: "main",
				Panes: []transport.Pane{
					{ID: "planner", Role: "planner", Status: "running", ZellijPaneID: "terminal_1"},
				},
			},
		},
	}, nil
}

func (c *fakeAgentClient) RecentEvents(_ context.Context, limit int, eventTypes ...string) (transport.RecentEventsResponse, error) {
	c.eventsLimit = limit
	c.eventTypes = append([]string(nil), eventTypes...)
	return c.eventsResponse, nil
}

func (c *fakeAgentClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	c.streamEventsCalled = true
	if c.eventStream != nil {
		return c.eventStream, nil
	}
	events := make(chan transport.Event)
	close(events)
	errs := make(chan error)
	return &transport.EventStream{
		Events: events,
		Errors: errs,
		Close:  func() error { return nil },
	}, nil
}

func (c *fakeAgentClient) Cleanup(_ context.Context, req transport.CleanupRequest) (transport.CleanupResponse, error) {
	c.cleanupRequest = req
	return c.cleanupResponse, nil
}
