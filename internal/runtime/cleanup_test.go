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

	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-coder", TaskID: "task-1", Role: "coder"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-test", TaskID: "task-1", Role: "test"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-log", TaskID: "task-2", Role: "log"})

	response, err := service.Cleanup(context.Background(), CleanupRequest{TaskID: "task-1", Role: "test"})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	if len(response.Closed) != 1 || response.Closed[0].ID != "pane-test" {
		t.Fatalf("Cleanup() closed = %#v, want pane-test", response.Closed)
	}
	wantClose := []zellij.ClosePaneRequest{{Session: "test-session", PaneID: "terminal_test"}}
	if !reflect.DeepEqual(backend.closeRequests, wantClose) {
		t.Fatalf("backend ClosePane requests = %#v, want %#v", backend.closeRequests, wantClose)
	}
	assertPaneStatus(t, service, "pane-coder", PaneStatusStarting)
	assertPaneMissing(t, service, "pane-test")
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

	wantClose := []zellij.ClosePaneRequest{{Session: "test-session", PaneID: "terminal_bad"}, {Session: "test-session", PaneID: "terminal_good"}}
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
	assertPaneMissing(t, service, "pane-good")
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
	if !reflect.DeepEqual(backend.closeRequests, []zellij.ClosePaneRequest{{Session: "test-session", PaneID: "terminal_5"}}) {
		t.Fatalf("backend ClosePane requests = %#v, want only requested pane", backend.closeRequests)
	}
	assertPaneMissing(t, service, "pane-1")
	assertPaneStatus(t, service, "pane-2", PaneStatusStarting)
}

func TestCleanupRoutesCloseByRecordSession(t *testing.T) {
	backend := &fakeBackend{createIDs: []zellij.PaneID{"terminal_a", "terminal_b"}}
	service := newTestService(backend)
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-a", ZellijSession: "session-a"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-b", ZellijSession: "session-b"})

	if _, err := service.Cleanup(context.Background(), CleanupRequest{}); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if got := backend.closeRequests[0].Session; got != "session-a" {
		t.Fatalf("first close session = %q, want session-a", got)
	}
	if got := backend.closeRequests[1].Session; got != "session-b" {
		t.Fatalf("second close session = %q, want session-b", got)
	}
}

func TestCleanupReleasesMatchingTerminalRecord(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_5"}
	service := newTestService(backend)
	mustCreatePane(t, service, CreatePaneRequest{ID: "coder", TaskID: "task-1"})

	if _, err := service.ClosePane(context.Background(), ClosePaneRequest{PaneID: "coder"}); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}

	response, err := service.Cleanup(context.Background(), CleanupRequest{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if len(response.Skipped) != 1 || response.Skipped[0].ID != "coder" {
		t.Fatalf("Cleanup() skipped = %#v, want terminal coder", response.Skipped)
	}
	assertPaneMissing(t, service, "coder")
}

func TestCleanupTreatsSubscriptionFirstRemovalAsSkipped(t *testing.T) {
	var service *Service
	backendErr := errors.New("pane already closed")
	backend := &fakeBackend{
		createID: "terminal_5",
		beforeClosePane: func(context.Context, zellij.ClosePaneRequest) error {
			if _, err := service.registry.RemovePane("coder"); err != nil {
				t.Fatalf("subscription RemovePane() error = %v", err)
			}
			return backendErr
		},
	}
	service = newTestService(backend)
	mustCreatePane(t, service, CreatePaneRequest{ID: "coder", TaskID: "task-1"})

	response, err := service.Cleanup(context.Background(), CleanupRequest{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("Cleanup() error = %v, want subscription-first close treated as success", err)
	}
	if len(response.Skipped) != 1 || response.Skipped[0].ID != "coder" || len(response.Failed) != 0 {
		t.Fatalf("Cleanup() = %#v, want coder skipped without failures", response)
	}
	assertPaneMissing(t, service, "coder")
}
