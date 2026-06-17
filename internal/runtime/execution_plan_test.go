package runtime

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

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
		Tabs: []ExecutionPlanTabSpec{
			{
				Name: "feature-auth",
				Panes: []ExecutionPlanPaneSpec{
					{ID: "planner", Role: "planner"},
					{ID: "frontend", Role: "react-dev"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v", err)
	}
	if response.RequestID != "req_123" || response.Session != "feature-auth" || response.Layout != "triple-horizontal" {
		t.Fatalf("ApplyExecutionPlan() metadata = %#v, want req/session/layout echoed", response)
	}
	if len(response.Tabs) != 1 || len(response.Tabs[0].Panes) != 2 {
		t.Fatalf("ApplyExecutionPlan() tabs = %d, want 1 tab with 2 panes", len(response.Tabs))
	}
	firstPane := response.Tabs[0].Panes[0]
	secondPane := response.Tabs[0].Panes[1]
	if firstPane.TaskID != "feature-auth" || firstPane.TabName != "feature-auth" {
		t.Fatalf("first pane = %#v, want task and tab name from session", firstPane)
	}
	if firstPane.ZellijTabID == nil || *firstPane.ZellijTabID != tabID {
		t.Fatalf("first pane tab = %v, want %d", firstPane.ZellijTabID, tabID)
	}
	if secondPane.ZellijTabID == nil || *secondPane.ZellijTabID != tabID {
		t.Fatalf("second pane tab = %v, want %d", secondPane.ZellijTabID, tabID)
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

func TestApplyExecutionPlanCreatesRemainingTabPanesConcurrently(t *testing.T) {
	tabID := ZellijTabID(21)
	release := make(chan struct{})
	started := make(chan string, 2)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_21a", TabID: int(tabID), TabName: "feature-auth"},
			{ID: "terminal_21b", TabID: int(tabID), TabName: "feature-auth"},
			{ID: "terminal_21c", TabID: int(tabID), TabName: "feature-auth"},
		},
		createIDs: []zellij.PaneID{"terminal_21b", "terminal_21c"},
		beforeCreatePane: func(ctx context.Context, req zellij.CreatePaneRequest, call int) error {
			started <- req.Name
			if call == 2 {
				close(release)
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	service := newTestService(backend)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	response, err := service.ApplyExecutionPlan(ctx, ApplyExecutionPlanRequest{
		RequestID: "req_123",
		Session:   "feature-auth",
		Layout:    "triple-horizontal",
		Tabs: []ExecutionPlanTabSpec{
			{
				Name: "feature-auth",
				Panes: []ExecutionPlanPaneSpec{
					{ID: "planner", Role: "planner"},
					{ID: "frontend", Role: "react-dev"},
					{ID: "console", Role: "console-tracker"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v, want remaining panes created concurrently", err)
	}
	if len(response.Tabs) != 1 || len(response.Tabs[0].Panes) != 3 {
		t.Fatalf("ApplyExecutionPlan() panes = %#v, want three panes", response.Tabs)
	}

	gotStarted := []string{<-started, <-started}
	sort.Strings(gotStarted)
	if !reflect.DeepEqual(gotStarted, []string{"console", "frontend"}) {
		t.Fatalf("concurrent CreatePane starts = %#v, want frontend and console", gotStarted)
	}
}

func TestApplyExecutionPlanRejectsInvalidLayout(t *testing.T) {
	service := newTestService(&fakeBackend{})

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "feature-auth",
		Layout:  "unknown-layout",
		Tabs:    []ExecutionPlanTabSpec{{Name: "default", Panes: []ExecutionPlanPaneSpec{{ID: "planner"}}}},
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
		Tabs: []ExecutionPlanTabSpec{
			{
				Name: "feature-auth",
				Panes: []ExecutionPlanPaneSpec{
					{ID: "planner", Role: "planner"},
				},
			},
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
		Tabs: []ExecutionPlanTabSpec{
			{
				Name: "feature-auth",
				Panes: []ExecutionPlanPaneSpec{
					{ID: "planner", Role: "planner"},
					{ID: "frontend", Role: "react-dev"},
				},
			},
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
