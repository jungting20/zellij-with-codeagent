package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func TestInspectRuntimeGroupsPanesAndOutputSummaries(t *testing.T) {
	backend := &fakeBackend{
		createIDs: []zellij.PaneID{"terminal_1", "terminal_2"},
	}
	service := newIntrospectionTestService(backend, nil)

	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-a",
		TaskID:  "task-1",
		Role:    PaneRoleCoder,
		Command: []string{"bash"},
	}); err != nil {
		t.Fatalf("CreatePane(pane-a) error = %v", err)
	}
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:     "pane-b",
		TaskID: "task-2",
		Role:   PaneRoleTest,
	}); err != nil {
		t.Fatalf("CreatePane(pane-b) error = %v", err)
	}
	if _, err := service.registry.UpdatePaneStatus("pane-a", registry.PaneStatusRunning, "ready"); err != nil {
		t.Fatalf("UpdatePaneStatus() error = %v", err)
	}
	if _, err := service.registry.UpdatePaneOutput("pane-a", "server ready\n"); err != nil {
		t.Fatalf("UpdatePaneOutput() error = %v", err)
	}

	response, err := service.InspectRuntime(context.Background(), InspectRuntimeRequest{})
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}

	if response.Message != "2 managed pane(s)" {
		t.Fatalf("InspectRuntime() message = %q, want managed pane count", response.Message)
	}
	if response.Counts.Managed != 2 || response.Counts.Running != 1 || response.Counts.Starting != 1 || response.Counts.Active != 2 {
		t.Fatalf("InspectRuntime() counts = %#v, want managed/running/starting/active counts", response.Counts)
	}
	if len(response.Tasks) != 2 || response.Tasks[0].TaskID != "task-1" || response.Tasks[0].Panes[0].ID != "pane-a" {
		t.Fatalf("InspectRuntime() task groups = %#v, want panes grouped by task", response.Tasks)
	}
	if len(response.Roles) != 2 || response.Roles[0].Role != PaneRoleCoder || response.Roles[1].Role != PaneRoleTest {
		t.Fatalf("InspectRuntime() role groups = %#v, want panes grouped by role", response.Roles)
	}
	if len(response.Outputs) != 2 || response.Outputs[0].PaneID != "pane-a" || response.Outputs[0].LastOutput != "server ready\n" {
		t.Fatalf("InspectRuntime() outputs = %#v, want latest output summary per pane", response.Outputs)
	}
}

func TestInspectRuntimeEmptyRegistryIsUseful(t *testing.T) {
	service := newIntrospectionTestService(&fakeBackend{}, nil)

	response, err := service.InspectRuntime(context.Background(), InspectRuntimeRequest{})
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}

	if response.Message != "no managed panes" {
		t.Fatalf("InspectRuntime() message = %q, want no managed panes", response.Message)
	}
	if response.Counts.Managed != 0 || len(response.Panes) != 0 || len(response.Tasks) != 0 || len(response.Roles) != 0 {
		t.Fatalf("InspectRuntime() = %#v, want empty but useful status", response)
	}
}

func TestRecentEventsReturnsSemanticEventsInOrder(t *testing.T) {
	bus := eventbus.New()
	service := newIntrospectionTestService(&fakeBackend{}, bus)
	base := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "pane-1", Message: "rendered", Time: base})
	bus.Publish(eventbus.Event{Type: eventbus.TypeServerReady, PaneID: "pane-1", TaskID: "task-1", Message: "ready", Time: base.Add(time.Second)})
	bus.Publish(eventbus.Event{Type: eventbus.TypeTestFailed, PaneID: "pane-2", TaskID: "task-1", Message: "failed", Time: base.Add(2 * time.Second)})
	bus.Publish(eventbus.Event{Type: eventbus.TypeTestPassed, PaneID: "pane-2", TaskID: "task-1", Message: "passed", Time: base.Add(3 * time.Second)})

	response, err := service.RecentEvents(context.Background(), RecentEventsRequest{
		Limit: 2,
		Types: []eventbus.EventType{
			eventbus.TypeServerReady,
			eventbus.TypeTestFailed,
			eventbus.TypeTestPassed,
		},
	})
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}

	if len(response.Events) != 2 {
		t.Fatalf("RecentEvents() length = %d, want 2", len(response.Events))
	}
	if response.Events[0].Type != eventbus.TypeTestFailed || response.Events[1].Type != eventbus.TypeTestPassed {
		t.Fatalf("RecentEvents() = %#v, want filtered semantic events in order", response.Events)
	}
	if response.Events[0].TaskID != "task-1" || response.Events[1].PaneID != "pane-2" {
		t.Fatalf("RecentEvents() summaries = %#v, want runtime identifiers preserved", response.Events)
	}
}

func TestSnapshotOutputUnknownPaneDoesNotCallBackend(t *testing.T) {
	backend := &fakeBackend{}
	service := newIntrospectionTestService(backend, nil)

	_, err := service.SnapshotOutput(context.Background(), SnapshotOutputRequest{PaneID: "missing"})
	if !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("SnapshotOutput() error = %v, want %v", err, ErrPaneNotFound)
	}
	if len(backend.dumpRequests) != 0 {
		t.Fatalf("backend DumpScreen calls = %#v, want none", backend.dumpRequests)
	}
}

func newIntrospectionTestService(backend *fakeBackend, bus *eventbus.Bus) *Service {
	return NewService(Options{
		Registry: registry.NewWithClock(func() time.Time {
			return time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
		}),
		Backend:  backend,
		EventBus: bus,
	})
}
