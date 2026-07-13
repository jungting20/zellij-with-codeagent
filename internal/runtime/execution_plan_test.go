package runtime

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
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

func TestApplyExecutionPlanAllowsArbitraryLayoutMetadata(t *testing.T) {
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
		RequestID: "req_custom_layout",
		Session:   "feature-auth",
		Layout:    "custom-grid",
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
		t.Fatalf("ApplyExecutionPlan() with arbitrary layout error = %v", err)
	}
	if response.Layout != "custom-grid" {
		t.Fatalf("ApplyExecutionPlan() response layout = %q, want custom-grid", response.Layout)
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

func TestApplyExecutionPlanSendsInitialInputForFirstAndRemainingPanes(t *testing.T) {
	tabID := ZellijTabID(31)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_31a", TabID: int(tabID), TabName: "goal-prefill"},
			{ID: "terminal_31b", TabID: int(tabID), TabName: "goal-prefill"},
		},
		createIDs: []zellij.PaneID{"terminal_31b"},
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "goal-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name: "goal-prefill",
			Panes: []ExecutionPlanPaneSpec{
				{ID: "coder", InitialInput: "fix the parser"},
				{ID: "notes", InitialInput: "review these notes"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v", err)
	}

	got := append([]zellij.SendInputRequest(nil), backend.sendRequests...)
	sort.Slice(got, func(i, j int) bool { return got[i].PaneID < got[j].PaneID })
	want := []zellij.SendInputRequest{
		{PaneID: "terminal_31a", Text: "fix the parser"},
		{PaneID: "terminal_31b", Text: "review these notes"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SendInput requests = %#v, want %#v", got, want)
	}
}

func TestApplyExecutionPlanSkipsEmptyInitialInput(t *testing.T) {
	tabID := ZellijTabID(32)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_32a", TabID: int(tabID), TabName: "empty-prefill"},
		},
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "empty-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name:  "empty-prefill",
			Panes: []ExecutionPlanPaneSpec{{ID: "coder"}},
		}},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v", err)
	}
	if len(backend.sendRequests) != 0 {
		t.Fatalf("SendInput requests = %#v, want none", backend.sendRequests)
	}
}

func TestApplyExecutionPlanRollsBackOnInitialInputFailure(t *testing.T) {
	tabID := ZellijTabID(33)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_33a", TabID: int(tabID), TabName: "failed-prefill"},
		},
		sendErr: errors.New("paste failed"),
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "failed-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name: "failed-prefill",
			Panes: []ExecutionPlanPaneSpec{{
				ID:           "coder",
				InitialInput: "fix the parser",
			}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `send initial input to pane "coder"`) {
		t.Fatalf("ApplyExecutionPlan() error = %v, want pane-specific error", err)
	}

	list, listErr := service.ListPanes(context.Background())
	if listErr != nil {
		t.Fatalf("ListPanes() error = %v", listErr)
	}
	if len(list.Panes) != 0 {
		t.Fatalf("ListPanes() = %#v, want empty registry", list.Panes)
	}
	if len(backend.closeRequests) != 1 || backend.closeRequests[0].PaneID != "terminal_33a" {
		t.Fatalf("ClosePane requests = %#v, want created pane rollback", backend.closeRequests)
	}
}

func TestApplyExecutionPlanRollsBackAllPanesOnRemainingInitialInputFailure(t *testing.T) {
	tabID := ZellijTabID(34)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_34a", TabID: int(tabID), TabName: "failed-remaining-prefill"},
			{ID: "terminal_34b", TabID: int(tabID), TabName: "failed-remaining-prefill"},
			{ID: "terminal_34c", TabID: int(tabID), TabName: "failed-remaining-prefill"},
		},
		createIDs: []zellij.PaneID{"terminal_34b", "terminal_34c"},
		sendErr:   errors.New("paste failed"),
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "failed-remaining-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name: "failed-remaining-prefill",
			Panes: []ExecutionPlanPaneSpec{
				{ID: "coder"},
				{ID: "review", InitialInput: "review the parser"},
				{ID: "notes"},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `send initial input to pane "review"`) {
		t.Fatalf("ApplyExecutionPlan() error = %v, want remaining pane input error", err)
	}

	list, listErr := service.ListPanes(context.Background())
	if listErr != nil {
		t.Fatalf("ListPanes() error = %v", listErr)
	}
	if len(list.Panes) != 0 {
		t.Fatalf("ListPanes() = %#v, want empty registry", list.Panes)
	}

	gotClosed := make([]string, 0, len(backend.closeRequests))
	for _, req := range backend.closeRequests {
		gotClosed = append(gotClosed, string(req.PaneID))
	}
	sort.Strings(gotClosed)
	wantClosed := []string{"terminal_34a", "terminal_34b", "terminal_34c"}
	if !reflect.DeepEqual(gotClosed, wantClosed) {
		t.Fatalf("closed panes = %#v, want all created panes %#v", gotClosed, wantClosed)
	}
}

func TestApplyExecutionPlanWaitsForInitialInputReadyText(t *testing.T) {
	tabID := ZellijTabID(35)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_35a", TabID: int(tabID), TabName: "ready-prefill"},
		},
		dumpOutputs: []string{"starting", "OpenAI Codex\n›"},
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "ready-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name: "ready-prefill",
			Panes: []ExecutionPlanPaneSpec{{
				ID:                    "coder",
				InitialInput:          "fix the parser",
				InitialInputReadyText: "›",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v", err)
	}
	if len(backend.dumpRequests) != 2 {
		t.Fatalf("DumpScreen requests = %d, want poll until second snapshot", len(backend.dumpRequests))
	}
	wantSend := []zellij.SendInputRequest{{PaneID: "terminal_35a", Text: "fix the parser"}}
	if !reflect.DeepEqual(backend.sendRequests, wantSend) {
		t.Fatalf("SendInput requests = %#v, want %#v after readiness", backend.sendRequests, wantSend)
	}
}

func TestApplyExecutionPlanRetriesSnapshotErrorsUntilInitialInputReady(t *testing.T) {
	tabID := ZellijTabID(36)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_36a", TabID: int(tabID), TabName: "retry-ready-prefill"},
		},
		dumpOutputs: []string{"", "OpenAI Codex\n›"},
		dumpErrors:  []error{errors.New("screen not ready"), nil},
	}
	service := newTestService(backend)

	_, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
		Session: "retry-ready-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name: "retry-ready-prefill",
			Panes: []ExecutionPlanPaneSpec{{
				ID:                    "coder",
				InitialInput:          "fix the parser",
				InitialInputReadyText: "›",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("ApplyExecutionPlan() error = %v", err)
	}
	if len(backend.dumpRequests) != 2 || len(backend.sendRequests) != 1 {
		t.Fatalf("dump/send requests = %d/%d, want retry then one send", len(backend.dumpRequests), len(backend.sendRequests))
	}
}

func TestApplyExecutionPlanReadinessTimeoutRollsBackWithFreshContext(t *testing.T) {
	tabID := ZellijTabID(37)
	backend := &fakeBackend{
		createTabID: zellij.TabID(tabID),
		listPanes: []zellij.Pane{
			{ID: "terminal_37a", TabID: int(tabID), TabName: "timeout-prefill"},
		},
		dumpOutput: "starting",
	}
	service := newTestService(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := service.ApplyExecutionPlan(ctx, ApplyExecutionPlanRequest{
		Session: "timeout-prefill",
		Tabs: []ExecutionPlanTabSpec{{
			Name: "timeout-prefill",
			Panes: []ExecutionPlanPaneSpec{{
				ID:                    "coder",
				InitialInput:          "fix the parser",
				InitialInputReadyText: "›",
			}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `wait for initial input readiness in pane "coder"`) {
		t.Fatalf("ApplyExecutionPlan() error = %v, want readiness timeout", err)
	}
	if len(backend.closeRequests) != 1 {
		t.Fatalf("ClosePane requests = %#v, want timeout rollback", backend.closeRequests)
	}
	if len(backend.closeContextErrs) != 1 || backend.closeContextErrs[0] != nil {
		t.Fatalf("ClosePane context errors = %#v, want fresh rollback context", backend.closeContextErrs)
	}
	list, listErr := service.ListPanes(context.Background())
	if listErr != nil {
		t.Fatalf("ListPanes() error = %v", listErr)
	}
	if len(list.Panes) != 0 {
		t.Fatalf("ListPanes() = %#v, want empty registry after timeout rollback", list.Panes)
	}
}
