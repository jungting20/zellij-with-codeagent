package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
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
	assertPaneMissing(t, service, "pane-exited")

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

func TestReconcileMultipleSessionsUsesCompositePaneIdentity(t *testing.T) {
	backend := &fakeBackend{listPanesBySession: map[string][]zellij.Pane{
		"session-a": {{ID: "terminal_1", TabID: 1}},
		"session-b": {{ID: "terminal_unmanaged", TabID: 2}},
	}}
	reg := registry.New()
	for _, req := range []registry.RegisterPaneRequest{
		{ID: "pane-a", SessionID: "session-a", ZellijPaneID: "terminal_1"},
		{ID: "pane-b", SessionID: "session-b", ZellijPaneID: "terminal_1"},
	} {
		if _, err := reg.RegisterPane(req); err != nil {
			t.Fatalf("RegisterPane(%s) error = %v", req.ID, err)
		}
	}
	service := NewService(Options{Registry: reg, Backend: backend})

	response, err := service.Reconcile(context.Background(), ReconcileRequest{})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := backend.listRequests; len(got) != 2 || got[0].Session != "session-a" || got[1].Session != "session-b" {
		t.Fatalf("ListPanes requests = %#v, want sorted session-a and session-b", got)
	}
	assertPaneStatus(t, service, "pane-a", PaneStatusRunning)
	assertPaneStatus(t, service, "pane-b", PaneStatusLost)
	if len(response.Unmanaged) != 1 || response.Unmanaged[0] != "terminal_unmanaged" {
		t.Fatalf("Reconcile() unmanaged = %#v, want terminal_unmanaged", response.Unmanaged)
	}
}

func TestReconcileMultipleSessionsListFailureLeavesAllRecordsIntact(t *testing.T) {
	backend := &fakeBackend{
		listPanesBySession: map[string][]zellij.Pane{
			"session-a": {},
		},
		listErrBySession: map[string]error{
			"session-b": errors.New("session-b list failed"),
		},
	}
	reg := registry.New()
	for _, req := range []registry.RegisterPaneRequest{
		{ID: "pane-a", SessionID: "session-a", ZellijPaneID: "terminal_a"},
		{ID: "pane-b", SessionID: "session-b", ZellijPaneID: "terminal_b"},
	} {
		if _, err := reg.RegisterPane(req); err != nil {
			t.Fatalf("RegisterPane(%s) error = %v", req.ID, err)
		}
	}
	service := NewService(Options{Registry: reg, Backend: backend})

	_, err := service.Reconcile(context.Background(), ReconcileRequest{})
	if err == nil || !strings.Contains(err.Error(), "session-b list failed") {
		t.Fatalf("Reconcile() error = %v, want session-b list failure", err)
	}
	if got := backend.listRequests; len(got) != 2 || got[0].Session != "session-a" || got[1].Session != "session-b" {
		t.Fatalf("ListPanes requests = %#v, want sorted session-a and session-b", got)
	}
	assertPaneStatus(t, service, "pane-a", PaneStatusStarting)
	assertPaneStatus(t, service, "pane-b", PaneStatusStarting)
}

func TestReconcileRecordDoesNotMutateReusedPaneGeneration(t *testing.T) {
	service := newTestService(&fakeBackend{})
	oldRecord, err := service.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "old-task",
		ZellijPaneID: "terminal_old",
	})
	if err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}
	if _, err := service.registry.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	newRecord, err := service.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "new-task",
		ZellijPaneID: "terminal_new",
	})
	if err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	_, err = service.reconcileRecord(oldRecord, map[livePaneKey]zellij.Pane{})
	if !errors.Is(err, registry.ErrStaleRecord) {
		t.Fatalf("reconcileRecord(old) error = %v, want %v", err, registry.ErrStaleRecord)
	}
	current, err := service.registry.GetPane("coder")
	if err != nil {
		t.Fatalf("GetPane(new) error = %v", err)
	}
	if current.Generation != newRecord.Generation || current.Status != registry.PaneStatusStarting || current.TaskID != "new-task" {
		t.Fatalf("current pane = %#v, want untouched new generation", current)
	}
}

func TestReconcileRecordDoesNotReturnReusedPaneFromNoMutationBranch(t *testing.T) {
	service := newTestService(&fakeBackend{})
	oldRecord, err := service.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "old-task",
		ZellijPaneID: "terminal_old",
		Status:       registry.PaneStatusRunning,
	})
	if err != nil {
		t.Fatalf("RegisterPane(old) error = %v", err)
	}
	if _, err := service.registry.RemovePane("coder"); err != nil {
		t.Fatalf("RemovePane(old) error = %v", err)
	}
	if _, err := service.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:           "coder",
		TaskID:       "new-task",
		ZellijPaneID: "terminal_new",
	}); err != nil {
		t.Fatalf("RegisterPane(new) error = %v", err)
	}

	_, err = service.reconcileRecord(oldRecord, map[livePaneKey]zellij.Pane{
		{session: oldRecord.SessionID, paneID: "terminal_old"}: {ID: "terminal_old"},
	})
	if !errors.Is(err, registry.ErrStaleRecord) {
		t.Fatalf("reconcileRecord(old running) error = %v, want %v", err, registry.ErrStaleRecord)
	}
}

func mustCreatePane(t *testing.T, service *Service, req CreatePaneRequest) Pane {
	t.Helper()
	if req.ZellijSession == "" {
		req.ZellijSession = "test-session"
	}
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

func assertPaneMissing(t *testing.T, service *Service, id PaneID) {
	t.Helper()
	_, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: id})
	if !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("InspectPane(%s) error = %v, want %v", id, err, ErrPaneNotFound)
	}
}
