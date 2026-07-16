package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"zellij-with-codeagent/internal/transport"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage: zellij-agent", "planner", "work", "chrome", "dashboard", "ticket-worker", "code-review", "debate-background"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDispatchesTicketWorkerHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"ticket-worker", "--help"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Usage: zellij-agent ticket-worker", "init", "start"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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
