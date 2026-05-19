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
	backend := &fakeBackend{
		createID:  "terminal_5",
		listPanes: []zellij.Pane{{ID: "terminal_5", TabID: 3, TabName: "main"}},
	}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		TaskID:  "task-1",
		AgentID: "agent-1",
		Role:    "test",
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
	if response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 3 || response.Pane.TabName != "main" {
		t.Fatalf("CreatePane() tab metadata = (%v, %q), want (3, main)", response.Pane.ZellijTabID, response.Pane.TabName)
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

func TestCreatePaneTargetsExistingTab(t *testing.T) {
	tabID := ZellijTabID(7)
	backend := &fakeBackend{
		createID:  "terminal_5",
		listPanes: []zellij.Pane{{ID: "terminal_5", TabID: 7, TabName: "tests"}},
	}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:          "pane-1",
		Role:        "test",
		Name:        "runner",
		ZellijTabID: &tabID,
		Command:     []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	want := zellij.CreatePaneRequest{
		Name:    "runner",
		TabID:   zellijTabID(7),
		Command: []string{"go", "test", "./..."},
	}
	if !reflect.DeepEqual(backend.createRequests[0], want) {
		t.Fatalf("backend CreatePane request = %#v, want %#v", backend.createRequests[0], want)
	}
	if response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 7 || response.Pane.TabName != "tests" {
		t.Fatalf("CreatePane() tab metadata = (%v, %q), want (7, tests)", response.Pane.ZellijTabID, response.Pane.TabName)
	}
}

func TestCreatePaneTargetsTabZero(t *testing.T) {
	tabID := ZellijTabID(0)
	backend := &fakeBackend{
		createID:  "terminal_5",
		listPanes: []zellij.Pane{{ID: "terminal_5", TabID: 0, TabName: "main"}},
	}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:          "pane-1",
		ZellijTabID: &tabID,
		Command:     []string{"pwd"},
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	want := zellij.CreatePaneRequest{
		TabID:   zellijTabID(0),
		Command: []string{"pwd"},
	}
	if !reflect.DeepEqual(backend.createRequests[0], want) {
		t.Fatalf("backend CreatePane request = %#v, want %#v", backend.createRequests[0], want)
	}
	if response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 0 {
		t.Fatalf("CreatePane() tab ID = %v, want 0", response.Pane.ZellijTabID)
	}
}

func TestCreatePaneCreatesNewTabAndRegistersTabMetadata(t *testing.T) {
	backend := &fakeBackend{
		createTabID: 9,
		listPanes:   []zellij.Pane{{ID: "terminal_9", TabID: 9, TabName: "agent-tests"}},
	}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		Role:    "test",
		NewTab:  true,
		TabName: "agent-tests",
		Command: []string{"go", "test", "./..."},
		CWD:     "/workspace",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}

	if len(backend.createTabRequests) != 1 {
		t.Fatalf("backend CreateTab calls = %d, want 1", len(backend.createTabRequests))
	}
	wantTabRequest := zellij.CreateTabRequest{
		Name:    "agent-tests",
		CWD:     "/workspace",
		Command: []string{"go", "test", "./..."},
	}
	if !reflect.DeepEqual(backend.createTabRequests[0], wantTabRequest) {
		t.Fatalf("backend CreateTab request = %#v, want %#v", backend.createTabRequests[0], wantTabRequest)
	}
	if len(backend.createRequests) != 0 {
		t.Fatalf("backend CreatePane calls = %d, want 0", len(backend.createRequests))
	}
	if response.Pane.ZellijPaneID != "terminal_9" || response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 9 || response.Pane.TabName != "agent-tests" {
		t.Fatalf("CreatePane() pane = %#v, want pane and tab metadata", response.Pane)
	}
}

func TestCreatePaneCleansUpNewTabWhenPaneDiscoveryFails(t *testing.T) {
	backend := &fakeBackend{
		createTabID: 9,
		listPanes:   []zellij.Pane{},
	}
	service := newTestService(backend)

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		NewTab:  true,
		TabName: "agent-tests",
		Command: []string{"go", "test", "./..."},
	})
	if !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("CreatePane() error = %v, want %v", err, ErrPaneNotFound)
	}
	if len(backend.closeTabRequests) != 1 || backend.closeTabRequests[0].TabID == nil || *backend.closeTabRequests[0].TabID != 9 {
		t.Fatalf("backend CloseTab requests = %#v, want cleanup of tab 9", backend.closeTabRequests)
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
		Role:    "coder",
		Command: []string{"bash"},
	}); err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	listCallsBefore := len(backend.listCalls)

	response, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: "pane-1"})
	if err != nil {
		t.Fatalf("InspectPane() error = %v", err)
	}

	if response.Pane.ID != "pane-1" {
		t.Fatalf("InspectPane() pane ID = %q, want pane-1", response.Pane.ID)
	}
	if response.Pane.TaskID != "task-1" || response.Pane.Role != "coder" {
		t.Fatalf("InspectPane() pane = %#v, want registry metadata", response.Pane)
	}
	if len(backend.listCalls) != listCallsBefore {
		t.Fatalf("backend ListPanes calls changed from %d to %d, want no inspect-time call", listCallsBefore, len(backend.listCalls))
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
		Role:    "coder",
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

func TestInProcessScenarioLeavesPaneOpenWhenClosePaneIsNotCalled(t *testing.T) {
	backend := &fakeBackend{
		createTabID: 9,
		listPanes:   []zellij.Pane{{ID: "terminal_9", TabID: 9, TabName: "agentd-scenario"}},
		dumpOutput:  "runtime-input-ok\n",
	}
	service := newTestService(backend)

	created, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID:      "pane-1",
		Role:    "coder",
		NewTab:  true,
		TabName: "agentd-scenario",
		Command: []string{"sh"},
		CWD:     "/workspace",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if created.Pane.ZellijPaneID != "terminal_9" || created.Pane.ZellijTabID == nil || *created.Pane.ZellijTabID != 9 {
		t.Fatalf("CreatePane() pane = %#v, want pane in created tab 9", created.Pane)
	}

	if err := service.SendInput(context.Background(), SendInputRequest{
		PaneID: created.Pane.ID,
		Text:   "printf 'runtime-input-ok\\n'\n",
	}); err != nil {
		t.Fatalf("SendInput() error = %v", err)
	}

	snapshot, err := service.SnapshotOutput(context.Background(), SnapshotOutputRequest{
		PaneID: created.Pane.ID,
		Full:   true,
	})
	if err != nil {
		t.Fatalf("SnapshotOutput() error = %v", err)
	}
	if snapshot.Output != "runtime-input-ok\n" || snapshot.Pane.LastOutput != "runtime-input-ok\n" {
		t.Fatalf("SnapshotOutput() = %#v, want captured output on open pane", snapshot)
	}

	inspected, err := service.InspectPane(context.Background(), InspectPaneRequest{PaneID: created.Pane.ID})
	if err != nil {
		t.Fatalf("InspectPane() error = %v", err)
	}
	if inspected.Pane.Status == PaneStatusClosed {
		t.Fatalf("InspectPane() status = %q, want pane to remain open", inspected.Pane.Status)
	}

	list, err := service.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("ListPanes() error = %v", err)
	}
	if len(list.Panes) != 1 || list.Panes[0].ID != created.Pane.ID {
		t.Fatalf("ListPanes() = %#v, want open scenario pane", list.Panes)
	}
	if len(backend.closeRequests) != 0 {
		t.Fatalf("backend ClosePane calls = %#v, want none", backend.closeRequests)
	}
	if len(backend.closeTabRequests) != 0 {
		t.Fatalf("backend CloseTab calls = %#v, want none", backend.closeTabRequests)
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

func zellijTabID(id zellij.TabID) *zellij.TabID {
	return &id
}

type fakeBackend struct {
	createID    zellij.PaneID
	createIDs   []zellij.PaneID
	createTabID zellij.TabID

	createErr      error
	createTabErr   error
	closeErr       error
	closeErrByPane map[zellij.PaneID]error
	closeTabErr    error
	sendErr        error
	listErr        error
	dumpErr        error

	listPanes  []zellij.Pane
	dumpOutput string

	createRequests    []zellij.CreatePaneRequest
	createTabRequests []zellij.CreateTabRequest
	closeRequests     []zellij.ClosePaneRequest
	closeTabRequests  []zellij.CloseTabRequest
	sendRequests      []zellij.SendInputRequest
	dumpRequests      []zellij.DumpScreenRequest
	listCalls         []struct{}
}

func (b *fakeBackend) CreateTab(_ context.Context, req zellij.CreateTabRequest) (zellij.TabID, error) {
	b.createTabRequests = append(b.createTabRequests, zellij.CreateTabRequest{
		Name:    req.Name,
		CWD:     req.CWD,
		Command: cloneStrings(req.Command),
	})
	if b.createTabErr != nil {
		return 0, b.createTabErr
	}
	if b.createTabID == 0 {
		return 1, nil
	}
	return b.createTabID, nil
}

func (b *fakeBackend) CloseTab(_ context.Context, req zellij.CloseTabRequest) error {
	var tabID *zellij.TabID
	if req.TabID != nil {
		clone := *req.TabID
		tabID = &clone
	}
	b.closeTabRequests = append(b.closeTabRequests, zellij.CloseTabRequest{TabID: tabID})
	return b.closeTabErr
}

func (b *fakeBackend) CreatePane(_ context.Context, req zellij.CreatePaneRequest) (zellij.PaneID, error) {
	var tabID *zellij.TabID
	if req.TabID != nil {
		clone := *req.TabID
		tabID = &clone
	}
	b.createRequests = append(b.createRequests, zellij.CreatePaneRequest{
		Name:     req.Name,
		CWD:      req.CWD,
		TabID:    tabID,
		Floating: req.Floating,
		Command:  cloneStrings(req.Command),
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
	if err := b.closeErrByPane[req.PaneID]; err != nil {
		return err
	}
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
