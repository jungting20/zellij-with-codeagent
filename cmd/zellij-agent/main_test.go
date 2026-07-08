package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	debatebg "zellij-with-codeagent/internal/cli/debatebackground"
	"zellij-with-codeagent/internal/debate"
	"zellij-with-codeagent/internal/transport"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage: zellij-agent", "planner", "work", "chrome", "code-review", "debate-background"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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
	if pane.Role != "tab-network" || len(pane.Command) != 5 || pane.Command[1] != "role" || pane.Command[2] != "tab-network" || pane.Command[3] != "--port" || pane.Command[4] != "9333" {
		t.Fatalf("pane = %#v, want tab-network command with passthrough port", pane)
	}
	if _, err := os.Stat(pane.Command[0]); err != nil {
		t.Fatalf("chrome command executable = %q is not stat-able: %v", pane.Command[0], err)
	}
}

func TestRunDispatchesCodeReview(t *testing.T) {
	runner := &zellijAgentBackgroundRunner{}
	restoreRunner := debatebg.SetBackgroundRunnerForTesting(runner)
	defer restoreRunner()
	starter := &zellijAgentCodexStarter{}
	restoreStarter := debatebg.SetCodexStarterForTesting(starter)
	defer restoreStarter()
	var stdout, stderr bytes.Buffer

	code := run([]string{"code-review", "--rounds", "1"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(runner.requestsFor("agy")) != 1 || len(runner.requestsFor("agent")) != 1 || len(runner.requestsFor("codex")) != 1 {
		t.Fatalf("runner requests = %#v, want default review agents", runner.requests)
	}
	if len(starter.requests) != 1 {
		t.Fatalf("codex start requests = %#v, want one", starter.requests)
	}
	if !strings.Contains(runner.requestsFor("codex")[0].Stdin, "Review the latest git diff.") {
		t.Fatalf("codex prompt = %q, want review topic", runner.requestsFor("codex")[0].Stdin)
	}
}

func TestRunDispatchesDebateBackground(t *testing.T) {
	runner := &zellijAgentBackgroundRunner{}
	restore := debatebg.SetBackgroundRunnerForTesting(runner)
	defer restore()
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate-background",
		"--topic", "top-level background",
		"--agents", "agy,codex",
		"--cwd", "/repo",
		"--agent-timeout", "1s",
		"--timeout", "5s",
	}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(runner.requestsFor("agy")) != 1 || len(runner.requestsFor("codex")) != 1 || len(runner.requestsFor("debate-coordinator")) != 1 {
		t.Fatalf("runner requests = %#v, want agy, codex, coordinator", runner.requests)
	}
	if !strings.Contains(stdout.String(), "debate request=") || !strings.Contains(stdout.String(), "background synthesis") {
		t.Fatalf("stdout = %q, want debate-background output", stdout.String())
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

type zellijAgentBackgroundRunner struct {
	mu       sync.Mutex
	requests []debate.BackgroundCommandRequest
}

type zellijAgentCodexStarter struct {
	requests []debatebg.CodexStartRequest
}

func (r *zellijAgentBackgroundRunner) Run(_ context.Context, req debate.BackgroundCommandRequest) (debate.BackgroundCommandResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	if req.AgentID == "debate-coordinator" {
		return debate.BackgroundCommandResult{Stdout: "background synthesis"}, nil
	}
	return debate.BackgroundCommandResult{Stdout: "background answer from " + req.AgentID}, nil
}

func (r *zellijAgentBackgroundRunner) requestsFor(agentID string) []debate.BackgroundCommandRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var requests []debate.BackgroundCommandRequest
	for _, req := range r.requests {
		if req.AgentID == agentID {
			requests = append(requests, req)
		}
	}
	return requests
}

func (s *zellijAgentCodexStarter) Start(_ context.Context, req debatebg.CodexStartRequest) error {
	s.requests = append(s.requests, req)
	return nil
}
