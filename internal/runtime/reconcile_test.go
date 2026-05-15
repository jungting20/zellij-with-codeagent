package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/zellij"
)

func TestReconcileUpdatesManagedPaneLifecycleAndReportsUnmanaged(t *testing.T) {
	backend := &fakeBackend{
		createIDs: []zellij.PaneID{"terminal_live", "terminal_missing", "terminal_exited"},
		listPanes: []zellij.Pane{
			{ID: "terminal_live", TabID: 1, TabName: "managed"},
			{ID: "terminal_exited", TabID: 1, TabName: "managed", Exited: true},
			{ID: "terminal_unmanaged", TabID: 1, TabName: "managed"},
		},
	}
	service := newTestService(backend)

	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-live"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-missing"})
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-exited"})

	response, err := service.Reconcile(context.Background(), ReconcileRequest{})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertPaneStatus(t, service, "pane-live", PaneStatusRunning)
	assertPaneStatus(t, service, "pane-missing", PaneStatusLost)
	assertPaneStatus(t, service, "pane-exited", PaneStatusExited)

	if len(response.Active) != 1 || response.Active[0].ID != "pane-live" {
		t.Fatalf("Reconcile() active = %#v, want pane-live", response.Active)
	}
	if len(response.Lost) != 1 || response.Lost[0].ID != "pane-missing" {
		t.Fatalf("Reconcile() lost = %#v, want pane-missing", response.Lost)
	}
	if len(response.Exited) != 1 || response.Exited[0].ID != "pane-exited" {
		t.Fatalf("Reconcile() exited = %#v, want pane-exited", response.Exited)
	}
	if len(response.Unmanaged) != 1 || response.Unmanaged[0] != "terminal_unmanaged" {
		t.Fatalf("Reconcile() unmanaged = %#v, want terminal_unmanaged", response.Unmanaged)
	}
}

func TestReconcileListFailureLeavesRegistryIntactAndPublishesHealth(t *testing.T) {
	backend := &fakeBackend{
		createID: "terminal_5",
		listErr:  errors.New("list failed"),
	}
	service := newTestService(backend)
	mustCreatePane(t, service, CreatePaneRequest{ID: "pane-1"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, unsub, err := service.SubscribeEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeEvents() error = %v", err)
	}
	defer unsub()

	_, err = service.Reconcile(context.Background(), ReconcileRequest{})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want list failure")
	}

	assertPaneStatus(t, service, "pane-1", PaneStatusStarting)

	select {
	case ev := <-events:
		if ev.Type != eventbus.TypeHealthChanged || !strings.Contains(ev.Message, "reconcile failed") {
			t.Fatalf("event = %#v, want reconcile health failure", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconcile health event")
	}
}

func mustCreatePane(t *testing.T, service *Service, req CreatePaneRequest) Pane {
	t.Helper()
	response, err := service.CreatePane(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePane(%s) error = %v", req.ID, err)
	}
	return response.Pane
}

func assertPaneStatus(t *testing.T, service *Service, id PaneID, want PaneStatus) {
	t.Helper()
	response, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: id})
	if err != nil {
		t.Fatalf("InspectPane(%s) error = %v", id, err)
	}
	if response.Pane.Status != want {
		t.Fatalf("InspectPane(%s) status = %q, want %q", id, response.Pane.Status, want)
	}
}
