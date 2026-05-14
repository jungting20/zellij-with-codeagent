package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func TestCreatePaneRegistersLogicalRecord(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_5"}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		TaskID:  "task-1",
		AgentID: "agent-1",
		Role:    PaneRoleTest,
		Name:    "tests",
		Command: []string{"go", "test", "./..."},
		CWD:     "/workspace",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	if response.Pane.ID != "pane-1" || response.Pane.ZellijPaneID != "terminal_5" {
		t.Fatalf("CreatePane() pane = %#v, want logical and zellij IDs", response.Pane)
	}
	if response.Pane.Status != PaneStatusStarting {
		t.Fatalf("CreatePane() status = %q, want %q", response.Pane.Status, PaneStatusStarting)
	}
	if len(backend.createRequests) != 1 {
		t.Fatalf("backend CreatePane calls = %d, want 1", len(backend.createRequests))
	}
	wantBackendRequest := zellij.CreatePaneRequest{
		Name:    "tests",
		CWD:     "/workspace",
		Command: []string{"go", "test", "./..."},
	}
	if !reflect.DeepEqual(backend.createRequests[0], wantBackendRequest) {
		t.Fatalf("backend CreatePane request = %#v, want %#v", backend.createRequests[0], wantBackendRequest)
	}

	list, err := service.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("ListPanes() error = %v", err)
	}
	if len(list.Panes) != 1 || list.Panes[0].ID != "pane-1" {
		t.Fatalf("ListPanes() = %#v, want created pane", list.Panes)
	}
}

func TestSendInputResolvesLogicalPaneID(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_5"}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	err := service.SendInput(context.Background(), SendInputRequest{
		PaneID: "pane-1",
		Text:   "go test ./...\n",
	})
	if err != nil {
		t.Fatalf("SendInput() error = %v", err)
	}

	want := []zellij.SendInputRequest{{
		PaneID: "terminal_5",
		Text:   "go test ./...\n",
	}}
	if !reflect.DeepEqual(backend.sendRequests, want) {
		t.Fatalf("backend SendInput requests = %#v, want %#v", backend.sendRequests, want)
	}
}

func TestSendInputUnknownPaneDoesNotCallBackend(t *testing.T) {
	backend := &fakeBackend{}
	service := newTestService(backend)

	err := service.SendInput(context.Background(), SendInputRequest{
		PaneID: "missing",
		Text:   "noop",
	})
	if !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("SendInput() error = %v, want %v", err, ErrPaneNotFound)
	}
	if len(backend.sendRequests) != 0 {
		t.Fatalf("backend SendInput calls = %d, want 0", len(backend.sendRequests))
	}
}

func TestCreatePaneBackendFailureLeavesRegistryEmpty(t *testing.T) {
	backend := &fakeBackend{createErr: errors.New("zellij failed")}
	service := newTestService(backend)

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"})
	if err == nil {
		t.Fatal("CreatePane() error = nil, want backend error")
	}

	list, err := service.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("ListPanes() error = %v", err)
	}
	if len(list.Panes) != 0 {
		t.Fatalf("ListPanes() = %#v, want empty registry", list.Panes)
	}
}

func TestCreatePaneRegistryFailureCleansUpCreatedZellijPane(t *testing.T) {
	backend := &fakeBackend{createIDs: []zellij.PaneID{"terminal_5", "terminal_7"}}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"})
	if !errors.Is(err, registry.ErrAlreadyExists) {
		t.Fatalf("CreatePane() error = %v, want %v", err, registry.ErrAlreadyExists)
	}
	if len(backend.closeRequests) != 1 || backend.closeRequests[0].PaneID != "terminal_7" {
		t.Fatalf("backend ClosePane requests = %#v, want cleanup of terminal_7", backend.closeRequests)
	}
}

func TestInspectPaneReturnsRegistryRecord(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_5"}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		TaskID:  "task-1",
		Role:    PaneRoleCoder,
		Command: []string{"bash"},
	}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	response, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: "pane-1"})
	if err != nil {
		t.Fatalf("InspectPane() error = %v", err)
	}

	if response.Pane.ID != "pane-1" {
		t.Fatalf("InspectPane() pane ID = %q, want pane-1", response.Pane.ID)
	}
	if response.Pane.TaskID != "task-1" || response.Pane.Role != PaneRoleCoder {
		t.Fatalf("InspectPane() pane = %#v, want registry metadata", response.Pane)
	}
	if len(backend.listCalls) != 0 {
		t.Fatalf("backend ListPanes calls = %d, want 0", len(backend.listCalls))
	}
}

func TestSnapshotOutputUpdatesPaneOutput(t *testing.T) {
	backend := &fakeBackend{
		createID:   "terminal_5",
		dumpOutput: "PASS\n",
	}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	response, err := service.SnapshotOutput(context.Background(), SnapshotOutputRequest{
		PaneID: "pane-1",
		Full:   true,
		ANSI:   true,
	})
	if err != nil {
		t.Fatalf("SnapshotOutput() error = %v", err)
	}

	if response.Output != "PASS\n" || response.Pane.LastOutput != "PASS\n" {
		t.Fatalf("SnapshotOutput() response = %#v, want output stored on pane", response)
	}
	want := []zellij.DumpScreenRequest{{
		PaneID: "terminal_5",
		Full:   true,
		ANSI:   true,
	}}
	if !reflect.DeepEqual(backend.dumpRequests, want) {
		t.Fatalf("backend DumpScreen requests = %#v, want %#v", backend.dumpRequests, want)
	}
}

func TestClosePaneMarksRecordClosed(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_5"}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	response, err := service.ClosePane(context.Background(), ClosePaneRequest{PaneID: "pane-1"})
	if err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}

	if response.Pane.Status != PaneStatusClosed {
		t.Fatalf("ClosePane() status = %q, want %q", response.Pane.Status, PaneStatusClosed)
	}
	want := []zellij.ClosePaneRequest{{PaneID: "terminal_5"}}
	if !reflect.DeepEqual(backend.closeRequests, want) {
		t.Fatalf("backend ClosePane requests = %#v, want %#v", backend.closeRequests, want)
	}
}

func TestClosePaneFailureMarksRecordError(t *testing.T) {
	backend := &fakeBackend{
		createID: "terminal_5",
		closeErr: errors.New("close failed"),
	}
	service := newTestService(backend)
	if _, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "pane-1"}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	response, err := service.ClosePane(context.Background(), ClosePaneRequest{PaneID: "pane-1"})
	if err == nil {
		t.Fatal("ClosePane() error = nil, want backend error")
	}
	if response.Pane.Status != PaneStatusError {
		t.Fatalf("ClosePane() status = %q, want %q", response.Pane.Status, PaneStatusError)
	}
	if response.Pane.StatusMessage != "close failed" {
		t.Fatalf("ClosePane() status message = %q, want close failed", response.Pane.StatusMessage)
	}
}

func TestInProcessHarnessExercisesRuntimeOperations(t *testing.T) {
	backend := &fakeBackend{
		createID:   "terminal_5",
		listPanes:  []zellij.Pane{{ID: "terminal_5", Title: "coder"}},
		dumpOutput: "ready\n",
	}
	service := newTestService(backend)

	created, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		Role:    PaneRoleCoder,
		Command: []string{"bash"},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if err := service.SendInput(context.Background(), SendInputRequest{PaneID: created.Pane.ID, Text: "echo ready\n"}); err != nil {
		t.Fatalf("SendInput() error = %v", err)
	}
	if _, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: created.Pane.ID}); err != nil {
		t.Fatalf("InspectPane() error = %v", err)
	}
	if _, err := service.SnapshotOutput(context.Background(), SnapshotOutputRequest{PaneID: created.Pane.ID}); err != nil {
		t.Fatalf("SnapshotOutput() error = %v", err)
	}
	if _, err := service.ClosePane(context.Background(), ClosePaneRequest{PaneID: created.Pane.ID}); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
}

func newTestService(backend *fakeBackend) *Service {
	return NewService(Options{
		Registry: registry.NewWithClock(func() time.Time {
			return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
		}),
		Backend: backend,
	})
}

type fakeBackend struct {
	createID  zellij.PaneID
	createIDs []zellij.PaneID

	createErr error
	closeErr  error
	sendErr   error
	listErr   error
	dumpErr   error

	listPanes  []zellij.Pane
	dumpOutput string

	createRequests []zellij.CreatePaneRequest
	closeRequests  []zellij.ClosePaneRequest
	sendRequests   []zellij.SendInputRequest
	dumpRequests   []zellij.DumpScreenRequest
	listCalls      []struct{}
}

func (b *fakeBackend) CreatePane(_ context.Context, req zellij.CreatePaneRequest) (zellij.PaneID, error) {
	b.createRequests = append(b.createRequests, zellij.CreatePaneRequest{
		Name:    req.Name,
		CWD:     req.CWD,
		Command: cloneStrings(req.Command),
	})
	if b.createErr != nil {
		return "", b.createErr
	}
	if len(b.createIDs) > 0 {
		id := b.createIDs[0]
		b.createIDs = b.createIDs[1:]
		return id, nil
	}
	if b.createID == "" {
		return "terminal_1", nil
	}
	return b.createID, nil
}

func (b *fakeBackend) ClosePane(_ context.Context, req zellij.ClosePaneRequest) error {
	b.closeRequests = append(b.closeRequests, req)
	return b.closeErr
}

func (b *fakeBackend) SendInput(_ context.Context, req zellij.SendInputRequest) error {
	b.sendRequests = append(b.sendRequests, req)
	return b.sendErr
}

func (b *fakeBackend) ListPanes(context.Context) ([]zellij.Pane, error) {
	b.listCalls = append(b.listCalls, struct{}{})
	if b.listErr != nil {
		return nil, b.listErr
	}

	panes := make([]zellij.Pane, len(b.listPanes))
	copy(panes, b.listPanes)
	return panes, nil
}

func (b *fakeBackend) DumpScreen(_ context.Context, req zellij.DumpScreenRequest) (string, error) {
	b.dumpRequests = append(b.dumpRequests, req)
	if b.dumpErr != nil {
		return "", b.dumpErr
	}
	return b.dumpOutput, nil
}

func (b *fakeBackend) SubscribeCommand(req zellij.SubscribeRequest) (zellij.CommandSpec, error) {
	return zellij.CommandSpec{
		Name: "zellij",
		Args: []string{"subscribe", "--pane-id", string(req.PaneID)},
	}, nil
}
