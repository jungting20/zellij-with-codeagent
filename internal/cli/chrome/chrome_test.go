package chromecli

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

func TestRunDryRunPrintsChromeExecutionPlanEnvelope(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--cwd", cwd,
		"--session", "chrome-debug",
		"--zellij-session", "flag-session",
		"--dry-run",
		"--", "--port", "9333", "--no-launch",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
		Now:                fixedNow,
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit during dry-run", client.requestID)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_chrome-debug" {
		t.Fatalf("envelope = %#v, want execution_plan req_chrome-debug", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "chrome-debug" || payload.Layout != "single-tab" || payload.Tabs[0].Name != "chrome" {
		t.Fatalf("payload = %#v, want chrome-debug single chrome tab", payload)
	}
	if payload.ZellijSession != "flag-session" {
		t.Fatalf("payload.ZellijSession = %q, want flag-session", payload.ZellijSession)
	}
	gotCommand := payload.Tabs[0].Panes[0].Command
	wantCommand := []string{
		"/tmp/bin/zellij-agent", "role", "tab-network",
		"--socket", "/tmp/agentd.sock",
		"--session", "chrome-debug",
		"--role-bin", "/tmp/bin/zellij-agent",
		"--spawn-on-new-tab",
		"--port", "9333",
		"--no-launch",
	}
	if strings.Join(gotCommand, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Fatalf("command = %#v, want %#v", gotCommand, wantCommand)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty dry-run stderr", stderr.String())
	}
}

func TestRunDryRunUsesEnvironmentZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--cwd", t.TempDir(), "--dry-run"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("Unmarshal(envelope) error = %v", err)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v", err)
	}
	if payload.ZellijSession != "env-session" {
		t.Fatalf("payload.ZellijSession = %q, want env-session", payload.ZellijSession)
	}
}

func TestRunRejectsMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--cwd", t.TempDir(), "--dry-run"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{Now: fixedNow})
	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("stderr = %q, want resolver error", stderr.String())
	}
}

func TestRunSubmitsGeneratedPlan(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--socket", "/tmp/custom.sock",
		"--timeout", "5s",
		"--cwd", cwd,
		"--session", "chrome-debug",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
		Now:                fixedNow,
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.timeout != 5*time.Second || client.requestID != "req_chrome-debug" {
		t.Fatalf("client socket=%q timeout=%s request=%q, want custom socket timeout request", client.socketPath, client.timeout, client.requestID)
	}
	if client.payload.Session != "chrome-debug" || len(client.payload.Tabs) != 1 || len(client.payload.Tabs[0].Panes) != 1 {
		t.Fatalf("payload = %#v, want one chrome tab-network pane", client.payload)
	}
	firstLine, _, _ := strings.Cut(stdout.String(), "\n")
	if !strings.Contains(firstLine, "request=req_chrome-debug session=chrome-debug") ||
		!strings.Contains(firstLine, "layout=single-tab") ||
		!strings.Contains(firstLine, "tabs=1") ||
		!strings.Contains(firstLine, "panes=1") ||
		!strings.Contains(stdout.String(), "- chrome-tab-network-20260708-123456-123456789 role=tab-network") {
		t.Fatalf("stdout = %q, want chrome summary", stdout.String())
	}
}

func TestRunNoWatchDryRunPrintsTabNetworkPlan(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--cwd", cwd,
		"--session", "chrome-debug",
		"--dry-run",
		"--no-watch",
		"--", "--port", "9333", "--no-launch",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
		Now:                fixedNow,
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	pane := payload.Tabs[0].Panes[0]
	wantCommand := []string{"/tmp/bin/zellij-agent", "role", "tab-network", "--port", "9333", "--no-launch"}
	if strings.Join(pane.Command, "\x00") != strings.Join(wantCommand, "\x00") || pane.Role != "tab-network" {
		t.Fatalf("pane = %#v, want no-watch tab-network command", pane)
	}
}

func TestRunHelpPrintsUsageToStdout(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run(args, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

			if code != 0 {
				t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage: zellij-agent chrome") ||
				!strings.Contains(stdout.String(), "--zellij-session string") ||
				!strings.Contains(stdout.String(), "--socket") ||
				!strings.Contains(stdout.String(), "--dry-run") ||
				!strings.Contains(stdout.String(), "--no-watch") ||
				!strings.Contains(stdout.String(), "-- tab-network options") {
				t.Fatalf("stdout = %q, want chrome usage with common options", stdout.String())
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

func TestRunRejectsInvalidCWD(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")

	code := Run([]string{"--cwd", missing}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve cwd") {
		t.Fatalf("stderr = %q, want cwd error", stderr.String())
	}
}

func TestRunSubmitErrorIncludesDaemonHint(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "test-session")
	cwd := t.TempDir()
	client := &fakeAgentClient{submitErr: errors.New("dial unix /tmp/agentd.sock: connect: no such file")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", cwd}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{Now: fixedNow})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "chrome submit failed via socket") || !strings.Contains(stderr.String(), "zellij-agent daemon serve") {
		t.Fatalf("stderr = %q, want submit error and daemon hint", stderr.String())
	}
}

func TestResolveCWDReturnsCurrentDirectory(t *testing.T) {
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
	c.requestID = requestID
	c.payload = payload
	if c.submitErr != nil {
		return transport.ExecutionPlanResponse{}, c.submitErr
	}
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name: payload.Tabs[0].Name,
				Panes: []transport.Pane{
					{
						ID:     payload.Tabs[0].Panes[0].ID,
						Role:   payload.Tabs[0].Panes[0].Role,
						Status: "starting",
					},
				},
			},
		},
	}, nil
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 8, 12, 34, 56, 123456789, time.UTC)
}
