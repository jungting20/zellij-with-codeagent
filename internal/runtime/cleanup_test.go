package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"zellij-with-codeagent/internal/zellij"
)

func TestCleanupClosesOnlyMatchingManagedPanes(t *testing.T) {
	backend := &fakeBackend{
		createIDs: []zellij.PaneID{"terminal_coder", "terminal_test", "terminal_log"},
	}
	service := newTestService(backend)

	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-coder", TaskID: "task-1", Role: PaneRoleCoder})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-test", TaskID: "task-1", Role: PaneRoleTest})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-log", TaskID: "task-2", Role: PaneRoleLog})

	response, err := service.Cleanup(context.Background(), CleanupRequest{TaskID: "task-1", Role: PaneRoleTest})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	if len(response.Closed) != 1 || response.Closed[0].ID != "pane-test" {
		t.Fatalf("Cleanup() closed = %#v, want pane-test", response.Closed)
	}
	wantClose := []zellij.ClosePaneRequest{{PaneID: "terminal_test"}}
	if !reflect.DeepEqual(backend.closeRequests, wantClose) {
		t.Fatalf("backend ClosePane requests = %#v, want %#v", backend.closeRequests, wantClose)
	}
	assertPaneStatus(t, service, "pane-coder", PaneStatusStarting)
	assertPaneStatus(t, service, "pane-test", PaneStatusClosed)
	assertPaneStatus(t, service, "pane-log", PaneStatusStarting)
}

func TestCleanupContinuesAfterPartialFailure(t *testing.T) {
	backend := &fakeBackend{
		createIDs: []zellij.PaneID{"terminal_bad", "terminal_good"},
		closeErrByPane: map[zellij.PaneID]error{
			"terminal_bad": errors.New("close failed"),
		},
	}
	service := newTestService(backend)

	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-bad"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-good"})

	response, err := service.Cleanup(context.Background(), CleanupRequest{})
	if !errors.Is(err, ErrCleanupPartial) {
		t.Fatalf("Cleanup() error = %v, want %v", err, ErrCleanupPartial)
	}

	wantClose := []zellij.ClosePaneRequest{{PaneID: "terminal_bad"}, {PaneID: "terminal_good"}}
	if !reflect.DeepEqual(backend.closeRequests, wantClose) {
		t.Fatalf("backend ClosePane requests = %#v, want both panes attempted", backend.closeRequests)
	}
	if len(response.Failed) != 1 || response.Failed[0].Pane.ID != "pane-bad" {
		t.Fatalf("Cleanup() failed = %#v, want pane-bad", response.Failed)
	}
	if len(response.Closed) != 1 || response.Closed[0].ID != "pane-good" {
		t.Fatalf("Cleanup() closed = %#v, want pane-good", response.Closed)
	}
	assertPaneStatus(t, service, "pane-bad", PaneStatusError)
	assertPaneStatus(t, service, "pane-good", PaneStatusClosed)
}

func TestCleanupReportsUnknownRequestedPane(t *testing.T) {
	backend := &fakeBackend{createIDs: []zellij.PaneID{"terminal_5", "terminal_7"}}
	service := newTestService(backend)
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-1"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-2"})

	response, err := service.Cleanup(context.Background(), CleanupRequest{PaneIDs: []PaneID{"pane-1", "missing"}})
	if !errors.Is(err, ErrCleanupPartial) {
		t.Fatalf("Cleanup() error = %v, want %v", err, ErrCleanupPartial)
	}
	if len(response.Closed) != 1 || response.Closed[0].ID != "pane-1" {
		t.Fatalf("Cleanup() closed = %#v, want pane-1", response.Closed)
	}
	if len(response.Failed) != 1 || response.Failed[0].Pane.ID != "missing" || response.Failed[0].Error != ErrPaneNotFound.Error() {
		t.Fatalf("Cleanup() failed = %#v, want missing pane not found", response.Failed)
	}
	if !reflect.DeepEqual(backend.closeRequests, []zellij.ClosePaneRequest{{PaneID: "terminal_5"}}) {
		t.Fatalf("backend ClosePane requests = %#v, want only requested pane", backend.closeRequests)
	}
	assertPaneStatus(t, service, "pane-2", PaneStatusStarting)
}
