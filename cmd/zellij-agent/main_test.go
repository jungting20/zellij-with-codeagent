package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcli "zellij-with-codeagent/internal/cli/agent"
	ticketworkercli "zellij-with-codeagent/internal/cli/ticketworker"
	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage: zellij-agent", "planner", "work", "chrome", "dashboard", "agent", "ticket-worker", "code-review", "debate-background"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDispatchesAgentHelp(t *testing.T) {
	for _, args := range [][]string{{"agent", "--help"}, {"agent", "start", "--help"}} {
		var stdout, stderr bytes.Buffer
		code := run(args, strings.NewReader(""), &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "Usage: zellij-agent agent") || stderr.Len() != 0 {
			t.Fatalf("run(%#v): code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunDispatchesAgentStart(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("ZELLIJ_SESSION_NAME", "session-a")
	t.Setenv("ZELLIJ_PANE_ID", "terminal_2")
	client := &fakeAgentClient{response: transport.StartAgentResponse{Agent: transport.AgentWithPane{
		Agent: transport.Agent{ID: "agent-1", Kind: "codex", PaneID: "pane-1"},
		Pane:  transport.Pane{ID: "pane-1"},
	}}}
	originalFactory := newAgentClient
	newAgentClient = func(socketPath string, timeout time.Duration) agentcli.AgentClient {
		client.socket, client.timeout = socketPath, timeout
		return client
	}
	t.Cleanup(func() { newAgentClient = originalFactory })
	var stdout, stderr bytes.Buffer

	code := run([]string{"agent", "start", "codex", "--cwd", cwd, "--", "--model", "gpt-5"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr=%q", code, stderr.String())
	}
	if client.request.Kind != "codex" || client.request.CWD != cwd || !reflect.DeepEqual(client.request.Args, []string{"--model", "gpt-5"}) {
		t.Fatalf("StartAgent request = %#v", client.request)
	}
	if stdout.String() != "started agent=agent-1 kind=codex pane=pane-1\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

type fakeAgentClient struct {
	request  transport.StartAgentRequest
	response transport.StartAgentResponse
	socket   string
	timeout  time.Duration
}

func (c *fakeAgentClient) StartAgent(_ context.Context, request transport.StartAgentRequest) (transport.StartAgentResponse, error) {
	c.request = request
	return c.response, nil
}

func TestRunDispatchesTicketWorkerHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"ticket-worker", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Usage: zellij-agent ticket-worker", "init", "add", "list", "next", "show", "start", "done", "cancel", "reopen"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if !strings.Contains(stdout.String(), "start   Start the ticket manager pane") || strings.Contains(stdout.String(), "Move a ready ticket to in_progress") {
		t.Fatalf("stdout = %q, want manager start description", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

type fakeTicketWorkerClient struct {
	requestID string
	payload   transport.ExecutionPlanPayload
}

func (c *fakeTicketWorkerClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.requestID = requestID
	c.payload = payload
	pane := payload.Tabs[0].Panes[0]
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{Name: payload.Tabs[0].Name, Panes: []transport.Pane{{ID: pane.ID, Role: pane.Role, Status: "starting"}}},
		},
	}, nil
}

func TestUnifiedTicketWorkerStartDispatchesPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ticketworker.InitializeProject(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")

	originalGetwd := getWorkingDirectory
	getWorkingDirectory = func() (string, error) { return root, nil }
	t.Cleanup(func() { getWorkingDirectory = originalGetwd })
	client := &fakeTicketWorkerClient{}
	originalFactory := newTicketWorkerClient
	newTicketWorkerClient = func(string, time.Duration) ticketworkercli.AgentClient { return client }
	t.Cleanup(func() { newTicketWorkerClient = originalFactory })
	var stdout, stderr bytes.Buffer

	code := run([]string{"ticket-worker", "start"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", code, stderr.String())
	}
	if client.requestID != ticketworker.StartRequestID(client.payload.Session) {
		t.Fatalf("request ID = %q, payload = %#v", client.requestID, client.payload)
	}
	pane := client.payload.Tabs[0].Panes[0]
	wantPrefix := []string{executablePath(), "role", "ticket-manager"}
	if pane.Role != "ticket-manager" || len(pane.Command) < len(wantPrefix) {
		t.Fatalf("pane = %#v", pane)
	}
	for i, want := range wantPrefix {
		if pane.Command[i] != want {
			t.Fatalf("command = %#v, want prefix %#v", pane.Command, wantPrefix)
		}
	}
}

func TestRunDispatchesTicketWorkerInit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := getWorkingDirectory
	getWorkingDirectory = func() (string, error) { return root, nil }
	t.Cleanup(func() { getWorkingDirectory = original })
	var stdout, stderr bytes.Buffer

	code := run([]string{"ticket-worker", "init"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	databasePath := filepath.Join(root, ".zellij-agent", "ticket-worker", "tickets.db")
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("database stat error = %v", err)
	}
	if _, err := os.Stat(ticketworker.ConfigPath(root)); err != nil {
		t.Fatalf("config stat error = %v", err)
	}
	cfg, err := ticketworker.LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.MaxWorkers != 3 || cfg.PollInterval != 30*time.Second {
		t.Fatalf("config defaults = %+v", cfg)
	}
	ignored, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil || !strings.Contains(string(ignored), ".zellij-agent/ticket-worker/") {
		t.Fatalf(".gitignore = %q, error = %v", ignored, err)
	}
}

func TestRunDispatchesDashboardHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"dashboard", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 || !strings.Contains(stdout.String(), "Usage: zellij-agent dashboard") || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunDispatchesChromeDryRun(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := run([]string{"chrome", "--cwd", cwd, "--session", "chrome-debug", "--dry-run", "--", "--port", "9333"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_chrome-debug" {
		t.Fatalf("envelope = %#v, want chrome execution plan", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "chrome-debug" || payload.Layout != "single-tab" || len(payload.Tabs) != 1 || payload.Tabs[0].Name != "chrome" {
		t.Fatalf("payload = %#v, want chrome-debug single chrome tab", payload)
	}
	pane := payload.Tabs[0].Panes[0]
	if pane.Role != "tab-network" || len(pane.Command) < 4 || pane.Command[1] != "role" || pane.Command[2] != "tab-network" {
		t.Fatalf("pane = %#v, want tab-network command", pane)
	}
	if !containsAdjacent(pane.Command, "--port", "9333") {
		t.Fatalf("command = %#v, want passthrough port", pane.Command)
	}
	if !containsValue(pane.Command, "--spawn-on-new-tab") {
		t.Fatalf("command = %#v, want spawn-on-new-tab", pane.Command)
	}
	if _, err := os.Stat(pane.Command[0]); err != nil {
		t.Fatalf("chrome command executable = %q is not stat-able: %v", pane.Command[0], err)
	}
}

func TestRunDispatchesCodeReviewHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"code-review", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 || !strings.Contains(stdout.String(), "Usage: zellij-agent code-review [options]") || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunDispatchesDebateBackgroundHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"debate-background", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"--output-format", "--agents", "--config"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDispatchesWorkDryRun(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := run([]string{"work", "--cwd", cwd, "--session", "work-command", "--dry-run", "implement work command"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_work-command" {
		t.Fatalf("envelope = %#v, want work execution plan", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "work-command" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 5 {
		t.Fatalf("payload = %#v, want work-command with five panes", payload)
	}
	coderCommand := payload.Tabs[0].Panes[0].Command
	if len(coderCommand) != 4 || coderCommand[1] != "role" || coderCommand[2] != "coding-agent" || coderCommand[3] != cwd {
		t.Fatalf("coder command = %#v, want executable role coding-agent cwd", coderCommand)
	}
	if _, err := os.Stat(coderCommand[0]); err != nil {
		t.Fatalf("coder command executable = %q is not stat-able: %v", coderCommand[0], err)
	}
}

func TestRunDispatchesPlannerHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"planner", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: agent-planner") {
		t.Fatalf("stdout = %q, want planner usage", stdout.String())
	}
}

func TestRunDispatchesCtlHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"ctl", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: agentctl") {
		t.Fatalf("stdout = %q, want ctl usage", stdout.String())
	}
}

func TestRunRejectsUnknownGroup(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unknown"}, strings.NewReader(""), &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command group: unknown") {
		t.Fatalf("stderr = %q, want unknown group", stderr.String())
	}
}

func containsAdjacent(values []string, key, value string) bool {
	for i := 0; i+1 < len(values); i++ {
		if values[i] == key && values[i+1] == value {
			return true
		}
	}
	return false
}

func containsValue(values []string, value string) bool {
	for _, got := range values {
		if got == value {
			return true
		}
	}
	return false
}
