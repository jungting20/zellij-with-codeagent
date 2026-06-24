package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
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
	for _, want := range []string{"Usage: zellij-agent", "planner", "work", "debate-background"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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
	if payload.Session != "work-command" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want work-command with four panes", payload)
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
	requests []debate.BackgroundCommandRequest
}

func (r *zellijAgentBackgroundRunner) Run(_ context.Context, req debate.BackgroundCommandRequest) (debate.BackgroundCommandResult, error) {
	r.requests = append(r.requests, req)
	if req.AgentID == "debate-coordinator" {
		return debate.BackgroundCommandResult{Stdout: "background synthesis"}, nil
	}
	return debate.BackgroundCommandResult{Stdout: "background answer from " + req.AgentID}, nil
}

func (r *zellijAgentBackgroundRunner) requestsFor(agentID string) []debate.BackgroundCommandRequest {
	var requests []debate.BackgroundCommandRequest
	for _, req := range r.requests {
		if req.AgentID == agentID {
			requests = append(requests, req)
		}
	}
	return requests
}
