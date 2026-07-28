package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/runtime"
)

func TestServiceStartAgentCreatesRegisteredMonitoredPane(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 9, 30, 0, 0, time.UTC)
	store := NewMemoryStore(func() time.Time { return now })
	events := make([]string, 0, 3)
	monitor := &serviceFakeMonitor{
		startFn: func(record Record) error {
			events = append(events, "monitor")
			stored, err := store.Get(record.ID)
			if err != nil {
				t.Fatalf("record must exist before Monitor.Start: %v", err)
			}
			if !reflect.DeepEqual(stored, record) {
				t.Fatalf("stored record = %#v, want %#v", stored, record)
			}
			return nil
		},
	}
	cwd := t.TempDir()
	runtimeService := &serviceFakeRuntime{
		createFn: func(_ context.Context, request runtime.CreatePaneRequest) (runtime.CreatePaneResponse, error) {
			events = append(events, "runtime")
			if _, err := store.Get("agent-1"); err != nil {
				t.Fatalf("record must exist before CreatePane: %v", err)
			}
			want := runtime.CreatePaneRequest{
				ID:                    "agent-1",
				AgentID:               "agent-1",
				Role:                  "coding-agent",
				Name:                  "gemini-agent-1",
				ZellijSession:         "physical-a",
				SameTabAsZellijPaneID: "terminal_2",
				Command:               []string{"agy", "--dangerously-skip-permissions", "--model", "gemini-3"},
				CWD:                   cwd,
			}
			if !reflect.DeepEqual(request, want) {
				t.Fatalf("CreatePane request = %#v, want %#v", request, want)
			}
			return runtime.CreatePaneResponse{Pane: runtime.Pane{
				ID:           request.ID,
				AgentID:      request.AgentID,
				Role:         request.Role,
				Command:      append([]string(nil), request.Command...),
				CWD:          request.CWD,
				ZellijPaneID: "terminal_9",
			}}, nil
		},
	}
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            store,
		LifecycleMonitor: monitor,
		Now:              func() time.Time { return now },
		NewAgentID:       func() ID { return "agent-1" },
	})

	response, err := service.StartAgent(ctx, StartAgentRequest{
		Kind:                KindGemini,
		CWD:                 cwd,
		ExtraArgs:           []string{"--model", "gemini-3"},
		SourceZellijSession: "physical-a",
		SourceZellijPaneID:  "terminal_2",
	})
	if err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}

	wantRecord := Record{
		ID:             "agent-1",
		Kind:           KindGemini,
		PaneID:         "agent-1",
		State:          StateUnknown,
		CreatedAt:      now,
		StateChangedAt: now,
	}
	if !reflect.DeepEqual(response.Agent.Agent, wantRecord) {
		t.Errorf("response record = %#v, want %#v", response.Agent.Agent, wantRecord)
	}
	if response.Agent.Pane.ID != "agent-1" || response.Agent.Pane.ZellijPaneID != "terminal_9" {
		t.Errorf("response pane = %#v", response.Agent.Pane)
	}
	if !reflect.DeepEqual(events, []string{"monitor", "runtime"}) {
		t.Errorf("side-effect order = %v, want [monitor runtime] after store registration", events)
	}
	if len(monitor.started) != 1 {
		t.Fatalf("Monitor.Start call count = %d, want 1", len(monitor.started))
	}
}

func TestServiceStartAgentRejectsInvalidRequestsBeforeRegistration(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")

	tests := []struct {
		name    string
		request StartAgentRequest
	}{
		{name: "unknown kind", request: validStartRequest(t, Kind("other"))},
		{name: "blank cwd", request: withStartCWD(validStartRequest(t, KindCodex), "   ")},
		{name: "inaccessible cwd", request: withStartCWD(validStartRequest(t, KindCodex), missingPath)},
		{name: "cwd is file", request: withStartCWD(validStartRequest(t, KindCodex), filePath)},
		{name: "missing source session", request: withStartSession(validStartRequest(t, KindCodex), "")},
		{name: "missing source pane", request: withStartPane(validStartRequest(t, KindCodex), "")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			runtimeService := &serviceFakeRuntime{}
			monitor := &serviceFakeMonitor{}
			service := NewService(ServiceOptions{
				RuntimeService:   runtimeService,
				Store:            store,
				LifecycleMonitor: monitor,
				NewAgentID:       func() ID { return "agent-1" },
			})

			if _, err := service.StartAgent(context.Background(), test.request); err == nil {
				t.Fatal("StartAgent() error = nil, want validation error")
			}
			records, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 0 || len(monitor.started) != 0 || len(runtimeService.created) != 0 {
				t.Fatalf("invalid request caused side effects: records=%d monitor=%d runtime=%d", len(records), len(monitor.started), len(runtimeService.created))
			}
		})
	}
}

func TestServiceStartAgentRejectsDuplicateGeneratedID(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore(nil)
	_, err := store.Create(Record{ID: "agent-1", Kind: KindClaude, PaneID: "agent-1", State: StateUnknown, CreatedAt: now, StateChangedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	monitor := &serviceFakeMonitor{}
	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: monitor, NewAgentID: func() ID { return "agent-1" }})

	_, err = service.StartAgent(context.Background(), validStartRequest(t, KindCodex))
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("StartAgent() error = %v, want %v", err, ErrDuplicateID)
	}
	if len(monitor.started) != 0 || len(runtimeService.created) != 0 {
		t.Fatalf("duplicate ID started dependencies: monitor=%d runtime=%d", len(monitor.started), len(runtimeService.created))
	}
}

func TestServiceStartAgentManifestOrMonitorFailureDeletesRecordWithoutPane(t *testing.T) {
	for _, name := range []string{"manifest load failure", "monitor start failure"} {
		t.Run(name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			monitorErr := errors.New(name)
			monitor := &serviceFakeMonitor{startErr: monitorErr}
			runtimeService := &serviceFakeRuntime{}
			service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: monitor, NewAgentID: func() ID { return "agent-1" }})

			_, err := service.StartAgent(context.Background(), validStartRequest(t, KindClaude))
			if !errors.Is(err, monitorErr) {
				t.Fatalf("StartAgent() error = %v, want %v", err, monitorErr)
			}
			if _, err := store.Get("agent-1"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("store.Get() error = %v, want %v", err, ErrNotFound)
			}
			if len(runtimeService.created) != 0 {
				t.Fatalf("CreatePane call count = %d, want 0", len(runtimeService.created))
			}
			if len(monitor.stopped) != 0 {
				t.Fatalf("Monitor.Stop call count = %d, want 0", len(monitor.stopped))
			}
		})
	}
}

func TestServiceStartAgentRuntimeFailureStopsMonitorAndDeletesRecord(t *testing.T) {
	store := NewMemoryStore(nil)
	runtimeErr := errors.New("create pane failed")
	monitor := &serviceFakeMonitor{}
	runtimeService := &serviceFakeRuntime{createErr: runtimeErr}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: monitor, NewAgentID: func() ID { return "agent-1" }})

	_, err := service.StartAgent(context.Background(), validStartRequest(t, KindCursor))
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("StartAgent() error = %v, want %v", err, runtimeErr)
	}
	if _, err := store.Get("agent-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("store.Get() error = %v, want %v", err, ErrNotFound)
	}
	if !reflect.DeepEqual(monitor.stopped, []ID{"agent-1"}) {
		t.Fatalf("Monitor.Stop calls = %v, want [agent-1]", monitor.stopped)
	}
}

func TestServiceStartAgentProvisioningRecordIsNotRemovedByConcurrentList(t *testing.T) {
	store := NewMemoryStore(nil)
	monitor := &serviceFakeMonitor{}
	createEntered := make(chan struct{})
	releaseCreate := make(chan struct{})
	listEntered := make(chan struct{})
	releaseList := make(chan struct{})
	runtimeService := &serviceFakeRuntime{
		createFn: func(_ context.Context, request runtime.CreatePaneRequest) (runtime.CreatePaneResponse, error) {
			close(createEntered)
			<-releaseCreate
			return runtime.CreatePaneResponse{Pane: runtime.Pane{ID: request.ID, Status: runtime.PaneStatusRunning}}, nil
		},
		listFn: func(context.Context) (runtime.ListPanesResponse, error) {
			close(listEntered)
			<-releaseList
			return runtime.ListPanesResponse{}, nil
		},
	}
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            store,
		LifecycleMonitor: monitor,
		NewAgentID:       func() ID { return "agent-1" },
	})
	startRequest := validStartRequest(t, KindCodex)
	startResult := make(chan error, 1)
	go func() {
		_, err := service.StartAgent(context.Background(), startRequest)
		startResult <- err
	}()
	<-createEntered

	type listAgentResult struct {
		response ListAgentsResponse
		err      error
	}
	listResult := make(chan listAgentResult, 1)
	go func() {
		response, err := service.ListAgents(context.Background())
		listResult <- listAgentResult{response: response, err: err}
	}()
	<-listEntered
	close(releaseCreate)
	if err := <-startResult; err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}
	close(releaseList)
	listed := <-listResult
	response, err := listed.response, listed.err
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(response.Agents) != 0 {
		t.Fatalf("ListAgents() provisioning rows = %d, want 0", len(response.Agents))
	}
	if _, err := store.Get("agent-1"); err != nil {
		t.Fatalf("provisioning record removed: %v", err)
	}
	if len(monitor.stopped) != 0 {
		t.Fatalf("Monitor.Stop calls during provisioning = %v", monitor.stopped)
	}
	if _, err := store.Get("agent-1"); err != nil {
		t.Fatalf("successful StartAgent record missing: %v", err)
	}
}

func TestServiceListAgentStaleSnapshotDoesNotDeleteReusedID(t *testing.T) {
	base := time.Date(2026, 7, 28, 11, 0, 0, 0, time.UTC)
	store := NewMemoryStore(nil)
	old := Record{ID: "agent-1", Kind: KindCodex, PaneID: "agent-1", State: StateUnknown, CreatedAt: base, StateChangedAt: base}
	if _, err := store.Create(old); err != nil {
		t.Fatal(err)
	}
	listEntered := make(chan struct{})
	releaseList := make(chan struct{})
	runtimeService := &serviceFakeRuntime{
		listFn: func(context.Context) (runtime.ListPanesResponse, error) {
			close(listEntered)
			<-releaseList
			return runtime.ListPanesResponse{}, nil
		},
		createFn: func(_ context.Context, request runtime.CreatePaneRequest) (runtime.CreatePaneResponse, error) {
			return runtime.CreatePaneResponse{Pane: runtime.Pane{ID: request.ID, Status: runtime.PaneStatusRunning}}, nil
		},
	}
	monitor := &serviceFakeMonitor{}
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            store,
		LifecycleMonitor: monitor,
		Now:              func() time.Time { return base.Add(time.Second) },
		NewAgentID:       func() ID { return "agent-1" },
	})
	listResult := make(chan error, 1)
	go func() {
		_, err := service.ListAgents(context.Background())
		listResult <- err
	}()
	<-listEntered

	if err := store.Delete(old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartAgent(context.Background(), validStartRequest(t, KindClaude)); err != nil {
		t.Fatalf("reused StartAgent() error = %v", err)
	}
	close(releaseList)
	if err := <-listResult; err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}

	current, err := store.Get("agent-1")
	if err != nil {
		t.Fatalf("reused record removed by stale cleanup: %v", err)
	}
	if current.Kind != KindClaude || !current.CreatedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("current record = %#v, want reused Claude record", current)
	}
	if len(monitor.stopped) != 0 {
		t.Fatalf("stale cleanup stopped reused monitor: %v", monitor.stopped)
	}
}

func TestServiceStartAgentPartialRuntimeCleanupPolicy(t *testing.T) {
	createCause := errors.New("pane initialization failed")
	createErr := errors.Join(createCause, runtime.ErrCleanupPartial)

	tests := []struct {
		name          string
		cleanup       runtime.CleanupResponse
		cleanupErr    error
		confirm       runtime.ListPanesResponse
		confirmErr    error
		wantPreserved bool
		wantDiag      error
	}{
		{
			name:    "successful retry and absent confirmation removes record",
			cleanup: runtime.CleanupResponse{Closed: []runtime.Pane{{ID: "agent-1"}}},
		},
		{
			name:          "failed retry even with absent logical pane preserves tracking",
			cleanup:       runtime.CleanupResponse{Failed: []runtime.CleanupFailure{{Pane: runtime.Pane{ID: "agent-1"}, Error: "still open"}}},
			cleanupErr:    runtime.ErrCleanupPartial,
			wantPreserved: true,
			wantDiag:      runtime.ErrCleanupPartial,
		},
		{
			name:          "remaining pane after nominal retry preserves tracking",
			cleanup:       runtime.CleanupResponse{Closed: []runtime.Pane{{ID: "agent-1"}}},
			confirm:       runtime.ListPanesResponse{Panes: []runtime.Pane{{ID: "agent-1", Status: runtime.PaneStatusError}}},
			wantPreserved: true,
			wantDiag:      runtime.ErrCleanupPartial,
		},
		{
			name:          "confirmation failure preserves tracking",
			cleanup:       runtime.CleanupResponse{Closed: []runtime.Pane{{ID: "agent-1"}}},
			confirmErr:    errors.New("runtime list unavailable"),
			wantPreserved: true,
			wantDiag:      errors.New("runtime list unavailable"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			monitor := &serviceFakeMonitor{}
			listCalls := 0
			runtimeService := &serviceFakeRuntime{
				createErr: createErr,
				cleanupFn: func(ctx context.Context, request runtime.CleanupRequest) (runtime.CleanupResponse, error) {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 6*time.Second {
						t.Fatalf("Cleanup context deadline = %v, want bounded future deadline", deadline)
					}
					if !reflect.DeepEqual(request, runtime.CleanupRequest{PaneIDs: []runtime.PaneID{"agent-1"}}) {
						t.Fatalf("Cleanup request = %#v", request)
					}
					return test.cleanup, test.cleanupErr
				},
				listFn: func(context.Context) (runtime.ListPanesResponse, error) {
					listCalls++
					if listCalls == 1 {
						return test.confirm, test.confirmErr
					}
					return runtime.ListPanesResponse{}, nil
				},
			}
			service := NewService(ServiceOptions{
				RuntimeService:   runtimeService,
				Store:            store,
				LifecycleMonitor: monitor,
				NewAgentID:       func() ID { return "agent-1" },
			})

			_, err := service.StartAgent(context.Background(), validStartRequest(t, KindCursor))
			if !errors.Is(err, createCause) || !errors.Is(err, runtime.ErrCleanupPartial) {
				t.Fatalf("StartAgent() error = %v, want original cause and ErrCleanupPartial", err)
			}
			if test.wantDiag != nil && !strings.Contains(err.Error(), test.wantDiag.Error()) {
				t.Fatalf("StartAgent() error = %v, want diagnostic %q", err, test.wantDiag)
			}
			if len(runtimeService.cleaned) != 1 {
				t.Fatalf("Cleanup call count = %d, want 1", len(runtimeService.cleaned))
			}
			_, getErr := store.Get("agent-1")
			if test.wantPreserved {
				if getErr != nil {
					t.Fatalf("record not preserved: %v", getErr)
				}
				if len(monitor.stopped) != 0 {
					t.Fatalf("preserved monitor stopped: %v", monitor.stopped)
				}
				response, listErr := service.ListAgents(context.Background())
				if listErr != nil {
					t.Fatalf("ListAgents() after uncertain cleanup error = %v", listErr)
				}
				if len(response.Agents) != 0 {
					t.Fatalf("uncertain missing pane rows = %d, want 0", len(response.Agents))
				}
				if _, getErr := store.Get("agent-1"); getErr != nil {
					t.Fatalf("ListAgents removed uncertain record: %v", getErr)
				}
			} else {
				if !errors.Is(getErr, ErrNotFound) {
					t.Fatalf("store.Get() error = %v, want %v", getErr, ErrNotFound)
				}
				if !reflect.DeepEqual(monitor.stopped, []ID{"agent-1"}) {
					t.Fatalf("Monitor.Stop calls = %v, want [agent-1]", monitor.stopped)
				}
			}
		})
	}
}

func TestServiceListAgentsJoinsRuntimePanesInCreationOrderAndRemovesOrphans(t *testing.T) {
	store := NewMemoryStore(nil)
	base := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	for _, record := range []Record{
		{ID: "agent-2", Kind: KindClaude, PaneID: "pane-2", State: StateWorking, CreatedAt: base.Add(time.Second), StateChangedAt: base.Add(time.Second)},
		{ID: "agent-orphan", Kind: KindCursor, PaneID: "pane-missing", State: StateUnknown, CreatedAt: base.Add(2 * time.Second), StateChangedAt: base.Add(2 * time.Second)},
		{ID: "agent-1", Kind: KindCodex, PaneID: "pane-1", State: StateIdle, CreatedAt: base, StateChangedAt: base},
	} {
		if _, err := store.Create(record); err != nil {
			t.Fatal(err)
		}
	}
	monitor := &serviceFakeMonitor{}
	runtimeService := &serviceFakeRuntime{listResponse: runtime.ListPanesResponse{Panes: []runtime.Pane{
		{ID: "unrelated", Role: "planner"},
		{ID: "pane-2", AgentID: "agent-2", Role: "coding-agent", ZellijPaneID: "terminal_2"},
		{ID: "pane-1", AgentID: "agent-1", Role: "coding-agent", ZellijPaneID: "terminal_1"},
	}}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: monitor})

	response, err := service.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(response.Agents) != 2 {
		t.Fatalf("ListAgents() count = %d, want 2", len(response.Agents))
	}
	if got := []ID{response.Agents[0].Agent.ID, response.Agents[1].Agent.ID}; !reflect.DeepEqual(got, []ID{"agent-1", "agent-2"}) {
		t.Fatalf("agent order = %v, want [agent-1 agent-2]", got)
	}
	if response.Agents[0].Pane.ZellijPaneID != "terminal_1" || response.Agents[1].Pane.ZellijPaneID != "terminal_2" {
		t.Errorf("joined panes = %#v", response.Agents)
	}
	if _, err := store.Get("agent-orphan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan store.Get() error = %v, want %v", err, ErrNotFound)
	}
	if !reflect.DeepEqual(monitor.stopped, []ID{"agent-orphan"}) {
		t.Fatalf("Monitor.Stop calls = %v, want [agent-orphan]", monitor.stopped)
	}
}

func TestServiceListAgentsReturnsRuntimeFailureWithoutDeletingRecords(t *testing.T) {
	store := NewMemoryStore(nil)
	now := time.Now()
	_, _ = store.Create(Record{ID: "agent-1", Kind: KindCodex, PaneID: "pane-1", State: StateUnknown, CreatedAt: now, StateChangedAt: now})
	runtimeErr := errors.New("list failed")
	monitor := &serviceFakeMonitor{}
	service := NewService(ServiceOptions{RuntimeService: &serviceFakeRuntime{listErr: runtimeErr}, Store: store, LifecycleMonitor: monitor})

	_, err := service.ListAgents(context.Background())
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("ListAgents() error = %v, want %v", err, runtimeErr)
	}
	if _, err := store.Get("agent-1"); err != nil {
		t.Fatalf("record deleted after runtime failure: %v", err)
	}
	if len(monitor.stopped) != 0 {
		t.Fatalf("Monitor.Stop calls = %v, want none", monitor.stopped)
	}
}

func TestServiceListAgentsRemovesMissingPaneAfterStartedAgentBecomesActive(t *testing.T) {
	store := NewMemoryStore(nil)
	monitor := &serviceFakeMonitor{}
	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            store,
		LifecycleMonitor: monitor,
		NewAgentID:       func() ID { return "agent-1" },
	})
	if _, err := service.StartAgent(context.Background(), validStartRequest(t, KindGemini)); err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}

	response, err := service.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(response.Agents) != 0 {
		t.Fatalf("ListAgents() rows = %d, want 0", len(response.Agents))
	}
	if _, err := store.Get("agent-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active orphan store.Get() error = %v, want %v", err, ErrNotFound)
	}
	if !reflect.DeepEqual(monitor.stopped, []ID{"agent-1"}) {
		t.Fatalf("Monitor.Stop calls = %v, want [agent-1]", monitor.stopped)
	}
}

func TestServiceListAgentsReleasesOwnershipAfterCloseObserverRemovedRecord(t *testing.T) {
	store := NewMemoryStore(nil)
	monitor := &serviceFakeMonitor{}
	service := NewService(ServiceOptions{
		RuntimeService:   &serviceFakeRuntime{},
		Store:            store,
		LifecycleMonitor: monitor,
		NewAgentID:       func() ID { return "agent-1" },
	})
	if _, err := service.StartAgent(context.Background(), validStartRequest(t, KindClaude)); err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}
	if err := store.Delete("agent-1"); err != nil {
		t.Fatalf("simulate close observer delete: %v", err)
	}
	monitor.Stop("agent-1")

	if _, err := service.ListAgents(context.Background()); err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	service.lifecycleMu.Lock()
	_, ownerExists := service.owners["agent-1"]
	service.lifecycleMu.Unlock()
	if ownerExists {
		t.Fatal("ListAgents() retained ownership after close observer removed the record")
	}
}

func TestServiceFocusAgentForwardsLogicalPaneAndSourceContext(t *testing.T) {
	store := NewMemoryStore(nil)
	now := time.Now()
	record := Record{ID: "agent-1", Kind: KindGemini, PaneID: "logical-pane-7", State: StateBlocked, CreatedAt: now, StateChangedAt: now}
	if _, err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	runtimeService := &serviceFakeRuntime{focusResponse: runtime.FocusPaneResponse{Pane: runtime.Pane{ID: "logical-pane-7", ZellijPaneID: "terminal_7"}}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

	response, err := service.FocusAgent(context.Background(), FocusAgentRequest{
		AgentID:             "agent-1",
		SourceZellijSession: "dashboard-session",
		SourceZellijPaneID:  "terminal_3",
	})
	if err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	wantRequest := runtime.FocusPaneRequest{PaneID: "logical-pane-7", SourceZellijSession: "dashboard-session", SourceZellijPaneID: "terminal_3"}
	if !reflect.DeepEqual(runtimeService.focused, []runtime.FocusPaneRequest{wantRequest}) {
		t.Fatalf("FocusPane requests = %#v, want %#v", runtimeService.focused, []runtime.FocusPaneRequest{wantRequest})
	}
	if !reflect.DeepEqual(response.Agent.Agent, record) || response.Agent.Pane.ZellijPaneID != "terminal_7" {
		t.Fatalf("FocusAgent response = %#v", response)
	}
}

func TestServiceFocusAgentNormalizesStaleRuntimeTargetsToAgentNotFound(t *testing.T) {
	tests := []struct {
		name     string
		focusErr error
		pane     runtime.Pane
	}{
		{name: "runtime pane missing", focusErr: runtime.ErrPaneNotFound},
		{name: "runtime pane invalid", focusErr: runtime.ErrInvalidPaneTarget},
		{name: "closed response", pane: runtime.Pane{ID: "pane-1", Status: runtime.PaneStatusClosed}},
		{name: "exited response", pane: runtime.Pane{ID: "pane-1", Status: runtime.PaneStatusExited}},
		{name: "lost response", pane: runtime.Pane{ID: "pane-1", Status: runtime.PaneStatusLost}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			now := time.Now()
			if _, err := store.Create(Record{ID: "agent-1", Kind: KindCodex, PaneID: "pane-1", State: StateUnknown, CreatedAt: now, StateChangedAt: now}); err != nil {
				t.Fatal(err)
			}
			runtimeService := &serviceFakeRuntime{focusErr: test.focusErr, focusResponse: runtime.FocusPaneResponse{Pane: test.pane}}
			service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

			_, err := service.FocusAgent(context.Background(), FocusAgentRequest{
				AgentID: "agent-1", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
			})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("FocusAgent() error = %v, want codingagent %v", err, ErrNotFound)
			}
			if test.focusErr != nil && !errors.Is(err, test.focusErr) {
				t.Fatalf("FocusAgent() error = %v, want runtime cause %v", err, test.focusErr)
			}
		})
	}
}

func TestServiceNewServiceRejectsNilAndTypedNilDependencies(t *testing.T) {
	validRuntime := &serviceFakeRuntime{}
	validStore := NewMemoryStore(nil)
	validMonitor := &serviceFakeMonitor{}
	var nilRuntime *serviceFakeRuntime
	var nilStore *serviceNilStore
	var nilMonitor *serviceFakeMonitor

	tests := []struct {
		name    string
		options ServiceOptions
	}{
		{name: "plain nil runtime", options: ServiceOptions{Store: validStore, LifecycleMonitor: validMonitor}},
		{name: "typed nil runtime", options: ServiceOptions{RuntimeService: nilRuntime, Store: validStore, LifecycleMonitor: validMonitor}},
		{name: "plain nil store", options: ServiceOptions{RuntimeService: validRuntime, LifecycleMonitor: validMonitor}},
		{name: "typed nil store", options: ServiceOptions{RuntimeService: validRuntime, Store: nilStore, LifecycleMonitor: validMonitor}},
		{name: "plain nil monitor", options: ServiceOptions{RuntimeService: validRuntime, Store: validStore}},
		{name: "typed nil monitor", options: ServiceOptions{RuntimeService: validRuntime, Store: validStore, LifecycleMonitor: nilMonitor}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if service := NewService(test.options); service != nil {
				t.Fatalf("NewService() = %#v, want nil", service)
			}
		})
	}

	valueRuntime := serviceValueRuntime{}
	valueStore := serviceValueStore{Store: NewMemoryStore(nil)}
	valueMonitor := serviceValueMonitor{}
	if service := NewService(ServiceOptions{RuntimeService: valueRuntime, Store: valueStore, LifecycleMonitor: valueMonitor}); service == nil {
		t.Fatal("NewService() rejected non-nil value implementations")
	}
}

func TestServiceImplementsAgentAndRuntimeServices(t *testing.T) {
	var _ AgentService = (*Service)(nil)
	var _ runtime.RuntimeService = (*Service)(nil)
}

type serviceFakeRuntime struct {
	runtime.RuntimeService
	createFn      func(context.Context, runtime.CreatePaneRequest) (runtime.CreatePaneResponse, error)
	createErr     error
	created       []runtime.CreatePaneRequest
	cleanupFn     func(context.Context, runtime.CleanupRequest) (runtime.CleanupResponse, error)
	cleanupErr    error
	cleaned       []runtime.CleanupRequest
	listFn        func(context.Context) (runtime.ListPanesResponse, error)
	listResponse  runtime.ListPanesResponse
	listErr       error
	focusResponse runtime.FocusPaneResponse
	focusErr      error
	focused       []runtime.FocusPaneRequest
}

func (f *serviceFakeRuntime) CreatePane(ctx context.Context, request runtime.CreatePaneRequest) (runtime.CreatePaneResponse, error) {
	f.created = append(f.created, request)
	if f.createFn != nil {
		return f.createFn(ctx, request)
	}
	if f.createErr != nil {
		return runtime.CreatePaneResponse{}, f.createErr
	}
	return runtime.CreatePaneResponse{Pane: runtime.Pane{ID: request.ID}}, nil
}

func (f *serviceFakeRuntime) Cleanup(ctx context.Context, request runtime.CleanupRequest) (runtime.CleanupResponse, error) {
	f.cleaned = append(f.cleaned, request)
	if f.cleanupFn != nil {
		return f.cleanupFn(ctx, request)
	}
	return runtime.CleanupResponse{}, f.cleanupErr
}

func (f *serviceFakeRuntime) ListPanes(ctx context.Context) (runtime.ListPanesResponse, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return f.listResponse, f.listErr
}

func (f *serviceFakeRuntime) FocusPane(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
	f.focused = append(f.focused, request)
	return f.focusResponse, f.focusErr
}

type serviceFakeMonitor struct {
	startFn  func(Record) error
	startErr error
	started  []Record
	stopped  []ID
}

type serviceNilStore struct {
	Store
}

type serviceValueRuntime struct {
	runtime.RuntimeService
}

type serviceValueStore struct {
	Store
}

type serviceValueMonitor struct{}

func (serviceValueMonitor) Start(Record) error { return nil }
func (serviceValueMonitor) Stop(ID)            {}

func (m *serviceFakeMonitor) Start(record Record) error {
	m.started = append(m.started, record)
	if m.startFn != nil {
		return m.startFn(record)
	}
	return m.startErr
}

func (m *serviceFakeMonitor) Stop(id ID) {
	m.stopped = append(m.stopped, id)
}

func validStartRequest(t *testing.T, kind Kind) StartAgentRequest {
	t.Helper()
	return StartAgentRequest{
		Kind:                kind,
		CWD:                 t.TempDir(),
		SourceZellijSession: "physical-a",
		SourceZellijPaneID:  "terminal_2",
	}
}

func withStartCWD(request StartAgentRequest, cwd string) StartAgentRequest {
	request.CWD = cwd
	return request
}

func withStartSession(request StartAgentRequest, session string) StartAgentRequest {
	request.SourceZellijSession = session
	return request
}

func withStartPane(request StartAgentRequest, pane string) StartAgentRequest {
	request.SourceZellijPaneID = runtime.ZellijPaneID(pane)
	return request
}

func TestServiceStartAgentResolvesRelativeCWD(t *testing.T) {
	cwd := t.TempDir()
	parent := filepath.Dir(cwd)
	relative, err := filepath.Rel(parent, cwd)
	if err != nil {
		t.Fatal(err)
	}
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	wantCWD, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}

	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            NewMemoryStore(nil),
		LifecycleMonitor: &serviceFakeMonitor{},
		NewAgentID:       func() ID { return "agent-1" },
	})
	_, err = service.StartAgent(context.Background(), StartAgentRequest{
		Kind: KindCodex, CWD: relative, SourceZellijSession: "physical-a", SourceZellijPaneID: "terminal_2",
	})
	if err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}
	if got := runtimeService.created[0].CWD; got != wantCWD {
		t.Fatalf("CreatePane CWD = %q, want %q", got, wantCWD)
	}
}

func TestServiceStartAgentDoesNotMutateExtraArgs(t *testing.T) {
	extra := []string{"--model", "gemini-3"}
	service := NewService(ServiceOptions{
		RuntimeService:   &serviceFakeRuntime{},
		Store:            NewMemoryStore(nil),
		LifecycleMonitor: &serviceFakeMonitor{},
		NewAgentID:       func() ID { return "agent-1" },
	})
	_, err := service.StartAgent(context.Background(), StartAgentRequest{
		Kind: KindGemini, CWD: t.TempDir(), ExtraArgs: extra, SourceZellijSession: "physical-a", SourceZellijPaneID: "terminal_2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(extra, " ") != "--model gemini-3" {
		t.Fatalf("ExtraArgs mutated: %v", extra)
	}
}
