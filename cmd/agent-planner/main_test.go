package main

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

func TestRunPageDryRunPrintsCanonicalEnvelope(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"page",
		"--url", "http://localhost:8000/example/aa",
		"--cwd", "/tmp/app",
		"--mock-source", "/tmp/app/src/pages/example/aa.tsx",
		"--agent-role-bin", "/tmp/runtime/bin/agent-role",
		"--dry-run",
	}, &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit during dry-run", client.requestID)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_page-example-aa" {
		t.Fatalf("envelope = %#v, want execution_plan req_page-example-aa", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "page-example-aa" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want page-example-aa with four panes", payload)
	}
	if strings.Contains(stdout.String(), "resolved_source") {
		t.Fatalf("dry-run output contains legacy resolved_source: %s", stdout.String())
	}
}

func TestRunPageSubmitsGeneratedPlan(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"page",
		"--socket", "/tmp/custom.sock",
		"--url", "http://localhost:8000/example/aa",
		"--cwd", "/tmp/app",
		"--mock-source", "/tmp/app/src/pages/example/aa.tsx",
		"--agent-role-bin", "/tmp/runtime/bin/agent-role",
		"--request-id", "req_custom",
		"--ui",
	}, &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.requestID != "req_custom" {
		t.Fatalf("client = socket %q request %q, want custom socket/request", client.socketPath, client.requestID)
	}
	if client.payload.Session != "page-example-aa" || len(client.payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want page-example-aa with four panes", client.payload)
	}
	if !strings.Contains(stderr.String(), "[AI PLANNER]") || !strings.Contains(stderr.String(), "source=/tmp/app/src/pages/example/aa.tsx") {
		t.Fatalf("stderr = %q, want planner UI", stderr.String())
	}
	if !strings.Contains(stdout.String(), "request=req_custom session=page-example-aa") {
		t.Fatalf("stdout = %q, want submit summary", stdout.String())
	}
}

func TestRunPageUsesDefaultMockSource(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"page", "--url", "http://localhost:8000/example/aa", "--dry-run"}, &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "README.md") {
		t.Fatalf("stdout = %q, want default README.md mock source", stdout.String())
	}
}

func TestDefaultAgentRoleBinUsesPathLookup(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "agent-role")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", dir)

	if got := defaultAgentRoleBin(); got != binPath {
		t.Fatalf("defaultAgentRoleBin() = %q, want PATH match %q", got, binPath)
	}
}

func TestRunValidateAcceptsAIEnvelopeFile(t *testing.T) {
	planPath := writePlanFile(t, `{
		"type": "execution_plan",
		"request_id": "req_ai_json",
		"payload": {
			"session": "page-example-aa",
			"layout": "triple-horizontal",
			"tabs": [
				{"name": "page-example-aa", "panes": [{"id": "page-editor", "role": "editor"}]}
			]
		}
	}`)
	var stdout, stderr bytes.Buffer

	code := run([]string{"validate", "--file", planPath}, &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "valid request=req_ai_json session=page-example-aa") {
		t.Fatalf("stdout = %q, want validation summary", stdout.String())
	}
}

func TestRunValidateRejectsLegacyPayloadFields(t *testing.T) {
	planPath := writePlanFile(t, `{
		"type": "execution_plan",
		"request_id": "req_legacy",
		"payload": {
			"url": "http://localhost:8000/example/aa",
			"resolved_source": "/tmp/app/src/pages/example/aa.tsx",
			"session": "page-example-aa",
			"tabs": [
				{"name": "page-example-aa", "panes": [{"id": "page-editor", "role": "editor"}]}
			]
		}
	}`)
	var stdout, stderr bytes.Buffer

	code := run([]string{"validate", "--file", planPath}, &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 1 {
		t.Fatalf("run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr = %q, want strict unknown field validation", stderr.String())
	}
}

func TestRunSubmitValidatesAndSubmitsAIEnvelopeFile(t *testing.T) {
	planPath := writePlanFile(t, `{
		"type": "execution_plan",
		"request_id": "req_ai_submit",
		"payload": {
			"session": "page-example-aa",
			"layout": "triple-horizontal",
			"tabs": [
				{"name": "page-example-aa", "panes": [{"id": "page-editor", "role": "editor"}]}
			]
		}
	}`)
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"submit", "--socket", "/tmp/custom.sock", "--file", planPath, "--ui"}, &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.requestID != "req_ai_submit" {
		t.Fatalf("client = socket %q request %q, want custom socket and req_ai_submit", client.socketPath, client.requestID)
	}
	if client.payload.Session != "page-example-aa" {
		t.Fatalf("payload = %#v, want page-example-aa", client.payload)
	}
	if !strings.Contains(stderr.String(), "[AI PLANNER]") || !strings.Contains(stdout.String(), "request=req_ai_submit") {
		t.Fatalf("stdout=%q stderr=%q, want UI and submit summary", stdout.String(), stderr.String())
	}
}

func TestRunSubmitRequiresFile(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"submit"}, &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 1 {
		t.Fatalf("run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--file is required") {
		t.Fatalf("stderr = %q, want missing file error", stderr.String())
	}
}

func TestRunTUIDryRunPrintsEnvelopeFromNaturalLanguageRequest(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := runWithInput([]string{
		"tui",
		"--goal", "localhost:8000/example/aa page source를 열고 네트워크와 콘솔을 확인해줘",
		"--agent-role-bin", "/tmp/runtime/bin/agent-role",
		"--dry-run",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("runWithInput() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit during dry-run", client.requestID)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_page-example-aa" {
		t.Fatalf("envelope = %#v, want execution_plan req_page-example-aa", envelope)
	}
	if !strings.Contains(stderr.String(), "[AI PLANNER]") || !strings.Contains(stderr.String(), "agentd=ok(test)") || !strings.Contains(stderr.String(), "request_text=localhost:8000/example/aa") || !strings.Contains(stderr.String(), "mock_source=") {
		t.Fatalf("stderr = %q, want TUI natural language summary", stderr.String())
	}
}

func TestRunTUISubmitsAfterConfirmation(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer
	input := strings.Join([]string{
		"http://localhost:8000/example/aa page source를 열어줘",
		"y",
		"",
	}, "\n")

	code := runWithInput([]string{
		"tui",
		"--socket", "/tmp/custom.sock",
		"--agent-role-bin", "/tmp/runtime/bin/agent-role",
		"--request-id", "req_tui",
	}, strings.NewReader(input), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("runWithInput() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.requestID != "req_tui" {
		t.Fatalf("client = socket %q request %q, want custom socket and req_tui", client.socketPath, client.requestID)
	}
	if client.payload.Session != "page-example-aa" || len(client.payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want page-example-aa with four panes", client.payload)
	}
	if !strings.Contains(stdout.String(), "request=req_tui session=page-example-aa") {
		t.Fatalf("stdout = %q, want submit summary", stdout.String())
	}
}

func TestRunTUICancelPrintsEnvelopeWithoutSubmitting(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer
	input := strings.Join([]string{
		"localhost:8000/example/aa page source를 열어줘",
		"n",
		"",
	}, "\n")

	code := runWithInput([]string{
		"tui",
		"--agent-role-bin", "/tmp/runtime/bin/agent-role",
	}, strings.NewReader(input), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("runWithInput() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit after cancel", client.requestID)
	}
	if !strings.Contains(stderr.String(), "submit cancelled") {
		t.Fatalf("stderr = %q, want cancellation message", stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("cancel output JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.RequestID != "req_page-example-aa" {
		t.Fatalf("envelope request = %q, want default request id", envelope.RequestID)
	}
}

func TestRunTUIRequiresURLInChatMessage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runWithInput([]string{"tui", "--dry-run"}, strings.NewReader("페이지 열어줘\n"), &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 2 {
		t.Fatalf("runWithInput() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "request must include a URL") {
		t.Fatalf("stderr = %q, want missing URL error", stderr.String())
	}
}

func TestRunTUIShowsUnreachableAgentd(t *testing.T) {
	client := &fakeAgentClient{healthErr: errors.New("dial failed")}
	var stdout, stderr bytes.Buffer

	code := runWithInput([]string{
		"tui",
		"--goal", "localhost:8000/example/aa 페이지 열어줘",
		"--dry-run",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("runWithInput() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "agentd=unreachable") {
		t.Fatalf("stderr = %q, want unreachable health status", stderr.String())
	}
}

func TestExtractURL(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{text: "localhost:8000/example/aa 페이지 열어줘", want: "http://localhost:8000/example/aa"},
		{text: "http://localhost:8000/example/aa 확인", want: "http://localhost:8000/example/aa"},
		{text: "https://example.test/path).", want: "https://example.test/path"},
	}
	for _, tt := range tests {
		got, ok := extractURL(tt.text)
		if !ok || got != tt.want {
			t.Fatalf("extractURL(%q) = %q, %v; want %q, true", tt.text, got, ok, tt.want)
		}
	}
}

func fakeFactory(client *fakeAgentClient) clientFactory {
	return func(socketPath string, timeout time.Duration) agentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

func writePlanFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration
	requestID  string
	payload    transport.ExecutionPlanPayload
	healthErr  error
}

func (c *fakeAgentClient) Health(context.Context) (transport.HealthResponse, error) {
	if c.healthErr != nil {
		return transport.HealthResponse{}, c.healthErr
	}
	return transport.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
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
					{ID: "page-editor", Role: "editor", Status: "running", ZellijPaneID: "terminal_1"},
					{ID: "page-lsp", Role: "lsp", Status: "running", ZellijPaneID: "terminal_2"},
					{ID: "page-network", Role: "network-tracker", Status: "running", ZellijPaneID: "terminal_3"},
					{ID: "page-console", Role: "console-tracker", Status: "running", ZellijPaneID: "terminal_4"},
				},
			},
		},
	}, nil
}
