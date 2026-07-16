package ctlcli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunDebateInvalidArgumentsPrecedeMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "topic", args: []string{"debate"}, want: "debate requires --topic"},
		{name: "rounds", args: []string{"debate", "--topic", "valid", "--rounds", "0"}, want: "debate requires --rounds between 1 and 3"},
		{name: "agent timeout", args: []string{"debate", "--topic", "valid", "--agent-timeout", "0s"}, want: "debate requires --agent-timeout greater than 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, strings.NewReader(""), &stdout, &stderr, ctlTestFactory(&ctlTestClient{}))
			if code != 2 {
				t.Fatalf("Run() exit code = %d, want 2; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) || strings.Contains(stderr.String(), "zellij session is required") {
				t.Fatalf("stderr = %q, want %q before session resolution", stderr.String(), tt.want)
			}
		})
	}
}

func TestRunPlanAcceptsStrictRawPayloadAndEnrichesSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	client := &ctlTestClient{}
	var stdout, stderr bytes.Buffer
	input := `{"session":"demo","tabs":[{"panes":[{"id":"coder"}]}]}`
	code := Run([]string{"plan", "--file", "-", "--request-id", "req_raw"}, strings.NewReader(input), &stdout, &stderr, ctlTestFactory(client))
	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "req_raw" || client.payload.ZellijSession != "env-session" {
		t.Fatalf("request=%q payload=%#v, want raw enriched plan", client.requestID, client.payload)
	}
}

func TestRunPlanRejectsUnknownRawPayloadField(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	var stdout, stderr bytes.Buffer
	input := `{"session":"demo","unknown":true,"tabs":[{"panes":[{"id":"coder"}]}]}`
	code := Run([]string{"plan", "--file", "-"}, strings.NewReader(input), &stdout, &stderr, ctlTestFactory(&ctlTestClient{}))
	if code != 1 || !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("code=%d stderr=%q, want strict unknown-field error", code, stderr.String())
	}
}

func TestRunPlanFileZellijSessionPrecedesEnvironment(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "env-session")
	client := &ctlTestClient{}
	var stdout, stderr bytes.Buffer
	input := `{"type":"execution_plan","request_id":"req_file","payload":{"session":"demo","zellij_session":"file-session","tabs":[{"panes":[{"id":"coder"}]}]}}`
	code := Run([]string{"plan", "--file", "-"}, strings.NewReader(input), &stdout, &stderr, ctlTestFactory(client))
	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.payload.ZellijSession != "file-session" {
		t.Fatalf("payload.ZellijSession = %q, want file-session", client.payload.ZellijSession)
	}
}

func TestRunPlanRejectsMissingZellijSession(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer
	input := `{"session":"demo","tabs":[{"panes":[{"id":"coder"}]}]}`
	code := Run([]string{"plan", "--file", "-"}, strings.NewReader(input), &stdout, &stderr, ctlTestFactory(&ctlTestClient{}))
	if code != 1 || !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("code=%d stderr=%q, want missing-session error", code, stderr.String())
	}
}

func TestRunDebateRejectsMissingZellijSessionAfterValidation(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"debate", "--topic", "valid topic"}, strings.NewReader(""), &stdout, &stderr, ctlTestFactory(&ctlTestClient{}))
	if code != 1 || !strings.Contains(stderr.String(), "resolve zellij session: zellij session is required") {
		t.Fatalf("code=%d stderr=%q, want missing-session error", code, stderr.String())
	}
}

type ctlTestClient struct {
	requestID string
	payload   transport.ExecutionPlanPayload
}

func (*ctlTestClient) Health(context.Context) (transport.HealthResponse, error) {
	return transport.HealthResponse{}, nil
}
func (*ctlTestClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	return transport.InspectRuntimeResponse{}, nil
}
func (*ctlTestClient) SendInput(context.Context, string, transport.SendInputRequest) error {
	return nil
}
func (*ctlTestClient) SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	return transport.SnapshotOutputResponse{}, nil
}
func (*ctlTestClient) SendMessage(context.Context, transport.SendMessageRequest) (transport.SendMessageResponse, error) {
	return transport.SendMessageResponse{}, nil
}
func (c *ctlTestClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.requestID, c.payload = requestID, payload
	return transport.ExecutionPlanResponse{RequestID: requestID, Session: payload.Session}, nil
}
func (*ctlTestClient) RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error) {
	return transport.RecentEventsResponse{}, nil
}
func (*ctlTestClient) StreamEvents(context.Context) (*transport.EventStream, error) { return nil, nil }
func (*ctlTestClient) Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error) {
	return transport.CleanupResponse{}, nil
}

func ctlTestFactory(client *ctlTestClient) ClientFactory {
	return func(string, time.Duration) AgentClient { return client }
}
