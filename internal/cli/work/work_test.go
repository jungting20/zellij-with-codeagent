package workcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunDryRunPrintsExecutionPlanEnvelope(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--cwd", cwd,
		"--session", "work-command",
		"--dry-run",
		"implement", "work", "command",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit during dry-run", client.requestID)
	}
	if got := bytes.Count(stdout.Bytes(), []byte(`"initial_input"`)); got != 1 {
		t.Fatalf("dry-run initial_input keys = %d, want coder-only JSON field", got)
	}
	if got := bytes.Count(stdout.Bytes(), []byte(`"initial_input_ready_text"`)); got != 1 {
		t.Fatalf("dry-run initial_input_ready_text keys = %d, want coder-only JSON field", got)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_work-command" {
		t.Fatalf("envelope = %#v, want execution_plan req_work-command", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "work-command" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 5 {
		t.Fatalf("payload = %#v, want work-command with five panes", payload)
	}
	if got := payload.Tabs[0].Panes[0].Command; len(got) != 4 || got[0] != "/tmp/bin/zellij-agent" || got[1] != "role" || got[2] != "coding-agent" || got[3] != cwd {
		t.Fatalf("coder command = %#v, want configured role command", got)
	}
	if got := payload.Tabs[0].Panes[0].InitialInput; got != "implement work command" {
		t.Fatalf("coder InitialInput = %q, want dry-run goal", got)
	}
	if got := payload.Tabs[0].Panes[0].InitialInputReadyText; got != "›" {
		t.Fatalf("coder InitialInputReadyText = %q, want Codex prompt marker", got)
	}
	for _, pane := range payload.Tabs[0].Panes[1:] {
		if pane.InitialInput != "" {
			t.Fatalf("pane %q InitialInput = %q, want coder-only prefill", pane.ID, pane.InitialInput)
		}
		if pane.InitialInputReadyText != "" {
			t.Fatalf("pane %q InitialInputReadyText = %q, want coder-only readiness", pane.ID, pane.InitialInputReadyText)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty dry-run stderr", stderr.String())
	}
}

func TestRunSubmitsGeneratedPlan(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--socket", "/tmp/custom.sock",
		"--timeout", "5s",
		"--cwd", cwd,
		"--session", "work-command",
		"--auto-test",
		"implement work command",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.timeout != 5*time.Second || client.requestID != "req_work-command" {
		t.Fatalf("client socket=%q timeout=%s request=%q, want custom socket timeout request", client.socketPath, client.timeout, client.requestID)
	}
	if client.payload.Session != "work-command" || len(client.payload.Tabs[0].Panes) != 5 {
		t.Fatalf("payload = %#v, want submitted work-command plan", client.payload)
	}
	if !strings.Contains(client.payload.Tabs[0].Panes[1].Command[2], "go test finished with exit=%s") {
		t.Fatalf("test command = %q, want auto-test marker", client.payload.Tabs[0].Panes[1].Command[2])
	}
	firstLine, _, _ := strings.Cut(stdout.String(), "\n")
	if !strings.Contains(firstLine, "request=req_work-command session=work-command") ||
		!strings.Contains(firstLine, "layout=triple-horizontal") ||
		!strings.Contains(firstLine, "tabs=1") ||
		!strings.Contains(firstLine, "panes=5") ||
		!strings.Contains(stdout.String(), "- coder role=coding-agent") {
		t.Fatalf("stdout = %q, want work summary", stdout.String())
	}
}

func TestRunHelpPrintsUsageToStdout(t *testing.T) {
	tests := [][]string{
		{"--help"},
		{"-h"},
		{"help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run(args, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

			if code != 0 {
				t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage: zellij-agent work") ||
				!strings.Contains(stdout.String(), "--socket") ||
				!strings.Contains(stdout.String(), "--timeout") ||
				!strings.Contains(stdout.String(), "default 15s") {
				t.Fatalf("stdout = %q, want work usage with common options", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty help stderr", stderr.String())
			}
		})
	}
}

func TestRunParseErrorReturnsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--bogus"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want parse error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty parse-error stdout", stdout.String())
	}
}

func TestRunRejectsEmptyGoal(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", t.TempDir()}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "work requires a goal") {
		t.Fatalf("stderr = %q, want missing goal message", stderr.String())
	}
}

func TestRunRejectsInvalidCWD(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")

	code := Run([]string{"--cwd", missing, "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve cwd") {
		t.Fatalf("stderr = %q, want cwd error", stderr.String())
	}
}

func TestRunSubmitErrorIncludesDaemonHint(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{submitErr: errors.New("dial unix /tmp/agentd.sock: connect: no such file")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", cwd, "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "work submit failed via socket") || !strings.Contains(stderr.String(), "zellij-agent daemon serve") {
		t.Fatalf("stderr = %q, want submit error and daemon hint", stderr.String())
	}
}

func TestResolveCWDReturnsAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(old)
	})

	got, err := resolveCWD("")
	if err != nil {
		t.Fatalf("resolveCWD() error = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveCWD() = %q, want %q", got, dir)
	}
}

func fakeFactory(client *fakeAgentClient) ClientFactory {
	return func(socketPath string, timeout time.Duration) AgentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration
	requestID  string
	payload    transport.ExecutionPlanPayload
	submitErr  error
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	if c.submitErr != nil {
		return transport.ExecutionPlanResponse{}, c.submitErr
	}
	c.requestID = requestID
	c.payload = payload
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name: payload.Tabs[0].Name,
				Panes: []transport.Pane{
					{ID: "coder", Role: "coding-agent", Status: "running", ZellijPaneID: "terminal_1"},
					{ID: "test", Role: "test-runner", Status: "running", ZellijPaneID: "terminal_2"},
					{ID: "review", Role: "review-assistant", Status: "running", ZellijPaneID: "terminal_3"},
					{ID: "lazygit", Role: "lazygit", Status: "running", ZellijPaneID: "terminal_4"},
					{ID: "notes", Role: "notes", Status: "running", ZellijPaneID: "terminal_5"},
				},
			},
		},
	}, nil
}
