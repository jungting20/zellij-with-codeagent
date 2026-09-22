package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/followup"
)

type followupTransportCall struct {
	id       string
	update   *followup.Update
	deadline time.Time
}

type followupTransportSummary struct {
	count             int
	paused, attention bool
}

type fakeFollowupService struct {
	mu        sync.Mutex
	response  followup.Queue
	err       error
	calls     []followupTransportCall
	summaries map[string]followupTransportSummary
	listed    []string
}

func (s *fakeFollowupService) Get(ctx context.Context, id string) (followup.Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline, _ := ctx.Deadline()
	s.calls = append(s.calls, followupTransportCall{id: id, deadline: deadline})
	return s.response, s.err
}

func (s *fakeFollowupService) Update(ctx context.Context, id string, req followup.Update) (followup.Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline, _ := ctx.Deadline()
	s.calls = append(s.calls, followupTransportCall{id: id, update: &req, deadline: deadline})
	return s.response, s.err
}

func (s *fakeFollowupService) Summary(id string) (int, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listed = append(s.listed, id)
	value := s.summaries[id]
	return value.count, value.paused, value.attention
}

func newFollowupTestClient(t *testing.T, service FollowupService) *Client {
	t.Helper()
	server, err := NewServer(ServerOptions{
		Service: newFakeRuntimeService(), VoiceNotifications: noopVoiceNotificationService{},
		Followups: service, SocketPath: "unused.sock", RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	client := NewClient(ClientOptions{})
	client.baseURL, client.http = httpServer.URL, httpServer.Client()
	return client
}

func TestAgentFollowupsClientServerRoundTrip(t *testing.T) {
	stamp := time.Unix(125, 123).UTC()
	service := &fakeFollowupService{response: followup.Queue{
		AgentID: "agent/1", PaneID: "pane-1", OwnershipToken: "owner-1",
		Paused: true, Phase: followup.NeedsAttention, ActiveItemID: "item/1",
		Reason: "전송 확인 필요", UpdatedAt: stamp, AttemptedAt: stamp,
		BaselineRevision: 7, ObservationEpoch: 15,
		Items: []followup.Item{{
			ID: "item/1", RequestID: "request%1", Text: "테스트 추가\n결과도 알려줘",
			State: followup.NeedsAttention, Error: "interrupted", CreatedAt: stamp, SentAt: stamp,
		}},
	}}
	client := newFollowupTestClient(t, service)
	ids := []string{"agent/1", "agent%2F1", "작업 #1?"}
	for _, id := range ids {
		got, err := client.GetAgentFollowups(context.Background(), id)
		if err != nil || !reflect.DeepEqual(got.Queue, service.response) {
			t.Fatalf("GetAgentFollowups(%q) = %+v, %v", id, got, err)
		}
	}
	updates := []UpdateAgentFollowupsRequest{
		{Action: "add", Text: "새 지시\n두 번째 줄", RequestID: "request/2"},
		{Action: "edit", ItemID: "item/1", Text: "수정된 지시"},
		{Action: "cancel", ItemID: "item/1"},
		{Action: "resolve", ItemID: "item/1"},
		{Action: "pause"},
		{Action: "resume"},
	}
	for i, update := range updates {
		got, err := client.UpdateAgentFollowups(context.Background(), ids[i%len(ids)], update)
		if err != nil || !reflect.DeepEqual(got.Queue, service.response) {
			t.Fatalf("UpdateAgentFollowups(%+v) = %+v, %v", update, got, err)
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.calls) != len(ids)+len(updates) {
		t.Fatalf("calls = %+v", service.calls)
	}
	for i, call := range service.calls {
		if call.deadline.IsZero() || time.Until(call.deadline) > time.Second {
			t.Fatalf("call %d missing request deadline: %v", i, call.deadline)
		}
		if i < len(ids) {
			if call.id != ids[i] || call.update != nil {
				t.Fatalf("GET call %d = %+v", i, call)
			}
		} else {
			index := i - len(ids)
			if call.id != ids[index%len(ids)] || call.update == nil || *call.update != updates[index] {
				t.Fatalf("POST call %d = %+v; update %+v", index, call, call.update)
			}
		}
	}
}

func TestAgentFollowupsClientMapsServiceErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   ErrorCode
	}{
		{"invalid", followup.ErrInvalid, http.StatusBadRequest, CodeBadRequest},
		{"missing queue", followup.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{"missing agent", codingagent.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{"conflicting state", followup.ErrConflict, http.StatusConflict, CodeRuntimeError},
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, CodeTimeout},
		{"canceled", context.Canceled, statusClientClosedRequest, CodeStreamClosed},
		{"storage failure", errors.New("storage unavailable"), http.StatusInternalServerError, CodeRuntimeError},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeFollowupService{err: fmt.Errorf("follow-up operation: %w", test.err)}
			client := newFollowupTestClient(t, service)
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				var err error
				if method == http.MethodGet {
					_, err = client.GetAgentFollowups(context.Background(), "agent-1")
				} else {
					_, err = client.UpdateAgentFollowups(context.Background(), "agent-1", UpdateAgentFollowupsRequest{Action: "pause"})
				}
				var clientErr *ClientError
				if !errors.As(err, &clientErr) || clientErr.StatusCode != test.status ||
					clientErr.APIError.Code != test.code || !strings.Contains(clientErr.APIError.Message, test.err.Error()) {
					t.Fatalf("%s error = %#v, %v", method, clientErr, err)
				}
			}
		})
	}
}

func TestAgentFollowupsUnavailableAndRejectedRequests(t *testing.T) {
	client := newFollowupTestClient(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var err error
		if method == http.MethodGet {
			_, err = client.GetAgentFollowups(context.Background(), "agent-1")
		} else {
			_, err = client.UpdateAgentFollowups(context.Background(), "agent-1", UpdateAgentFollowupsRequest{Action: "pause"})
		}
		var clientErr *ClientError
		if !errors.As(err, &clientErr) || clientErr.StatusCode != http.StatusNotImplemented ||
			clientErr.APIError.Code != CodeRuntimeError || !strings.Contains(clientErr.APIError.Message, "restart") {
			t.Fatalf("unavailable %s error = %#v, %v", method, clientErr, err)
		}
	}
	service := &fakeFollowupService{}
	server := newTestServer(t, newFakeRuntimeService())
	server.followups = service
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, "/v1/agents/agent-1/followups", nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d body=%s", method, response.Code, response.Body.String())
		}
	}
	for _, body := range []string{"", "{", `{"action":"pause","unexpected":true}`, `{"action":"pause"}{"action":"resume"}`} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/agents/agent-1/followups", strings.NewReader(body)))
		var decoded ErrorResponse
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusBadRequest || decoded.Error.Code != CodeBadRequest {
			t.Fatalf("invalid body %q status=%d body=%+v", body, response.Code, decoded)
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.calls) != 0 {
		t.Fatalf("rejected requests called follow-up service: %+v", service.calls)
	}
}

type followupListingRuntime struct{ *fakeRuntimeService }

func (s *followupListingRuntime) ListAgents(context.Context) (codingagent.ListAgentsResponse, error) {
	return codingagent.ListAgentsResponse{Agents: []codingagent.AgentWithPane{
		fakeAgentResponse(codingagent.KindCodex, "queued").Agent,
		fakeAgentResponse(codingagent.KindClaude, "attention").Agent,
		fakeAgentResponse(codingagent.KindCodex, "empty").Agent,
	}}, nil
}

func TestAgentFollowupsSummariesEnrichAgentList(t *testing.T) {
	service := &fakeFollowupService{summaries: map[string]followupTransportSummary{
		"queued": {count: 3}, "attention": {count: 2, paused: true, attention: true},
	}}
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			server := newTestServer(t, newFakeRuntimeService())
			server.service = &followupListingRuntime{newFakeRuntimeService()}
			if enabled {
				server.followups = service
			}
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()
			client := NewClient(ClientOptions{})
			client.baseURL, client.http = httpServer.URL, httpServer.Client()
			got, err := client.ListAgents(context.Background())
			if err != nil || len(got.Agents) != 3 {
				t.Fatalf("ListAgents = %+v, %v", got, err)
			}
			for _, row := range got.Agents {
				want := followupTransportSummary{}
				if enabled {
					want = service.summaries[row.Agent.ID]
				}
				if row.Agent.FollowupCount != want.count || row.Agent.FollowupPaused != want.paused ||
					row.Agent.FollowupAttention != want.attention || row.Agent.PaneID != row.Pane.ID {
					t.Fatalf("agent summary = %+v; want %+v", row, want)
				}
			}
		})
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if !reflect.DeepEqual(service.listed, []string{"queued", "attention", "empty"}) || len(service.calls) != 0 {
		t.Fatalf("summary queries=%v queue operations=%+v", service.listed, service.calls)
	}
}
