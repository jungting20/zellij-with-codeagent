package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"zellij-with-codeagent/internal/zellij"
)

func TestApplyExecutionPlanCreatesPanesInOneTab(t *testing.T) {
	tabID := ZellijTabID(11)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_11a", TabID: int(tabID), TabName: "feature-auth"},
			{ID: "terminal_11b", TabID: int(tabID), TabName: "feature-auth"},
		},
		createIDs: []zellij.PaneID{"terminal_11b"},
	}
	service := newTestService(backend)

	response, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		RequestID: "req_123",
		Session:   "feature-auth",
		Layout:    "triple-horizontal",
		Panes: []ExecutionPlanPaneSpec{
			{ID: "planner", Role: "planner"},
			{ID: "frontend", Role: "react-dev"},
		},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v", err)
	}
	if response.RequestID != "req_123" || response.Session != "feature-auth" || response.Layout != "triple-horizontal" {
		t.Fatalf("ApplyExecutionPlan() metadata = %#v, want req/session/layout echoed", response)
	}
	if len(response.Panes) != 2 {
		t.Fatalf("ApplyExecutionPlan() panes = %d, want 2", len(response.Panes))
	}
	if response.Panes[0].TaskID != "feature-auth" || response.Panes[0].TabName != "feature-auth" {
		t.Fatalf("first pane = %#v, want task and tab name from session", response.Panes[0])
	}
	if response.Panes[0].ZellijTabID == nil || *response.Panes[0].ZellijTabID != tabID {
		t.Fatalf("first pane tab = %v, want %d", response.Panes[0].ZellijTabID, tabID)
	}
	if response.Panes[1].ZellijTabID == nil || *response.Panes[1].ZellijTabID != tabID {
		t.Fatalf("second pane tab = %v, want %d", response.Panes[1].ZellijTabID, tabID)
	}

	if len(backend.createTabRequests) != 1 {
		t.Fatalf("CreateTab calls = %d, want 1", len(backend.createTabRequests))
	}
	if backend.createTabRequests[0].Name != "feature-auth" {
		t.Fatalf("CreateTab name = %q, want feature-auth", backend.createTabRequests[0].Name)
	}
	if len(backend.createRequests) != 1 {
		t.Fatalf("CreatePane calls = %d, want 1 after new tab", len(backend.createRequests))
	}
	wantSecond := zellij.CreatePaneRequest{
		Name:    "frontend",
		TabID:   zellijTabID(zellij.TabID(tabID)),
		Command: DefaultExecutionPlanPaneCommand("frontend"),
	}
	if !reflect.DeepEqual(backend.createRequests[0], wantSecond) {
		t.Fatalf("second CreatePane = %#v, want %#v", backend.createRequests[0], wantSecond)
	}
}

func TestApplyExecutionPlanRejectsInvalidLayout(t *testing.T) {
	service := newTestService(&fakeBackend{})

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "feature-auth",
		Layout:  "unknown-layout",
		Panes:   []ExecutionPlanPaneSpec{{ID: "planner"}},
	})
	if !errors.Is(err, ErrInvalidExecutionPlan) {
		t.Fatalf("ApplyExecutionPlan() error = %v, want %v", err, ErrInvalidExecutionPlan)
	}
}

func TestApplyExecutionPlanAllowsEmptyLayout(t *testing.T) {
	tabID := ZellijTabID(15)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_15a", TabID: int(tabID), TabName: "feature-auth"},
		},
		createIDs: []zellij.PaneID{"terminal_15a"},
	}
	service := newTestService(backend)

	response, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		RequestID: "req_empty_layout",
		Session:   "feature-auth",
		Layout:    "",
		Panes: []ExecutionPlanPaneSpec{
			{ID: "planner", Role: "planner"},
		},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() with empty layout error = %v", err)
	}
	if response.Layout != "" {
		t.Fatalf("ApplyExecutionPlan() response layout = %q, want empty", response.Layout)
	}
}

func TestApplyExecutionPlanRollsBackOnSecondPaneFailure(t *testing.T) {
	tabID := ZellijTabID(3)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes:   []zellij.Pane{{ID: "terminal_3", TabID: 3, TabName: "feature-auth"}},
		createErr:   errors.New("zellij failed"),
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "feature-auth",
		Layout:  "triple-horizontal",
		Panes: []ExecutionPlanPaneSpec{
			{ID: "planner", Role: "planner"},
			{ID: "frontend", Role: "react-dev"},
		},
	})
	if err == nil {
		t.Fatal("ApplyExecutionPlan() error = nil, want second pane failure")
	}

	list, listErr := service.ListPanes(context.Background())
	if listErr != nil {
		t.Fatalf("ListPanes() error = %v", listErr)
	}
	if len(list.Panes) != 0 {
		t.Fatalf("ListPanes() = %#v, want empty registry after rollback", list.Panes)
	}
	if len(backend.closeRequests) != 1 || backend.closeRequests[0].PaneID != "terminal_3" {
		t.Fatalf("ClosePane requests = %#v, want rollback close of first pane", backend.closeRequests)
	}
}
