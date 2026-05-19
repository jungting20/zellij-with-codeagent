package fake

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/transport"
)

func TestPlannerRunHappyPath(t *testing.T) {
	client := newFakeClient()
	planner := New(client, Options{IdleTimeout: time.Second, TotalTimeout: 5 * time.Second})

	result, err := planner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Panes) != 5 {
		t.Fatalf("created panes = %d, want 5", len(result.Panes))
	}
	if result.Cleanup == nil || len(result.Cleanup.Closed) != 5 {
		t.Fatalf("cleanup = %#v, want closed panes", result.Cleanup)
	}
	if len(result.Events) != 3 {
		t.Fatalf("events = %#v, want server_ready, test_failed, test_passed", result.Events)
	}
	if client.cleanupReq.TaskID != DefaultTaskID {
		t.Fatalf("cleanup task = %q, want %q", client.cleanupReq.TaskID, DefaultTaskID)
	}
	for _, pane := range result.Panes {
		if pane.ZellijPaneID != "" {
			t.Fatalf("planner result exposed zellij id %q in fake client pane; fake planner should depend on logical ids", pane.ZellijPaneID)
		}
	}
}

func TestPlannerIgnoresDuplicateEvents(t *testing.T) {
	client := newFakeClient()
	client.duplicateEvents = true
	planner := New(client, Options{IdleTimeout: time.Second, TotalTimeout: 5 * time.Second})

	result, err := planner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	serverReady := 0
	for _, event := range result.Events {
		if event.Type == string(eventbus.TypeServerReady) {
			serverReady++
		}
	}
	if serverReady != 1 {
		t.Fatalf("server_ready events in result = %d, want 1", serverReady)
	}
}

func TestPlannerCreateFailureCleansUp(t *testing.T) {
	client := newFakeClient()
	client.failCreateAfter = 2
	planner := New(client, Options{IdleTimeout: time.Second, TotalTimeout: 5 * time.Second})

	_, err := planner.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want create failure")
	}
	if client.cleanupReq.TaskID != DefaultTaskID {
		t.Fatalf("cleanup task = %q, want %q after create failure", client.cleanupReq.TaskID, DefaultTaskID)
	}
}

func TestPlannerIdleTimeoutReportsUnresolvedEvent(t *testing.T) {
	client := newFakeClient()
	client.suppressEvents = true
	planner := New(client, Options{IdleTimeout: 20 * time.Millisecond, TotalTimeout: time.Second})

	_, err := planner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "waiting for server_ready") {
		t.Fatalf("Run() error = %v, want server_ready wait timeout", err)
	}
}

func TestPlannerLeaveOpenSkipsCleanup(t *testing.T) {
	client := newFakeClient()
	planner := New(client, Options{LeaveOpen: true, IdleTimeout: time.Second, TotalTimeout: 5 * time.Second})

	result, err := planner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Cleanup != nil {
		t.Fatalf("cleanup = %#v, want nil in leave-open mode", result.Cleanup)
	}
	if client.cleanupReq.TaskID != "" {
		t.Fatalf("cleanup request = %#v, want none", client.cleanupReq)
	}
}

type fakeClient struct {
	mu sync.Mutex

	panes           []transport.Pane
	events          chan transport.Event
	cleanupReq      transport.CleanupRequest
	failCreateAfter int
	duplicateEvents bool
	suppressEvents  bool
}

func newFakeClient() *fakeClient {
	return &fakeClient{events: make(chan transport.Event, 64)}
}

func (f *fakeClient) CreatePane(_ context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreateAfter > 0 && len(f.panes) >= f.failCreateAfter {
		return transport.CreatePaneResponse{}, errors.New("create failed")
	}
	tabID := 7
	if req.NewTab {
		tabID = 7
	} else if req.ZellijTabID != nil {
		tabID = *req.ZellijTabID
	}
	pane := transport.Pane{
		ID:          req.ID,
		TaskID:      req.TaskID,
		AgentID:     req.AgentID,
		ZellijTabID: &tabID,
		TabName:     req.TabName,
		Role:        req.Role,
		Status:      "starting",
		CreatedAt:   time.Unix(1, 0),
		UpdatedAt:   time.Unix(1, 0),
	}
	f.panes = append(f.panes, pane)
	return transport.CreatePaneResponse{Pane: pane}, nil
}

func (f *fakeClient) SendInput(_ context.Context, paneID string, req transport.SendInputRequest) error {
	if f.suppressEvents {
		return nil
	}
	var eventType eventbus.EventType
	switch {
	case strings.Contains(req.Text, ":3000"):
		eventType = eventbus.TypeServerReady
	case strings.Contains(req.Text, "fail"):
		eventType = eventbus.TypeTestFailed
	case strings.Contains(req.Text, "pass"):
		eventType = eventbus.TypeTestPassed
	default:
		return nil
	}
	event := transport.Event{Type: string(eventType), PaneID: paneID, TaskID: DefaultTaskID, Message: req.Text, Time: time.Unix(1, 0)}
	f.events <- event
	if f.duplicateEvents {
		f.events <- event
	}
	return nil
}

func (f *fakeClient) SnapshotOutput(_ context.Context, paneID string, _ transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	return transport.SnapshotOutputResponse{
		Pane:   transport.Pane{ID: paneID},
		Output: "agentd_fake_planner_ready:" + paneID,
	}, nil
}

func (f *fakeClient) RecentEvents(_ context.Context, _ int, _ ...string) (transport.RecentEventsResponse, error) {
	return transport.RecentEventsResponse{}, nil
}

func (f *fakeClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	return &transport.EventStream{
		Events: f.events,
		Errors: make(chan error),
		Close:  func() error { return nil },
	}, nil
}

func (f *fakeClient) Cleanup(_ context.Context, req transport.CleanupRequest) (transport.CleanupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupReq = req
	return transport.CleanupResponse{Closed: append([]transport.Pane(nil), f.panes...)}, nil
}
