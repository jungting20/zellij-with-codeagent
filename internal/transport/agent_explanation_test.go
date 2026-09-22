package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
)

type explanationRuntime struct {
	*fakeRuntimeService
	response codingagent.AgentExplanation
	err      error
	ids      []codingagent.ID
	deadline time.Time
}

func (s *explanationRuntime) ExplainAgent(ctx context.Context, id codingagent.ID) (codingagent.AgentExplanation, error) {
	s.ids = append(s.ids, id)
	s.deadline, _ = ctx.Deadline()
	return s.response, s.err
}

func TestExplainAgentClientServerRoundTrip(t *testing.T) {
	service := &explanationRuntime{
		fakeRuntimeService: newFakeRuntimeService(),
		response: codingagent.AgentExplanation{
			AgentID: "agent/1", Kind: codingagent.KindCodex, PaneID: "pane-1", State: codingagent.StateWorking,
			StateReason: "matched_manifest_rule", MatchedRule: "working", ObservationAvailable: true,
			StateChangedAt: time.Unix(20, 0).UTC(), ObservedAt: time.Unix(30, 0).UTC(),
			PendingIdle: true, IdleConfirmations: 2,
			Detection: &codingagent.DetectionExplanation{
				Detection: codingagent.Detection{State: codingagent.StateIdle, Fallback: true},
				Rules:     []codingagent.RuleEvaluation{{ID: "working", Priority: 10, Region: codingagent.Region{Type: codingagent.RegionOSCTitle}, Evidence: "title\x1b[0m"}},
			},
		},
	}
	server, err := NewServer(ServerOptions{Service: service, VoiceNotifications: noopVoiceNotificationService{}, SocketPath: "unused.sock", RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var requests []string
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		server.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	client := NewClient(ClientOptions{})
	client.baseURL, client.http = httpServer.URL, httpServer.Client()
	for _, id := range []string{"agent/1", "agent%2F1"} {
		got, err := client.ExplainAgent(context.Background(), id)
		if err != nil || !reflect.DeepEqual(got, service.response) {
			t.Fatalf("ExplainAgent(%q) = %#v, %v; want %#v", id, got, err, service.response)
		}
	}
	if !reflect.DeepEqual(requests, []string{"GET /v1/agents/agent%2F1/explain", "GET /v1/agents/agent%252F1/explain"}) {
		t.Fatalf("requests=%v", requests)
	}
	if !reflect.DeepEqual(service.ids, []codingagent.ID{"agent/1", "agent%2F1"}) {
		t.Fatalf("ids=%v", service.ids)
	}
	if service.deadline.IsZero() || time.Until(service.deadline) > time.Second {
		t.Fatalf("request deadline=%s", service.deadline)
	}

	service.err = codingagent.ErrNotFound
	_, err = client.ExplainAgent(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("missing agent error=%v", err)
	}
}

func TestExplainAgentRouteRejectsMutationAndReportsUnavailable(t *testing.T) {
	service := &explanationRuntime{fakeRuntimeService: newFakeRuntimeService()}
	server := newTestServer(t, service.fakeRuntimeService)
	server.service = service
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/agents/agent-1/explain", nil))
	if response.Code != http.StatusMethodNotAllowed || len(service.ids) != 0 {
		t.Fatalf("POST status=%d calls=%v", response.Code, service.ids)
	}

	server.service = service.fakeRuntimeService
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agents/agent-1/explain", nil))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported explanation status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExplainAgentRouteMapsErrors(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   ErrorCode
	}{
		{codingagent.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, CodeTimeout},
		{errors.New("detector unavailable"), http.StatusInternalServerError, CodeRuntimeError},
	} {
		t.Run(string(tt.code), func(t *testing.T) {
			service := &explanationRuntime{fakeRuntimeService: newFakeRuntimeService(), err: tt.err}
			server := newTestServer(t, service.fakeRuntimeService)
			server.service = service
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agents/agent-1/explain", nil))
			var body ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tt.status || body.Error.Code != tt.code {
				t.Fatalf("status=%d body=%#v", response.Code, body)
			}
		})
	}
}
