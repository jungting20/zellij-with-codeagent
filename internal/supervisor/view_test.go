package supervisor

import (
	"context"
	"errors"
	"testing"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

func TestBuildViewCombinesRuntimeStatusAndRecentEvents(t *testing.T) {
	service := &stubRuntime{
		status: rt.InspectRuntimeResponse{
			Message: "1 managed pane(s)",
			Counts:  rt.RuntimeCounts{Managed: 1, Running: 1, Active: 1},
			Panes: []rt.Pane{{
				ID:           "pane-1",
				TaskID:       "task-1",
				Role:         rt.PaneRoleCoder,
				ZellijPaneID: "terminal_1",
				Status:       rt.PaneStatusRunning,
			}},
			Tasks: []rt.TaskPaneGroup{{TaskID: "task-1", Panes: []rt.Pane{{ID: "pane-1"}}}},
			Roles: []rt.RolePaneGroup{{Role: rt.PaneRoleCoder, Panes: []rt.Pane{{ID: "pane-1"}}}},
			Outputs: []rt.PaneOutputSummary{{
				PaneID:     "pane-1",
				TaskID:     "task-1",
				Role:       rt.PaneRoleCoder,
				Status:     rt.PaneStatusRunning,
				LastOutput: "ready\n",
			}},
		},
		events: rt.RecentEventsResponse{
			Events: []rt.EventSummary{{Type: eventbus.TypeServerReady, PaneID: "pane-1", Message: "ready"}},
		},
	}

	view, err := BuildView(context.Background(), service, ViewOptions{
		EventLimit: 5,
		EventTypes: []eventbus.EventType{
			eventbus.TypeServerReady,
		},
	})
	if err != nil {
		t.Fatalf("BuildView() error = %v", err)
	}

	if view.Message != "1 managed pane(s)" || view.Counts.Managed != 1 {
		t.Fatalf("BuildView() status = %#v, want runtime status", view)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].TaskID != "task-1" {
		t.Fatalf("BuildView() tasks = %#v, want task grouping", view.Tasks)
	}
	if len(view.Outputs) != 1 || view.Outputs[0].LastOutput != "ready\n" {
		t.Fatalf("BuildView() outputs = %#v, want output summaries", view.Outputs)
	}
	if len(view.RecentEvents) != 1 || view.RecentEvents[0].Type != eventbus.TypeServerReady {
		t.Fatalf("BuildView() events = %#v, want recent events", view.RecentEvents)
	}
	if service.recentReq.Limit != 5 || len(service.recentReq.Types) != 1 || service.recentReq.Types[0] != eventbus.TypeServerReady {
		t.Fatalf("RecentEvents() request = %#v, want view options forwarded", service.recentReq)
	}
}

func TestBuildViewPropagatesRuntimeErrors(t *testing.T) {
	wantErr := errors.New("status unavailable")
	service := &stubRuntime{statusErr: wantErr}

	_, err := BuildView(context.Background(), service, ViewOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildView() error = %v, want %v", err, wantErr)
	}
}

type stubRuntime struct {
	status    rt.InspectRuntimeResponse
	statusErr error
	events    rt.RecentEventsResponse
	eventsErr error
	recentReq rt.RecentEventsRequest
}

func (s *stubRuntime) CreatePane(context.Context, rt.CreatePaneRequest) (rt.CreatePaneResponse, error) {
	return rt.CreatePaneResponse{}, nil
}

func (s *stubRuntime) SendInput(context.Context, rt.SendInputRequest) error {
	return nil
}

func (s *stubRuntime) ListPanes(context.Context) (rt.ListPanesResponse, error) {
	return rt.ListPanesResponse{}, nil
}

func (s *stubRuntime) InspectPane(context.Context, rt.InspectPaneRequest) (rt.InspectPaneResponse, error) {
	return rt.InspectPaneResponse{}, nil
}

func (s *stubRuntime) SnapshotOutput(context.Context, rt.SnapshotOutputRequest) (rt.SnapshotOutputResponse, error) {
	return rt.SnapshotOutputResponse{}, nil
}

func (s *stubRuntime) InspectRuntime(context.Context, rt.InspectRuntimeRequest) (rt.InspectRuntimeResponse, error) {
	return s.status, s.statusErr
}

func (s *stubRuntime) RecentEvents(_ context.Context, req rt.RecentEventsRequest) (rt.RecentEventsResponse, error) {
	s.recentReq = req
	return s.events, s.eventsErr
}

func (s *stubRuntime) ClosePane(context.Context, rt.ClosePaneRequest) (rt.ClosePaneResponse, error) {
	return rt.ClosePaneResponse{}, nil
}

func (s *stubRuntime) Reconcile(context.Context, rt.ReconcileRequest) (rt.ReconcileResponse, error) {
	return rt.ReconcileResponse{}, nil
}

func (s *stubRuntime) Cleanup(context.Context, rt.CleanupRequest) (rt.CleanupResponse, error) {
	return rt.CleanupResponse{}, nil
}

func (s *stubRuntime) ApplyExecutionPlan(context.Context, rt.ApplyExecutionPlanRequest) (rt.ApplyExecutionPlanResponse, error) {
	return rt.ApplyExecutionPlanResponse{}, nil
}

func (s *stubRuntime) SubscribeEvents(context.Context) (<-chan eventbus.Event, func(), error) {
	return nil, func() {}, nil
}
