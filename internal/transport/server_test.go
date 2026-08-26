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

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

func TestServerAgentRoutes(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)

	start := httptest.NewRecorder()
	server.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/v1/agents", strings.NewReader(`{"kind":"codex","cwd":"/workspace/project","access":"read-only","args":["--model","gpt-5"],"source_session":"physical-a","source_zellij_pane_id":"terminal_2"}`)))
	if start.Code != http.StatusCreated {
		t.Fatalf("start status = %d, want 201; body=%s", start.Code, start.Body.String())
	}
	if service.agentStartReq.Kind != codingagent.KindCodex || service.agentStartReq.CWD != "/workspace/project" || service.agentStartReq.AccessMode != codingagent.AccessReadOnly || service.agentStartReq.SourceZellijSession != "physical-a" || service.agentStartReq.SourceZellijPaneID != "terminal_2" {
		t.Fatalf("StartAgent request = %#v", service.agentStartReq)
	}
	var startResponse StartAgentResponse
	if err := json.Unmarshal(start.Body.Bytes(), &startResponse); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResponse.Agent.Agent.Access != "read-only" {
		t.Fatalf("start response access = %q, want read-only", startResponse.Agent.Agent.Access)
	}

	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/agents", nil))
	if list.Code != http.StatusOK || service.agentListCalls != 1 {
		t.Fatalf("list status=%d calls=%d body=%s", list.Code, service.agentListCalls, list.Body.String())
	}

	focus := httptest.NewRecorder()
	server.ServeHTTP(focus, httptest.NewRequest(http.MethodPost, "/v1/agents/agent%2F1/focus", strings.NewReader(`{"source_session":"physical-a","source_zellij_pane_id":"terminal_2"}`)))
	if focus.Code != http.StatusOK {
		t.Fatalf("focus status = %d, want 200; body=%s", focus.Code, focus.Body.String())
	}
	if service.agentFocusReq.AgentID != "agent/1" || service.agentFocusReq.SourceZellijSession != "physical-a" || service.agentFocusReq.SourceZellijPaneID != "terminal_2" {
		t.Fatalf("FocusAgent request = %#v", service.agentFocusReq)
	}

	next := httptest.NewRecorder()
	server.ServeHTTP(next, httptest.NewRequest(http.MethodPost, "/v1/agents/next", strings.NewReader(`{"source_session":"physical-b","source_zellij_pane_id":"terminal_8","idle_only":true}`)))
	if next.Code != http.StatusOK {
		t.Fatalf("next status = %d, want 200; body=%s", next.Code, next.Body.String())
	}
	if service.agentNextCalls != 1 {
		t.Fatalf("FocusNextAgent calls = %d, want 1", service.agentNextCalls)
	}
	if service.agentNextReq.SourceZellijSession != "physical-b" || service.agentNextReq.SourceZellijPaneID != "terminal_8" || !service.agentNextReq.IdleOnly {
		t.Fatalf("FocusNextAgent request = %#v", service.agentNextReq)
	}
	var nextResponse FocusNextAgentResponse
	if err := json.Unmarshal(next.Body.Bytes(), &nextResponse); err != nil {
		t.Fatalf("decode next response: %v", err)
	}
	if !nextResponse.Focused || nextResponse.Agent.Agent.ID != "agent-2" {
		t.Fatalf("FocusNextAgent response = %#v", nextResponse)
	}

	doubleEscaped := httptest.NewRecorder()
	server.ServeHTTP(doubleEscaped, httptest.NewRequest(http.MethodPost, "/v1/agents/agent%252F1/focus", strings.NewReader(`{"source_session":"physical-a","source_zellij_pane_id":"terminal_2"}`)))
	if doubleEscaped.Code != http.StatusOK {
		t.Fatalf("double-escaped focus status = %d, want 200; body=%s", doubleEscaped.Code, doubleEscaped.Body.String())
	}
	if service.agentFocusReq.AgentID != "agent%2F1" {
		t.Fatalf("double-escaped FocusAgent id = %q, want literal agent%%2F1", service.agentFocusReq.AgentID)
	}
}

func TestServerFocusNextAgentReturnsSuccessfulNoOp(t *testing.T) {
	service := newFakeRuntimeService()
	service.agentNextResponseSet = true
	service.agentNextResponse = codingagent.FocusNextAgentResponse{
		Focused: false,
		Agent:   fakeAgentResponse(codingagent.KindCodex, "ignored-agent").Agent,
	}
	server := newTestServer(t, service)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/agents/next", strings.NewReader(`{"source_session":"physical-b","source_zellij_pane_id":"terminal_8"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw no-op response: %v", err)
	}
	rawFocused, ok := raw["focused"]
	if !ok {
		t.Fatalf("no-op response body=%s, want explicit focused key", recorder.Body.String())
	}
	var focused bool
	if err := json.Unmarshal(rawFocused, &focused); err != nil {
		t.Fatalf("decode focused value: %v", err)
	}
	if focused {
		t.Fatalf("no-op focused=%t, want false", focused)
	}
	var response FocusNextAgentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Focused {
		t.Fatalf("response=%#v, want no-op", response)
	}
}

func TestServerAgentRoutesRejectTrailingJSONWithoutDispatch(t *testing.T) {
	validStart := `{"kind":"codex","cwd":"/tmp","source_session":"physical-a","source_zellij_pane_id":"terminal_2"}`
	validFocus := `{"source_session":"physical-a","source_zellij_pane_id":"terminal_2"}`
	validNext := `{"source_session":"physical-b","source_zellij_pane_id":"terminal_8"}`
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "start junk", path: "/v1/agents", body: validStart + ` junk`},
		{name: "start second object", path: "/v1/agents", body: validStart + ` {}`},
		{name: "focus junk", path: "/v1/agents/agent-1/focus", body: validFocus + ` junk`},
		{name: "focus second object", path: "/v1/agents/agent-1/focus", body: validFocus + ` {}`},
		{name: "next junk", path: "/v1/agents/next", body: validNext + ` junk`},
		{name: "next second object", path: "/v1/agents/next", body: validNext + ` {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newFakeRuntimeService()
			server := newTestServer(t, service)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
			if service.agentStartCalls != 0 || service.agentFocusCalls != 0 || service.agentNextCalls != 0 {
				t.Fatalf("service dispatch start=%d focus=%d next=%d, want zero", service.agentStartCalls, service.agentFocusCalls, service.agentNextCalls)
			}
		})
	}
}

func TestServerAgentRoutesRejectInvalidMethodShapeAndJSON(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		wantStatus               int
	}{
		{name: "collection put", method: http.MethodPut, path: "/v1/agents", wantStatus: http.StatusMethodNotAllowed},
		{name: "focus get", method: http.MethodGet, path: "/v1/agents/agent-1/focus", wantStatus: http.StatusMethodNotAllowed},
		{name: "next get", method: http.MethodGet, path: "/v1/agents/next", wantStatus: http.StatusMethodNotAllowed},
		{name: "unknown action", method: http.MethodPost, path: "/v1/agents/agent-1/stop", body: `{}`, wantStatus: http.StatusNotFound},
		{name: "extra suffix", method: http.MethodPost, path: "/v1/agents/agent-1/focus/extra", body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "malformed start", method: http.MethodPost, path: "/v1/agents", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "malformed focus", method: http.MethodPost, path: "/v1/agents/agent-1/focus", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "malformed next", method: http.MethodPost, path: "/v1/agents/next", body: `{`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newFakeRuntimeService()
			server := newTestServer(t, service)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if service.agentFocusCalls != 0 || service.agentNextCalls != 0 {
				t.Fatalf("invalid request dispatched focus=%d next=%d", service.agentFocusCalls, service.agentNextCalls)
			}
		})
	}
}

func TestServerNextAgentErrorsMapToStableHTTPResponses(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		err        error
		wantStatus int
		wantCode   ErrorCode
	}{
		{name: "session required", body: `{"source_zellij_pane_id":"terminal_8"}`, err: codingagent.ErrAgentSourceRequired, wantStatus: http.StatusBadRequest, wantCode: CodeBadRequest},
		{name: "pane required", body: `{"source_session":"physical-b"}`, err: codingagent.ErrAgentSourceRequired, wantStatus: http.StatusBadRequest, wantCode: CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newFakeRuntimeService()
			service.agentNextErr = tt.err
			server := newTestServer(t, service)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/agents/next", strings.NewReader(tt.body)))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if service.agentNextCalls != 1 {
				t.Fatalf("FocusNextAgent calls = %d, want 1", service.agentNextCalls)
			}
			var decoded ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", decoded.Error.Code, tt.wantCode)
			}
		})
	}
}

func TestServerAgentErrorsMapToStableHTTPResponses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   ErrorCode
	}{
		{name: "not found", err: fmt.Errorf("lookup: %w", codingagent.ErrNotFound), wantStatus: http.StatusNotFound, wantCode: CodeNotFound},
		{name: "invalid kind", err: fmt.Errorf("validate: %w", codingagent.ErrInvalidAgentKind), wantStatus: http.StatusBadRequest, wantCode: CodeBadRequest},
		{name: "invalid cwd", err: fmt.Errorf("validate: %w", codingagent.ErrInvalidAgentCWD), wantStatus: http.StatusBadRequest, wantCode: CodeBadRequest},
		{name: "invalid access", err: fmt.Errorf("validate: %w", codingagent.ErrInvalidAccessMode), wantStatus: http.StatusBadRequest, wantCode: CodeBadRequest},
		{name: "source required", err: fmt.Errorf("validate: %w", codingagent.ErrAgentSourceRequired), wantStatus: http.StatusBadRequest, wantCode: CodeBadRequest},
		{name: "internal", err: errors.New("backend unavailable"), wantStatus: http.StatusInternalServerError, wantCode: CodeRuntimeError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newFakeRuntimeService()
			service.agentStartErr = tt.err
			server := newTestServer(t, service)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/agents", strings.NewReader(`{"kind":"codex","cwd":"/tmp","source_session":"physical-a","source_zellij_pane_id":"terminal_2"}`)))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			var decoded ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", decoded.Error.Code, tt.wantCode)
			}
			if strings.Contains(decoded.Error.Message, "codingagent.") || strings.Contains(decoded.Error.Message, "*codingagent") {
				t.Fatalf("error leaks Go type name: %q", decoded.Error.Message)
			}
		})
	}
}

func TestServerCreatePane(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)

	body := strings.NewReader(`{"id":"pane-1","task_id":"task-1","zellij_session":"physical-a","agent_id":"agent-1","role":"test","same_tab_as_pane_id":"manager","tab_name":"agentd-test","command":["go","test"],"cwd":"."}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes", body)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if service.createReq.ID != "pane-1" || service.createReq.TaskID != "task-1" || service.createReq.NewTab {
		t.Fatalf("CreatePane request = %#v, want decoded logical request", service.createReq)
	}
	if service.createReq.SameTabAsPaneID != "manager" {
		t.Fatalf("CreatePane request SameTabAsPaneID = %q, want manager", service.createReq.SameTabAsPaneID)
	}
	if service.createReq.ZellijSession != "physical-a" {
		t.Fatalf("CreatePane request ZellijSession = %q, want physical-a", service.createReq.ZellijSession)
	}
	var decoded CreatePaneResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Pane.ID != "pane-1" || decoded.Pane.ZellijPaneID != "terminal_1" {
		t.Fatalf("response pane = %#v, want logical and zellij ids", decoded.Pane)
	}
}

func TestHandleCreatePaneInitialInput(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes", strings.NewReader(`{
		"id": "coder",
		"zellij_session": "physical-a",
		"initial_input": "implement ticket\n",
		"initial_input_ready_text": "›"
	}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if service.createReq.InitialInput != "implement ticket\n" || service.createReq.InitialInputReadyText != "›" {
		t.Fatalf("CreatePane request = %#v, want initial input fields", service.createReq)
	}
}

func TestServerShutdownStopsListeningAndRemovesSocket(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/agentd-shutdown-test-%d.sock", time.Now().UnixNano())
	defer os.Remove(socketPath)
	server, err := NewServer(ServerOptions{
		Service:            newFakeRuntimeService(),
		VoiceNotifications: noopVoiceNotificationService{},
		SocketPath:         socketPath,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe(context.Background()) }()
	client := NewClient(ClientOptions{SocketPath: socketPath, Timeout: time.Second})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	response, err := client.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if response.Status != "stopping" {
		t.Fatalf("Shutdown() status = %q, want stopping", response.Status)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ListenAndServe() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListenAndServe() did not stop")
	}
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket stat error = %v, want not exist", err)
	}
}

func TestServerCreatePaneInvalidTargetReturnsBadRequest(t *testing.T) {
	service := newFakeRuntimeService()
	service.createErr = rt.ErrInvalidPaneTarget
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes", strings.NewReader(`{"id":"pane-1"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var decoded ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error.Code != CodeBadRequest {
		t.Fatalf("error = %#v, want bad_request", decoded.Error)
	}
}

func TestServerCreatePaneWithRoles(t *testing.T) {
	roles := []string{"coder", "network-tracker", "console-tracker"}

	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			service := newFakeRuntimeService()
			if service == nil {
				t.Fatal("failed to initialize fake runtime service")
			}
			server := newTestServer(t, service)
			if server == nil {
				t.Fatal("failed to initialize test server")
			}

			payload := fmt.Sprintf(`{
				"id": "pane-%s",
				"task_id": "task-%s",
				"zellij_session": "physical-a",
				"agent_id": "agent-%s",
				"role": "%s",
				"new_tab": true,
				"tab_name": "agentd-%s",
				"command": ["./bin/agent-role", "%s"],
				"cwd": "."
			}`, role, role, role, role, role, role)

			body := strings.NewReader(payload)
			request := httptest.NewRequest(http.MethodPost, "/v1/panes", body)
			response := httptest.NewRecorder()

			server.ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
			}

			if service.createReq.ID != rt.PaneID("pane-"+role) || service.createReq.Role != role {
				t.Fatalf("CreatePane request = %#v, want role %s", service.createReq, role)
			}

			var decoded CreatePaneResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			if decoded.Pane.ID != "pane-"+role || decoded.Pane.Role != role {
				t.Fatalf("response pane = %#v, want role %s", decoded.Pane, role)
			}
		})
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

func TestServerWaitForOutputMarker(t *testing.T) {
	service := newFakeRuntimeService()
	service.markerResponse = rt.WaitForOutputMarkerResponse{
		PaneID:      "worker-1",
		Marker:      "DONE ",
		MatchedLine: "DONE ticket_id=TICKET-123",
		MatchedAt:   time.Unix(3, 0),
	}
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes/worker-1/wait-marker", strings.NewReader(`{"marker":"DONE ","match_prefix":true}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.markerReq.PaneID != "worker-1" || service.markerReq.Marker != "DONE " || !service.markerReq.MatchPrefix {
		t.Fatalf("WaitForOutputMarker request = %#v, want logical worker-1 prefix", service.markerReq)
	}
	var decoded WaitForOutputMarkerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.PaneID != "worker-1" || decoded.Marker != "DONE " || decoded.MatchedLine != "DONE ticket_id=TICKET-123" || !decoded.MatchedAt.Equal(time.Unix(3, 0)) {
		t.Fatalf("response = %#v, want mapped marker response", decoded)
	}
}

func TestServerWaitForOutputMarkerBypassesRequestTimeoutAndPropagatesCancellation(t *testing.T) {
	service := newFakeRuntimeService()
	service.markerResponse = rt.WaitForOutputMarkerResponse{PaneID: "worker-1", Marker: "DONE", MatchedAt: time.Unix(3, 0)}
	service.markerBlock = make(chan struct{})
	server, err := NewServer(ServerOptions{
		Service:            service,
		VoiceNotifications: noopVoiceNotificationService{},
		SocketPath:         "unused.sock",
		RequestTimeout:     10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/panes/worker-1/wait-marker", strings.NewReader(`{"marker":"DONE"}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("wait-marker returned before release; status=%d body=%s", response.Code, response.Body.String())
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case <-service.markerCanceled:
	case <-time.After(time.Second):
		t.Fatal("runtime marker context was not canceled with request")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-marker handler did not return after request cancellation")
	}
}

func TestServerClosePaneUsesLogicalID(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes/worker-1/close", strings.NewReader(`{}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.closeReq.PaneID != "worker-1" {
		t.Fatalf("ClosePane request = %#v, want logical worker-1", service.closeReq)
	}
	var decoded ClosePaneResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Pane.ID != "worker-1" {
		t.Fatalf("response pane = %#v, want worker-1", decoded.Pane)
	}
}

func TestServerPaneWaitAndCloseMapNotFound(t *testing.T) {
	for _, action := range []string{"wait-marker", "close"} {
		t.Run(action, func(t *testing.T) {
			service := newFakeRuntimeService()
			service.markerErr = rt.ErrPaneNotFound
			service.closeErr = rt.ErrPaneNotFound
			server := newTestServer(t, service)
			body := `{}`
			if action == "wait-marker" {
				body = `{"marker":"DONE"}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/panes/missing/"+action, strings.NewReader(body))
			response := httptest.NewRecorder()

			server.ServeHTTP(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
			}
			var decoded ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if decoded.Error.Code != CodeNotFound {
				t.Fatalf("error = %#v, want not_found", decoded.Error)
			}
		})
	}
}

func TestServerSendMessage(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"from":"planner","to":"tester","type":"task_request","body":"run tests"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.messageReq.FromPaneID != "planner" || service.messageReq.ToPaneID != "tester" || service.messageReq.Type != "task_request" {
		t.Fatalf("SendMessage request = %#v, want decoded message", service.messageReq)
	}
	var decoded SendMessageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.From.ID != "planner" || decoded.To.ID != "tester" || decoded.Type != "task_request" {
		t.Fatalf("response = %#v, want planner to tester", decoded)
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

func TestServerStreamsOnlyRequestedEventTypes(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	resp, err := http.Get(httpServer.URL + "/v1/events/stream?type=agent_state_changed")
	if err != nil {
		t.Fatalf("GET filtered stream: %v", err)
	}
	defer resp.Body.Close()

	service.publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "agent", Message: "large viewport"})
	service.publish(eventbus.Event{Type: eventbus.TypeAgentStateChanged, PaneID: "agent", AgentState: "idle"})

	var event Event
	if err := json.NewDecoder(resp.Body).Decode(&event); err != nil {
		t.Fatalf("decode filtered event: %v", err)
	}
	if event.Type != string(eventbus.TypeAgentStateChanged) || event.PaneID != "agent" {
		t.Fatalf("event = %#v, want agent_state_changed for agent", event)
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
		Service:            service,
		VoiceNotifications: noopVoiceNotificationService{},
		SocketPath:         "unused.sock",
		RequestTimeout:     time.Second,
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

type fakeRuntimeService struct {
	mu sync.Mutex

	createCalled         bool
	createReq            rt.CreatePaneRequest
	createErr            error
	applyPlanCalled      bool
	applyPlanReq         rt.ApplyExecutionPlanRequest
	sendReq              rt.SendInputRequest
	sendErr              error
	messageReq           rt.SendMessageRequest
	messageErr           error
	recentReq            rt.RecentEventsRequest
	cleanupErr           error
	markerReq            rt.WaitForOutputMarkerRequest
	markerResponse       rt.WaitForOutputMarkerResponse
	markerErr            error
	markerBlock          chan struct{}
	markerCanceled       chan struct{}
	closeReq             rt.ClosePaneRequest
	closeErr             error
	agentStartReq        codingagent.StartAgentRequest
	agentStartCalls      int
	agentStartErr        error
	agentListCalls       int
	agentListErr         error
	agentFocusReq        codingagent.FocusAgentRequest
	agentFocusCalls      int
	agentFocusErr        error
	agentNextReq         codingagent.FocusNextAgentRequest
	agentNextCalls       int
	agentNextErr         error
	agentNextResponseSet bool
	agentNextResponse    codingagent.FocusNextAgentResponse

	subs []chan eventbus.Event
}

func (f *fakeRuntimeService) StartAgent(_ context.Context, req codingagent.StartAgentRequest) (codingagent.StartAgentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentStartCalls++
	f.agentStartReq = req
	if f.agentStartErr != nil {
		return codingagent.StartAgentResponse{}, f.agentStartErr
	}
	response := fakeAgentResponse(req.Kind, "agent-1")
	response.Agent.Agent.AccessMode = req.AccessMode
	return response, nil
}

func (f *fakeRuntimeService) ListAgents(context.Context) (codingagent.ListAgentsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentListCalls++
	if f.agentListErr != nil {
		return codingagent.ListAgentsResponse{}, f.agentListErr
	}
	response := fakeAgentResponse(codingagent.KindCodex, "agent-1")
	return codingagent.ListAgentsResponse{Agents: []codingagent.AgentWithPane{response.Agent}}, nil
}

func (f *fakeRuntimeService) FocusAgent(_ context.Context, req codingagent.FocusAgentRequest) (codingagent.FocusAgentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentFocusCalls++
	f.agentFocusReq = req
	if f.agentFocusErr != nil {
		return codingagent.FocusAgentResponse{}, f.agentFocusErr
	}
	return codingagent.FocusAgentResponse(fakeAgentResponse(codingagent.KindCodex, req.AgentID)), nil
}

func (f *fakeRuntimeService) FocusNextAgent(_ context.Context, req codingagent.FocusNextAgentRequest) (codingagent.FocusNextAgentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentNextCalls++
	f.agentNextReq = req
	if f.agentNextErr != nil {
		return codingagent.FocusNextAgentResponse{}, f.agentNextErr
	}
	if f.agentNextResponseSet {
		return f.agentNextResponse, nil
	}
	return codingagent.FocusNextAgentResponse{
		Focused: true,
		Agent:   fakeAgentResponse(codingagent.KindCodex, "agent-2").Agent,
	}, nil
}

func fakeAgentResponse(kind codingagent.Kind, id codingagent.ID) codingagent.StartAgentResponse {
	createdAt := time.Unix(10, 0)
	return codingagent.StartAgentResponse{Agent: codingagent.AgentWithPane{
		Agent: codingagent.Record{ID: id, Kind: kind, PaneID: rt.PaneID(id), State: codingagent.StateUnknown, CreatedAt: createdAt, StateChangedAt: time.Unix(20, 0)},
		Pane:  rt.Pane{ID: rt.PaneID(id), ZellijPaneID: "terminal_7", Status: rt.PaneStatusRunning, CreatedAt: createdAt, UpdatedAt: time.Unix(30, 0)},
	}}
}

func newFakeRuntimeService() *fakeRuntimeService {
	return &fakeRuntimeService{markerCanceled: make(chan struct{}, 1)}
}

func (f *fakeRuntimeService) CreatePane(_ context.Context, req rt.CreatePaneRequest) (rt.CreatePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalled = true
	f.createReq = req
	if strings.TrimSpace(req.ZellijSession) == "" {
		return rt.CreatePaneResponse{}, rt.ErrZellijSessionRequired
	}
	if f.createErr != nil {
		return rt.CreatePaneResponse{}, f.createErr
	}
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

func (f *fakeRuntimeService) SendMessage(_ context.Context, req rt.SendMessageRequest) (rt.SendMessageResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messageReq = req
	return rt.SendMessageResponse{
		From:          fakePane(req.FromPaneID),
		To:            fakePane(req.ToPaneID),
		Type:          req.Type,
		Body:          req.Body,
		DeliveredText: "[agentd] message\n" + req.Body + "\n",
	}, f.messageErr
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

func (f *fakeRuntimeService) WaitForOutputMarker(ctx context.Context, req rt.WaitForOutputMarkerRequest) (rt.WaitForOutputMarkerResponse, error) {
	f.mu.Lock()
	f.markerReq = req
	response := f.markerResponse
	err := f.markerErr
	block := f.markerBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			select {
			case f.markerCanceled <- struct{}{}:
			default:
			}
			return rt.WaitForOutputMarkerResponse{}, ctx.Err()
		}
	}
	return response, err
}

func (f *fakeRuntimeService) InspectRuntime(context.Context, rt.InspectRuntimeRequest) (rt.InspectRuntimeResponse, error) {
	pane := fakePane("pane-1")
	return rt.InspectRuntimeResponse{
		Message: "1 managed pane(s)",
		Counts:  rt.RuntimeCounts{Managed: 1, Starting: 1, Active: 1},
		Panes:   []rt.Pane{pane},
		Tasks:   []rt.TaskPaneGroup{{TaskID: "task-1", Panes: []rt.Pane{pane}}},
		Roles:   []rt.RolePaneGroup{{Role: "test", Panes: []rt.Pane{pane}}},
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

func (f *fakeRuntimeService) ClosePane(_ context.Context, req rt.ClosePaneRequest) (rt.ClosePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeReq = req
	return rt.ClosePaneResponse{Pane: fakePane(req.PaneID)}, f.closeErr
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
	if strings.TrimSpace(req.ZellijSession) == "" {
		return rt.ApplyExecutionPlanResponse{}, rt.ErrZellijSessionRequired
	}
	tabID := rt.ZellijTabID(7)

	var tabs []rt.ExecutionPlanTabResult
	for _, tSpec := range req.Tabs {
		var panes []rt.Pane
		for _, pSpec := range tSpec.Panes {
			panes = append(panes, rt.Pane{
				ID:           pSpec.ID,
				TaskID:       rt.TaskID(req.Session),
				ZellijPaneID: "terminal_mock",
				ZellijTabID:  &tabID,
				TabName:      tSpec.Name,
				Role:         pSpec.Role,
				Status:       rt.PaneStatusStarting,
				CreatedAt:    time.Unix(1, 0),
				UpdatedAt:    time.Unix(1, 0),
			})
		}
		tabs = append(tabs, rt.ExecutionPlanTabResult{
			Name:  tSpec.Name,
			Panes: panes,
		})
	}

	return rt.ApplyExecutionPlanResponse{
		RequestID: req.RequestID,
		Session:   req.Session,
		Layout:    req.Layout,
		Tabs:      tabs,
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
		SessionID:    "default",
		TabID:        "tab-1",
		TaskID:       "task-1",
		AgentID:      "agent-1",
		ZellijPaneID: "terminal_1",
		ZellijTabID:  &tabID,
		TabName:      "agentd-test",
		Role:         "test",
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
			"zellij_session":"physical-a",
			"layout":"triple-horizontal",
			"tabs":[
				{
					"name": "feature-auth",
					"panes": [
						{"id":"planner","role":"planner"},
						{"id":"frontend","role":"react-dev"}
					]
				}
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
	if service.applyPlanReq.RequestID != "req_123" || service.applyPlanReq.Session != "feature-auth" || service.applyPlanReq.ZellijSession != "physical-a" {
		t.Fatalf("ApplyExecutionPlan request = %#v, want req_123 feature-auth", service.applyPlanReq)
	}
	if len(service.applyPlanReq.Tabs) != 1 || len(service.applyPlanReq.Tabs[0].Panes) != 2 || service.applyPlanReq.Tabs[0].Panes[0].ID != "planner" {
		t.Fatalf("ApplyExecutionPlan tabs = %#v, want planner and frontend in tab feature-auth", service.applyPlanReq.Tabs)
	}

	var decoded ExecutionPlanResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.RequestID != "req_123" || len(decoded.Tabs) != 1 || len(decoded.Tabs[0].Panes) != 2 {
		t.Fatalf("response = %#v, want echoed request_id and tabs with panes", decoded)
	}
}

func TestServerCreatePaneMissingZellijSessionReturnsBadRequest(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/panes", strings.NewReader(`{"id":"pane-1"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestServerSubmitExecutionPlanMissingZellijSessionReturnsBadRequest(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/v1/requests", strings.NewReader(`{
		"type":"execution_plan",
		"request_id":"req_123",
		"payload":{"session":"feature-auth","tabs":[{"name":"main","panes":[{"id":"planner"}]}]}
	}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
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
	if !bytes.Contains(response.Body.Bytes(), []byte(`"capabilities":["agent_access_read_only_v1"]`)) {
		t.Fatalf("body = %s, want read-only capability", response.Body.String())
	}
}

func (f *fakeRuntimeService) ListSessions(context.Context) ([]rt.SessionRecord, error) {
	tabID := rt.ZellijTabID(7)
	pane := rt.PaneRecord{
		ID:          "pane-1",
		SessionID:   "default",
		TabID:       "tab-1",
		ZellijTabID: &tabID,
		TabName:     "agentd-test",
		Role:        "test",
		Status:      rt.PaneStatusStarting,
	}
	tab := rt.TabRecord{
		ID:   "tab-1",
		Name: "agentd-test",
		Panes: map[rt.PaneID]rt.PaneRecord{
			"pane-1": pane,
		},
	}
	session := rt.SessionRecord{
		ID: "default",
		Tabs: map[rt.TabID]rt.TabRecord{
			"tab-1": tab,
		},
	}
	return []rt.SessionRecord{session}, nil
}

func (f *fakeRuntimeService) GetSession(_ context.Context, id rt.SessionID) (rt.SessionRecord, error) {
	if id != "default" {
		return rt.SessionRecord{}, rt.ErrSessionNotFound
	}
	tabID := rt.ZellijTabID(7)
	pane := rt.PaneRecord{
		ID:          "pane-1",
		SessionID:   "default",
		TabID:       "tab-1",
		ZellijTabID: &tabID,
		TabName:     "agentd-test",
		Role:        "test",
		Status:      rt.PaneStatusStarting,
	}
	tab := rt.TabRecord{
		ID:   "tab-1",
		Name: "agentd-test",
		Panes: map[rt.PaneID]rt.PaneRecord{
			"pane-1": pane,
		},
	}
	return rt.SessionRecord{
		ID: "default",
		Tabs: map[rt.TabID]rt.TabRecord{
			"tab-1": tab,
		},
	}, nil
}

func (f *fakeRuntimeService) ListTabs(_ context.Context, sessionID rt.SessionID) ([]rt.TabRecord, error) {
	if sessionID != "default" {
		return nil, rt.ErrSessionNotFound
	}
	tabID := rt.ZellijTabID(7)
	pane := rt.PaneRecord{
		ID:          "pane-1",
		SessionID:   "default",
		TabID:       "tab-1",
		ZellijTabID: &tabID,
		TabName:     "agentd-test",
		Role:        "test",
		Status:      rt.PaneStatusStarting,
	}
	tab := rt.TabRecord{
		ID:   "tab-1",
		Name: "agentd-test",
		Panes: map[rt.PaneID]rt.PaneRecord{
			"pane-1": pane,
		},
	}
	return []rt.TabRecord{tab}, nil
}

func (f *fakeRuntimeService) GetTab(_ context.Context, sessionID rt.SessionID, tabID rt.TabID) (rt.TabRecord, error) {
	if sessionID != "default" {
		return rt.TabRecord{}, rt.ErrSessionNotFound
	}
	if tabID != "tab-1" {
		return rt.TabRecord{}, rt.ErrTabNotFound
	}
	tabIDVal := rt.ZellijTabID(7)
	pane := rt.PaneRecord{
		ID:          "pane-1",
		SessionID:   "default",
		TabID:       "tab-1",
		ZellijTabID: &tabIDVal,
		TabName:     "agentd-test",
		Role:        "test",
		Status:      rt.PaneStatusStarting,
	}
	return rt.TabRecord{
		ID:   "tab-1",
		Name: "agentd-test",
		Panes: map[rt.PaneID]rt.PaneRecord{
			"pane-1": pane,
		},
	}, nil
}

func TestServerSessionsAndTabs(t *testing.T) {
	service := newFakeRuntimeService()
	server := newTestServer(t, service)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	resp := httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("GET /v1/sessions code = %d, want 200", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/default", nil)
	resp = httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("GET /v1/sessions/default code = %d, want 200", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/default/tabs", nil)
	resp = httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("GET /v1/sessions/default/tabs code = %d, want 200", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/default/tabs/tab-1", nil)
	resp = httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("GET /v1/sessions/default/tabs/tab-1 code = %d, want 200", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/default/tabs/tab-1/panes", nil)
	resp = httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("GET /v1/sessions/default/tabs/tab-1/panes code = %d, want 200", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/missing", nil)
	resp = httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Errorf("GET /v1/sessions/missing code = %d, want 404", resp.Code)
	}
}
