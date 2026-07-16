package plannercli

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

func TestRunTUIInvalidRequestPrecedesMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer

	code := RunWithInput([]string{"tui", "--goal", "inspect this page", "--dry-run"}, strings.NewReader(""), &stdout, &stderr, plannerTestFactory(&plannerTestClient{}))

	if code != 2 {
		t.Fatalf("RunWithInput() exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "request must include a URL") || strings.Contains(stderr.String(), "zellij session is required") {
		t.Fatalf("stderr = %q, want URL validation before session resolution", stderr.String())
	}
}

func TestRunPageRejectsMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"page", "--url", "http://localhost/test", "--mock-source", "README.md", "--dry-run"}, &stdout, &stderr, plannerTestFactory(&plannerTestClient{}))
	if code != 1 || !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("code=%d stderr=%q, want missing-session error", code, stderr.String())
	}
}

func TestRunTUIRejectsMissingZellijSessionAfterRequestValidation(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer
	code := RunWithInput([]string{"tui", "--goal", "inspect http://localhost/test", "--mock-source", "README.md", "--dry-run"}, strings.NewReader(""), &stdout, &stderr, plannerTestFactory(&plannerTestClient{}))
	if code != 1 || !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("code=%d stderr=%q, want missing-session error", code, stderr.String())
	}
}

func TestRunSubmitFileZellijSessionPrecedesEnvironment(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	path := writePlannerTestFile(t, `{"type":"execution_plan","request_id":"req_file","payload":{"session":"demo","zellij_session":"file-session","tabs":[{"panes":[{"id":"coder"}]}]}}`)
	client := &plannerTestClient{}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"submit", "--file", path}, &stdout, &stderr, plannerTestFactory(client))
	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.payload.ZellijSession != "file-session" {
		t.Fatalf("payload.ZellijSession = %q, want file-session", client.payload.ZellijSession)
	}
}

func TestRunSubmitRejectsMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	path := writePlannerTestFile(t, `{"type":"execution_plan","request_id":"req_file","payload":{"session":"demo","tabs":[{"panes":[{"id":"coder"}]}]}}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"submit", "--file", path}, &stdout, &stderr, plannerTestFactory(&plannerTestClient{}))
	if code != 1 || !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("code=%d stderr=%q, want missing-session error", code, stderr.String())
	}
}

type plannerTestClient struct {
	payload transport.ExecutionPlanPayload
}

func (c *plannerTestClient) Health(context.Context) (transport.HealthResponse, error) {
	return transport.HealthResponse{Status: "ok"}, nil
}

func (c *plannerTestClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.payload = payload
	return transport.ExecutionPlanResponse{RequestID: requestID, Session: payload.Session, Layout: payload.Layout}, nil
}

func plannerTestFactory(client *plannerTestClient) ClientFactory {
	return func(string, time.Duration) AgentClient { return client }
}

func writePlannerTestFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
