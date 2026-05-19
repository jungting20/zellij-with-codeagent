package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

func TestServerCreatePane(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)

	body := strings.NewReader(`{"id":"pane-1","task_id":"task-1","agent_id":"agent-1","role":"test","new_tab":true,"tab_name":"agentd-test","command":["go","test"],"cwd":"."}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes", body)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if service.createReq.ID != "pane-1" || service.createReq.TaskID != "task-1" || !service.createReq.NewTab {
		t.Fatalf("CreatePane request = %#v, want decoded logical request", service.createReq)
	}
	var decoded CreatePaneResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Pane.ID != "pane-1" || decoded.Pane.ZellijPaneID != "terminal_1" {
		t.Fatalf("response pane = %#v, want logical and zellij ids", decoded.Pane)
	}
}

func TestServerSendInput(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes/pane-1/input", strings.NewReader(`{"text":"go test\n"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.sendReq.PaneID != "pane-1" || service.sendReq.Text != "go test\n" {
		t.Fatalf("SendInput request = %#v, want pane-1 text", service.sendReq)
	}
}

func TestServerInvalidJSONDoesNotCallRuntime(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes", strings.NewReader(`{`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if service.createCalled {
		t.Fatal("CreatePane was called for invalid JSON")
	}
}

func TestServerMapsRuntimeNotFound(t *testing.T) {
	service := newFakeRuntimeService()
	service.sendErr = rt.ErrPaneNotFound
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes/missing/input", strings.NewReader(`{"text":"noop"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	var decoded ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if decoded.Error.Code != CodeNotFound {
		t.Fatalf("error = %#v, want not_found", decoded.Error)
	}
}

func TestServerInspectRuntime(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/v1/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var decoded InspectRuntimeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Message != "1 managed pane(s)" || decoded.Counts.Managed != 1 {
		t.Fatalf("runtime response = %#v, want fake status", decoded)
	}
}

func TestServerStreamsEvents(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	resp, err := http.Get(httpServer.URL + "/v1/events/stream")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	service.publish(eventbus.Event{Type: eventbus.TypeServerReady, PaneID: "server", Message: "ready", Time: time.Unix(1, 0)})

	decoder := json.NewDecoder(resp.Body)
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("decode streamed event: %v", err)
	}
	if event.Type != string(eventbus.TypeServerReady) || event.PaneID != "server" {
		t.Fatalf("event = %#v, want server_ready for server", event)
	}
}

func TestServerRecentEventsFilter(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/v1/events/recent?limit=1&type=test_passed", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if service.recentReq.Limit != 1 || len(service.recentReq.Types) != 1 || service.recentReq.Types[0] != eventbus.TypeTestPassed {
		t.Fatalf("RecentEvents request = %#v, want limit/type filter", service.recentReq)
	}
}

func TestServerCleanupPartialReturnsDetails(t *testing.T) {
	service := newFakeRuntimeService()
	service.cleanupErr = errors.Join(rt.ErrCleanupPartial, errors.New("1 pane failed"))
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/cleanup", strings.NewReader(`{"task_id":"task-1"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var decoded CleanupResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Failed) != 1 || decoded.Failed[0].Pane.ID != "pane-failed" {
		t.Fatalf("cleanup response = %#v, want failed pane details", decoded)
	}
}

func TestPrepareSocketRefusesActiveSocket(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := netListenUnix(path)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer listener.Close()

	if err := prepareSocket(path); err == nil {
		t.Fatal("prepareSocket() error = nil, want active socket error")
	}
}

func newTestServer(t *testing.T, service *fakeRuntimeService) *Server {
	t.Helper()
	server, err := NewServer(ServerOptions{
		Service:        service,
		SocketPath:     "unused.sock",
		RequestTimeout: time.Second,
		Version:        "test",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

type fakeRuntimeService struct {
	mu sync.Mutex

	createCalled     bool
	createReq        rt.CreatePaneRequest
	applyPlanCalled  bool
	applyPlanReq     rt.ApplyExecutionPlanRequest
	sendReq          rt.SendInputRequest
	sendErr          error
	recentReq        rt.RecentEventsRequest
	cleanupErr       error

	subs []chan eventbus.Event
}

func newFakeRuntimeService() *fakeRuntimeService {
	return &fakeRuntimeService{}
}

func (f *fakeRuntimeService) CreatePane(_ context.Context, req rt.CreatePaneRequest) (rt.CreatePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalled = true
	f.createReq = req
	tabID := rt.ZellijTabID(7)
	return rt.CreatePaneResponse{Pane: rt.Pane{
		ID:           req.ID,
		TaskID:       req.TaskID,
		AgentID:      req.AgentID,
		ZellijPaneID: "terminal_1",
		ZellijTabID:  &tabID,
		TabName:      req.TabName,
		Role:         req.Role,
		Command:      req.Command,
		CWD:          req.CWD,
		Status:       rt.PaneStatusStarting,
		CreatedAt:    time.Unix(1, 0),
		UpdatedAt:    time.Unix(1, 0),
	}}, nil
}

func (f *fakeRuntimeService) SendInput(_ context.Context, req rt.SendInputRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendReq = req
	return f.sendErr
}

func (f *fakeRuntimeService) ListPanes(context.Context) (rt.ListPanesResponse, error) {
	return rt.ListPanesResponse{Panes: []rt.Pane{fakePane("pane-1")}}, nil
}

func (f *fakeRuntimeService) InspectPane(context.Context, rt.InspectPaneRequest) (rt.InspectPaneResponse, error) {
	return rt.InspectPaneResponse{Pane: fakePane("pane-1")}, nil
}

func (f *fakeRuntimeService) SnapshotOutput(context.Context, rt.SnapshotOutputRequest) (rt.SnapshotOutputResponse, error) {
	pane := fakePane("pane-1")
	return rt.SnapshotOutputResponse{Pane: pane, Output: "snapshot"}, nil
}

func (f *fakeRuntimeService) InspectRuntime(context.Context, rt.InspectRuntimeRequest) (rt.InspectRuntimeResponse, error) {
	pane := fakePane("pane-1")
	return rt.InspectRuntimeResponse{
		Message: "1 managed pane(s)",
		Counts:  rt.RuntimeCounts{Managed: 1, Starting: 1, Active: 1},
		Panes:   []rt.Pane{pane},
		Tasks:   []rt.TaskPaneGroup{{TaskID: "task-1", Panes: []rt.Pane{pane}}},
		Roles:   []rt.RolePaneGroup{{Role: rt.PaneRoleTest, Panes: []rt.Pane{pane}}},
		Outputs: []rt.PaneOutputSummary{{PaneID: pane.ID, TaskID: pane.TaskID, Role: pane.Role, Status: pane.Status, UpdatedAt: pane.UpdatedAt}},
	}, nil
}

func (f *fakeRuntimeService) RecentEvents(_ context.Context, req rt.RecentEventsRequest) (rt.RecentEventsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recentReq = req
	return rt.RecentEventsResponse{Events: []rt.EventSummary{{
		Type:    eventbus.TypeTestPassed,
		PaneID:  "pane-1",
		TaskID:  "task-1",
		Message: "ok",
		Time:    time.Unix(1, 0),
	}}}, nil
}

func (f *fakeRuntimeService) ClosePane(context.Context, rt.ClosePaneRequest) (rt.ClosePaneResponse, error) {
	return rt.ClosePaneResponse{Pane: fakePane("pane-1")}, nil
}

func (f *fakeRuntimeService) Reconcile(context.Context, rt.ReconcileRequest) (rt.ReconcileResponse, error) {
	pane := fakePane("pane-1")
	return rt.ReconcileResponse{Panes: []rt.Pane{pane}, Active: []rt.Pane{pane}}, nil
}

func (f *fakeRuntimeService) Cleanup(context.Context, rt.CleanupRequest) (rt.CleanupResponse, error) {
	response := rt.CleanupResponse{
		Closed: []rt.Pane{fakePane("pane-1")},
		Failed: []rt.CleanupFailure{{
			Pane:  fakePane("pane-failed"),
			Error: "close failed",
		}},
	}
	return response, f.cleanupErr
}

func (f *fakeRuntimeService) ApplyExecutionPlan(_ context.Context, req rt.ApplyExecutionPlanRequest) (rt.ApplyExecutionPlanResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyPlanCalled = true
	f.applyPlanReq = req
	tabID := rt.ZellijTabID(7)
	return rt.ApplyExecutionPlanResponse{
		RequestID: req.RequestID,
		Session:   req.Session,
		Layout:    req.Layout,
		Panes: []rt.Pane{
			{
				ID:           req.Panes[0].ID,
				TaskID:       rt.TaskID(req.Session),
				ZellijPaneID: "terminal_1",
				ZellijTabID:  &tabID,
				TabName:      req.Session,
				Role:         req.Panes[0].Role,
				Status:       rt.PaneStatusStarting,
				CreatedAt:    time.Unix(1, 0),
				UpdatedAt:    time.Unix(1, 0),
			},
			{
				ID:           req.Panes[1].ID,
				TaskID:       rt.TaskID(req.Session),
				ZellijPaneID: "terminal_2",
				ZellijTabID:  &tabID,
				TabName:      req.Session,
				Role:         req.Panes[1].Role,
				Status:       rt.PaneStatusStarting,
				CreatedAt:    time.Unix(1, 0),
				UpdatedAt:    time.Unix(1, 0),
			},
		},
	}, nil
}

func (f *fakeRuntimeService) SubscribeEvents(ctx context.Context) (<-chan eventbus.Event, func(), error) {
	ch := make(chan eventbus.Event, 8)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	unsub := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, sub := range f.subs {
			if sub == ch {
				f.subs = append(f.subs[:i], f.subs[i+1:]...)
				break
			}
		}
		close(ch)
	}
	go func() {
		<-ctx.Done()
	}()
	return ch, unsub, nil
}

func (f *fakeRuntimeService) publish(event eventbus.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, sub := range f.subs {
		sub <- event
	}
}

func fakePane(id rt.PaneID) rt.Pane {
	tabID := rt.ZellijTabID(7)
	return rt.Pane{
		ID:           id,
		TaskID:       "task-1",
		AgentID:      "agent-1",
		ZellijPaneID: "terminal_1",
		ZellijTabID:  &tabID,
		TabName:      "agentd-test",
		Role:         rt.PaneRoleTest,
		Status:       rt.PaneStatusStarting,
		CreatedAt:    time.Unix(1, 0),
		UpdatedAt:    time.Unix(1, 0),
	}
}

func netListenUnix(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	path := fmt.Sprintf("/tmp/agentd-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
	return path
}

func TestServerSubmitExecutionPlan(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	body := strings.NewReader(`{
		"type":"execution_plan",
		"request_id":"req_123",
		"payload":{
			"session":"feature-auth",
			"layout":"triple-horizontal",
			"panes":[
				{"id":"planner","role":"planner"},
				{"id":"frontend","role":"react-dev"}
			]
		}
	}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/requests", body)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if !service.applyPlanCalled {
		t.Fatal("ApplyExecutionPlan was not called")
	}
	if service.applyPlanReq.RequestID != "req_123" || service.applyPlanReq.Session != "feature-auth" {
		t.Fatalf("ApplyExecutionPlan request = %#v, want req_123 feature-auth", service.applyPlanReq)
	}
	if len(service.applyPlanReq.Panes) != 2 || service.applyPlanReq.Panes[0].ID != "planner" {
		t.Fatalf("ApplyExecutionPlan panes = %#v, want planner and frontend", service.applyPlanReq.Panes)
	}

	var decoded ExecutionPlanResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.RequestID != "req_123" || len(decoded.Panes) != 2 {
		t.Fatalf("response = %#v, want echoed request_id and panes", decoded)
	}
}

func TestServerSubmitExecutionPlanRejectsUnknownType(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	body := strings.NewReader(`{"type":"unknown","request_id":"req_1","payload":{}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/requests", body)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if service.applyPlanCalled {
		t.Fatal("ApplyExecutionPlan should not be called for unknown type")
	}
}

func TestServerHealth(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"status":"ok"`)) {
		t.Fatalf("body = %s, want ok status", response.Body.String())
	}
}
