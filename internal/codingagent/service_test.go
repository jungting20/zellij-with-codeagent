package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
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
		claimFn: func(_ context.Context, request runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error) {
			events = append(events, "runtime")
			if _, err := store.Get("agent-1"); err != nil {
				t.Fatalf("record must exist before ClaimPane: %v", err)
			}
			want := runtime.ClaimPaneRequest{
				ID: "agent-1", AgentID: "agent-1", Role: "coding-agent",
				ZellijSession: "physical-a", ZellijPaneID: "terminal_2",
				Command: []string{"agy", "--dangerously-skip-permissions", "--model", "gemini-3"},
				CWD:     cwd,
			}
			if !reflect.DeepEqual(request, want) {
				t.Fatalf("ClaimPane request = %#v, want %#v", request, want)
			}
			return runtime.ClaimPaneResponse{Pane: runtime.Pane{
				ID:           request.ID,
				AgentID:      request.AgentID,
				Role:         request.Role,
				SessionID:    runtime.SessionID(request.ZellijSession),
				Command:      append([]string(nil), request.Command...),
				CWD:          request.CWD,
				ZellijPaneID: request.ZellijPaneID,
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
		NotifyOnIdle:        true,
		SourceZellijSession: "physical-a",
		SourceZellijPaneID:  "terminal_2",
	})
	if err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}

	wantRecord := Record{
		ID:             "agent-1",
		Kind:           KindGemini,
		AccessMode:     AccessFull,
		PaneID:         "agent-1",
		CWD:            cwd,
		State:          StateUnknown,
		NotifyOnIdle:   true,
		CreatedAt:      now,
		StateChangedAt: now,
	}
	if !reflect.DeepEqual(response.Agent.Agent, wantRecord) {
		t.Errorf("response record = %#v, want %#v", response.Agent.Agent, wantRecord)
	}
	wantPane := runtime.Pane{
		ID: "agent-1", AgentID: "agent-1", Role: "coding-agent",
		SessionID: "physical-a", ZellijPaneID: "terminal_2",
		Command: []string{"agy", "--dangerously-skip-permissions", "--model", "gemini-3"},
		CWD:     cwd,
	}
	if !reflect.DeepEqual(response.Agent.Pane, wantPane) {
		t.Errorf("response pane = %#v", response.Agent.Pane)
	}
	if !reflect.DeepEqual(events, []string{"monitor", "runtime"}) {
		t.Errorf("side-effect order = %v, want [monitor runtime] after store registration", events)
	}
	if len(monitor.started) != 1 {
		t.Fatalf("Monitor.Start call count = %d, want 1", len(monitor.started))
	}
}

func TestServiceStartHermesCreatesManagedPaneWithoutStateMonitor(t *testing.T) {
	now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	store := NewMemoryStore(func() time.Time { return now })
	monitor := &serviceFakeMonitor{startErr: errors.New("monitor must not start")}
	cwd := t.TempDir()
	runtimeService := &serviceFakeRuntime{
		claimFn: func(_ context.Context, request runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error) {
			wantCommand := []string{"hermes", "chat", "--yolo", "-q", "investigate"}
			if !reflect.DeepEqual(request.Command, wantCommand) {
				t.Fatalf("ClaimPane command = %#v, want %#v", request.Command, wantCommand)
			}
			return runtime.ClaimPaneResponse{Pane: runtime.Pane{
				ID: request.ID, AgentID: request.AgentID, Role: request.Role,
				SessionID: runtime.SessionID(request.ZellijSession), ZellijPaneID: request.ZellijPaneID,
				Command: append([]string(nil), request.Command...), CWD: request.CWD,
			}}, nil
		},
	}
	service := NewService(ServiceOptions{
		RuntimeService: runtimeService, Store: store, LifecycleMonitor: monitor,
		Now: func() time.Time { return now }, NewAgentID: func() ID { return "agent-hermes" },
	})

	response, err := service.StartAgent(context.Background(), StartAgentRequest{
		Kind: KindHermes, CWD: cwd,
		ExtraArgs: []string{"chat", "--yolo", "-q", "investigate"}, NotifyOnIdle: true,
		SourceZellijSession: "physical-a", SourceZellijPaneID: "terminal_2",
	})
	if err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}
	if len(monitor.started) != 0 {
		t.Fatalf("monitor started = %#v, want none", monitor.started)
	}
	if response.Agent.Agent.State != StateUnknown || response.Agent.Agent.StateReason != "state_tracking_disabled" {
		t.Fatalf("Hermes state = (%q, %q), want unknown/state_tracking_disabled", response.Agent.Agent.State, response.Agent.Agent.StateReason)
	}
	if response.Agent.Agent.NotifyOnIdle {
		t.Fatal("Hermes NotifyOnIdle = true, want false")
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
			if len(records) != 0 || len(monitor.started) != 0 || len(runtimeService.claimed) != 0 {
				t.Fatalf("invalid request caused side effects: records=%d monitor=%d claims=%d", len(records), len(monitor.started), len(runtimeService.claimed))
			}
		})
	}
}

func TestServiceStartAgentAppliesCanonicalAccessModeAndCommand(t *testing.T) {
	tests := []struct {
		name        string
		accessMode  AccessMode
		wantAccess  AccessMode
		wantCommand []string
	}{
		{
			name:        "empty defaults to full",
			wantAccess:  AccessFull,
			wantCommand: []string{"codex", "--dangerously-bypass-approvals-and-sandbox", "review this repository"},
		},
		{
			name:        "full keeps bypass",
			accessMode:  AccessFull,
			wantAccess:  AccessFull,
			wantCommand: []string{"codex", "--dangerously-bypass-approvals-and-sandbox", "review this repository"},
		},
		{
			name:        "read only uses sandbox",
			accessMode:  AccessReadOnly,
			wantAccess:  AccessReadOnly,
			wantCommand: []string{"codex", "--sandbox", "read-only", "--ask-for-approval", "never", "review this repository"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			monitor := &serviceFakeMonitor{}
			runtimeService := &serviceFakeRuntime{}
			service := NewService(ServiceOptions{
				RuntimeService:   runtimeService,
				Store:            store,
				LifecycleMonitor: monitor,
				NewAgentID:       func() ID { return "agent-1" },
			})

			request := validStartRequest(t, KindCodex)
			request.AccessMode = test.accessMode
			if test.wantAccess == AccessReadOnly {
				request.Prompt = "review this repository"
			} else {
				request.ExtraArgs = []string{"review this repository"}
			}
			response, err := service.StartAgent(context.Background(), request)
			if err != nil {
				t.Fatalf("StartAgent() error = %v", err)
			}
			if response.Agent.Agent.AccessMode != test.wantAccess {
				t.Fatalf("record access = %q, want %q", response.Agent.Agent.AccessMode, test.wantAccess)
			}
			if !reflect.DeepEqual(response.Agent.Pane.Command, test.wantCommand) {
				t.Fatalf("pane command = %#v, want %#v", response.Agent.Pane.Command, test.wantCommand)
			}
			if len(runtimeService.claimed) != 1 || !reflect.DeepEqual(runtimeService.claimed[0].Command, test.wantCommand) {
				t.Fatalf("ClaimPane commands = %#v, want %#v", runtimeService.claimed, test.wantCommand)
			}
			if test.wantAccess == AccessReadOnly && slices.Contains(response.Agent.Pane.Command, "--dangerously-bypass-approvals-and-sandbox") {
				t.Fatalf("read-only command includes permission bypass: %#v", response.Agent.Pane.Command)
			}
		})
	}
}

func TestServiceReadOnlyClaimsAndReturnsNoBypassCommand(t *testing.T) {
	cwd := t.TempDir()
	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            NewMemoryStore(nil),
		LifecycleMonitor: &serviceFakeMonitor{},
		NewAgentID:       func() ID { return "agent-1" },
	})

	response, err := service.StartAgent(context.Background(), StartAgentRequest{
		Kind:                KindCodex,
		AccessMode:          AccessReadOnly,
		CWD:                 cwd,
		Prompt:              "Verify M1",
		SourceZellijSession: "physical-a",
		SourceZellijPaneID:  "terminal_2",
	})
	if err != nil {
		t.Fatalf("StartAgent() error = %v", err)
	}

	want := []string{"codex", "--sandbox", "read-only", "--ask-for-approval", "never", "Verify M1"}
	if !reflect.DeepEqual(response.Agent.Pane.Command, want) {
		t.Fatalf("response pane command = %#v, want %#v", response.Agent.Pane.Command, want)
	}
	if len(runtimeService.claimed) != 1 || !reflect.DeepEqual(runtimeService.claimed[0].Command, want) {
		t.Fatalf("ClaimPane command = %#v, want %#v", runtimeService.claimed, want)
	}
	if slices.Contains(response.Agent.Pane.Command, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("read-only command includes permission bypass: %#v", response.Agent.Pane.Command)
	}
}

func TestServiceStartAgentRejectsReadOnlyPayloadBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prompt string
		extra  []string
	}{
		{name: "arguments", extra: []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{name: "option prompt", prompt: "--config sandbox=workspace-write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			monitor := &serviceFakeMonitor{}
			runtimeService := &serviceFakeRuntime{}
			service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: monitor, NewAgentID: func() ID { return "agent-1" }})
			req := validStartRequest(t, KindCodex)
			req.AccessMode, req.Prompt, req.ExtraArgs = AccessReadOnly, tc.prompt, tc.extra
			if _, err := service.StartAgent(context.Background(), req); !errors.Is(err, ErrInvalidAccessMode) {
				t.Fatalf("StartAgent() error = %v, want ErrInvalidAccessMode", err)
			}
			records, _ := store.List()
			if len(records) != 0 || len(monitor.started) != 0 || len(runtimeService.claimed) != 0 {
				t.Fatalf("rejected request caused side effects: records=%d monitor=%d claims=%d", len(records), len(monitor.started), len(runtimeService.claimed))
			}
		})
	}
}

func TestServiceStartAgentRejectsUnsupportedAccessBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		kind   Kind
		access AccessMode
	}{
		{name: "read-only Gemini", kind: KindGemini, access: AccessReadOnly},
		{name: "unknown mode", kind: KindCodex, access: AccessMode("limited")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			monitor := &serviceFakeMonitor{}
			runtimeService := &serviceFakeRuntime{}
			service := NewService(ServiceOptions{
				RuntimeService:   runtimeService,
				Store:            store,
				LifecycleMonitor: monitor,
				NewAgentID:       func() ID { return "agent-1" },
			})

			request := validStartRequest(t, test.kind)
			request.AccessMode = test.access
			request.CWD = filepath.Join(t.TempDir(), "missing")
			request.SourceZellijSession = ""
			request.SourceZellijPaneID = ""
			if _, err := service.StartAgent(context.Background(), request); !errors.Is(err, ErrInvalidAccessMode) {
				t.Fatalf("StartAgent() error = %v, want ErrInvalidAccessMode", err)
			}
			records, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 0 || len(monitor.started) != 0 || len(runtimeService.claimed) != 0 {
				t.Fatalf("unsupported access caused side effects: records=%d monitor=%d claims=%d", len(records), len(monitor.started), len(runtimeService.claimed))
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
	if len(monitor.started) != 0 || len(runtimeService.claimed) != 0 {
		t.Fatalf("duplicate ID started dependencies: monitor=%d claims=%d", len(monitor.started), len(runtimeService.claimed))
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
			if len(runtimeService.claimed) != 0 {
				t.Fatalf("ClaimPane call count = %d, want 0", len(runtimeService.claimed))
			}
			if len(monitor.stopped) != 0 {
				t.Fatalf("Monitor.Stop call count = %d, want 0", len(monitor.stopped))
			}
		})
	}
}

func TestServiceStartAgentClaimFailureStopsMonitorDeletesRecordAndLeavesSourcePaneUntouched(t *testing.T) {
	store := NewMemoryStore(nil)
	runtimeErr := errors.New("claim pane failed")
	monitor := &serviceFakeMonitor{}
	runtimeService := &serviceFakeRuntime{claimErr: runtimeErr}
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
	if len(runtimeService.claimed) != 1 {
		t.Fatalf("ClaimPane call count = %d, want 1", len(runtimeService.claimed))
	}
	if len(runtimeService.cleaned) != 0 {
		t.Fatalf("claim failure cleaned source pane: cleanup=%d", len(runtimeService.cleaned))
	}
}

func TestServiceStartAgentProvisioningRecordIsNotRemovedByConcurrentList(t *testing.T) {
	store := NewMemoryStore(nil)
	monitor := &serviceFakeMonitor{}
	claimEntered := make(chan struct{})
	releaseClaim := make(chan struct{})
	listEntered := make(chan struct{})
	releaseList := make(chan struct{})
	runtimeService := &serviceFakeRuntime{
		claimFn: func(_ context.Context, request runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error) {
			close(claimEntered)
			<-releaseClaim
			return runtime.ClaimPaneResponse{Pane: runtime.Pane{ID: request.ID, Status: runtime.PaneStatusRunning}}, nil
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
	<-claimEntered

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
	close(releaseClaim)
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
	if len(runtimeService.claimed) != 1 {
		t.Fatalf("ClaimPane call count = %d, want 1", len(runtimeService.claimed))
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
		claimFn: func(_ context.Context, request runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error) {
			return runtime.ClaimPaneResponse{Pane: runtime.Pane{ID: request.ID, Status: runtime.PaneStatusRunning}}, nil
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

func TestServiceSetAgentPinnedPersistsInSubsequentLists(t *testing.T) {
	store := NewMemoryStore(nil)
	now := time.Unix(10, 0)
	record := Record{ID: "agent-1", Kind: KindCodex, PaneID: "pane-1", State: StateIdle, CreatedAt: now, StateChangedAt: now}
	if _, err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	runtimeService := &serviceFakeRuntime{listResponse: runtime.ListPanesResponse{Panes: []runtime.Pane{{ID: "pane-1"}}}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

	response, err := service.SetAgentPinned(context.Background(), SetAgentPinnedRequest{AgentID: "agent-1", Pinned: true})
	if err != nil {
		t.Fatalf("SetAgentPinned() error = %v", err)
	}
	if !response.Agent.Pinned {
		t.Fatalf("SetAgentPinned() = %#v", response)
	}
	listed, err := service.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(listed.Agents) != 1 || !listed.Agents[0].Agent.Pinned {
		t.Fatalf("ListAgents() = %#v", listed)
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

func TestServiceDifferentIDStartsSweepClosedOwnersWithoutAccumulating(t *testing.T) {
	store := NewMemoryStore(nil)
	monitor := &serviceFakeMonitor{}
	service := NewService(ServiceOptions{
		RuntimeService:   &serviceFakeRuntime{},
		Store:            store,
		LifecycleMonitor: monitor,
	})
	for index := 0; index < 20; index++ {
		response, err := service.StartAgent(context.Background(), validStartRequest(t, KindCodex))
		if err != nil {
			t.Fatalf("StartAgent(%d) error = %v", index, err)
		}
		if err := store.Delete(response.Agent.Agent.ID); err != nil {
			t.Fatalf("simulate close observer delete %q: %v", response.Agent.Agent.ID, err)
		}
		monitor.Stop(response.Agent.Agent.ID)
	}
	if _, err := service.StartAgent(context.Background(), validStartRequest(t, KindClaude)); err != nil {
		t.Fatalf("final StartAgent() error = %v", err)
	}

	service.lifecycleMu.Lock()
	ownerCount := len(service.owners)
	service.lifecycleMu.Unlock()
	if ownerCount != 1 {
		t.Fatalf("owner count = %d, want only current active owner", ownerCount)
	}
}

func TestServiceFocusAgentSweepsUnrelatedClosedOwnerAndPreservesTrackedUncertainOwner(t *testing.T) {
	store := NewMemoryStore(nil)
	monitor := &serviceFakeMonitor{}
	runtimeService := &serviceFakeRuntime{focusResponse: runtime.FocusPaneResponse{Pane: runtime.Pane{ID: "target-pane", Status: runtime.PaneStatusRunning}}}
	ids := []ID{"closed-agent", "uncertain-agent"}
	nextID := 0
	service := NewService(ServiceOptions{
		RuntimeService:   runtimeService,
		Store:            store,
		LifecycleMonitor: monitor,
		NewAgentID: func() ID {
			id := ids[nextID]
			nextID++
			return id
		},
	})
	closed, err := service.StartAgent(context.Background(), validStartRequest(t, KindCodex))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(closed.Agent.Agent.ID); err != nil {
		t.Fatal(err)
	}
	monitor.Stop(closed.Agent.Agent.ID)
	uncertain, err := service.StartAgent(context.Background(), validStartRequest(t, KindClaude))
	if err != nil {
		t.Fatal(err)
	}
	service.lifecycleMu.Lock()
	service.owners[uncertain.Agent.Agent.ID].state = agentCleanupUncertain
	service.lifecycleMu.Unlock()
	now := time.Now()
	if _, err := store.Create(Record{ID: "target", Kind: KindGemini, PaneID: "target-pane", State: StateUnknown, CreatedAt: now, StateChangedAt: now}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.FocusAgent(context.Background(), FocusAgentRequest{
		AgentID: "target", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	}); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	service.lifecycleMu.Lock()
	_, closedExists := service.owners["closed-agent"]
	uncertainOwner := service.owners["uncertain-agent"]
	service.lifecycleMu.Unlock()
	if closedExists {
		t.Fatal("FocusAgent retained unrelated closed owner")
	}
	if uncertainOwner == nil || uncertainOwner.state != agentCleanupUncertain {
		t.Fatalf("tracked uncertain owner = %#v, want preserved", uncertainOwner)
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

func TestServiceFocusAgentRejectsBlankSourceBeforeRuntimeCall(t *testing.T) {
	for _, request := range []FocusAgentRequest{
		{AgentID: "agent-1", SourceZellijPaneID: "terminal_1"},
		{AgentID: "agent-1", SourceZellijSession: "dashboard"},
		{AgentID: "agent-1", SourceZellijSession: "   ", SourceZellijPaneID: "  "},
	} {
		store := NewMemoryStore(nil)
		now := time.Now()
		if _, err := store.Create(Record{ID: "agent-1", Kind: KindCodex, PaneID: "pane-1", State: StateUnknown, CreatedAt: now, StateChangedAt: now}); err != nil {
			t.Fatal(err)
		}
		runtimeService := &serviceFakeRuntime{}
		service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

		if _, err := service.FocusAgent(context.Background(), request); !errors.Is(err, ErrAgentSourceRequired) {
			t.Fatalf("FocusAgent(%#v) error = %v, want %v", request, err, ErrAgentSourceRequired)
		}
		if len(runtimeService.focused) != 0 || runtimeService.listed != 0 {
			t.Fatalf("blank source called runtime: focus=%d list=%d", len(runtimeService.focused), runtimeService.listed)
		}
	}
}

func TestServiceFocusAgentClassifiesInvalidTargetFromCurrentRuntimePane(t *testing.T) {
	listFailure := errors.New("runtime list failed")
	tests := []struct {
		name         string
		panes        []runtime.Pane
		listErr      error
		wantNotFound bool
		wantListErr  bool
	}{
		{name: "absent target is stale", wantNotFound: true},
		{name: "closed target is stale", panes: []runtime.Pane{{ID: "pane-1", Status: runtime.PaneStatusClosed}}, wantNotFound: true},
		{name: "lost target is stale", panes: []runtime.Pane{{ID: "pane-1", Status: runtime.PaneStatusLost}}, wantNotFound: true},
		{name: "active target preserves bad request", panes: []runtime.Pane{{ID: "pane-1", Status: runtime.PaneStatusRunning}}},
		{name: "confirmation failure preserves bad request", listErr: listFailure, wantListErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			now := time.Now()
			if _, err := store.Create(Record{ID: "agent-1", Kind: KindGemini, PaneID: "pane-1", State: StateUnknown, CreatedAt: now, StateChangedAt: now}); err != nil {
				t.Fatal(err)
			}
			runtimeService := &serviceFakeRuntime{
				focusErr:     runtime.ErrInvalidPaneTarget,
				listResponse: runtime.ListPanesResponse{Panes: test.panes},
				listErr:      test.listErr,
			}
			service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

			_, err := service.FocusAgent(context.Background(), FocusAgentRequest{
				AgentID: "agent-1", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
			})
			if !errors.Is(err, runtime.ErrInvalidPaneTarget) {
				t.Fatalf("FocusAgent() error = %v, want %v", err, runtime.ErrInvalidPaneTarget)
			}
			if got := errors.Is(err, ErrNotFound); got != test.wantNotFound {
				t.Fatalf("errors.Is(ErrNotFound) = %v, want %v; error=%v", got, test.wantNotFound, err)
			}
			if runtimeService.listed != 1 {
				t.Fatalf("ListPanes call count = %d, want 1", runtimeService.listed)
			}
			if test.wantListErr && !errors.Is(err, listFailure) {
				t.Fatalf("FocusAgent() error = %v, want list diagnostic %v", err, listFailure)
			}
		})
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

func TestServiceFocusNextAgentSelectsFirstConsecutiveAndWrappedAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	runtimeService := &serviceFakeRuntime{focusFn: func(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
		return runtime.FocusPaneResponse{Pane: runtime.Pane{ID: request.PaneID, Status: runtime.PaneStatusRunning}}, nil
	}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1"}

	wantIDs := []ID{"agent-1", "agent-2", "agent-3", "agent-1"}
	for call, wantID := range wantIDs {
		response, err := service.FocusNextAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusNextAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("FocusNextAgent() call %d response = %#v, want focused %q", call+1, response, wantID)
		}
	}
}

func TestServiceFocusPreviousAgentSelectsLastConsecutiveAndWrappedAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusPreviousAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1"}

	for call, wantID := range []ID{"agent-3", "agent-2", "agent-1", "agent-3"} {
		response, err := service.FocusPreviousAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusPreviousAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("FocusPreviousAgent() call %d response = %#v, want focused %q", call+1, response, wantID)
		}
	}
}

func TestServiceFocusPreviousAgentSelectsOnlyIdleAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateIdle, StateBlocked, StateIdle})
	service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusPreviousAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", IdleOnly: true}

	for call, wantID := range []ID{"agent-4", "agent-2", "agent-4"} {
		response, err := service.FocusPreviousAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusPreviousAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("FocusPreviousAgent() call %d response = %#v, want focused %q", call+1, response, wantID)
		}
	}
}

func TestServiceFocusAdjacentAgentSelectsOnlyPinnedAgents(t *testing.T) {
	tests := []struct {
		name    string
		forward bool
		wantIDs []ID
	}{
		{name: "next", forward: true, wantIDs: []ID{"agent-1", "agent-3", "agent-1"}},
		{name: "previous", wantIDs: []ID{"agent-3", "agent-1", "agent-3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore(nil)
			seedFocusRecords(t, store)
			for _, id := range []ID{"agent-1", "agent-3"} {
				if _, err := store.SetPinned(id, true); err != nil {
					t.Fatalf("SetPinned(%q) error = %v", id, err)
				}
			}
			service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
			request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", PinnedOnly: true}

			for call, wantID := range tt.wantIDs {
				var response FocusNextAgentResponse
				var err error
				if tt.forward {
					response, err = service.FocusNextAgent(context.Background(), request)
				} else {
					response, err = service.FocusPreviousAgent(context.Background(), request)
				}
				if err != nil {
					t.Fatalf("focus call %d error = %v", call+1, err)
				}
				if !response.Focused || response.Agent.Agent.ID != wantID {
					t.Fatalf("focus call %d response = %#v, want focused %q", call+1, response, wantID)
				}
			}
		})
	}
}

func TestServiceFocusAdjacentAgentPinnedAndIdleFiltersIntersect(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateIdle, StateIdle, StateBlocked})
	for _, id := range []ID{"agent-1", "agent-2", "agent-4"} {
		if _, err := store.SetPinned(id, true); err != nil {
			t.Fatalf("SetPinned(%q) error = %v", id, err)
		}
	}
	service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{
		SourceZellijSession: "dashboard",
		SourceZellijPaneID:  "terminal_1",
		IdleOnly:            true,
		PinnedOnly:          true,
	}

	for _, focus := range []func(context.Context, FocusNextAgentRequest) (FocusNextAgentResponse, error){
		service.FocusNextAgent,
		service.FocusPreviousAgent,
	} {
		response, err := focus(context.Background(), request)
		if err != nil || !response.Focused || response.Agent.Agent.ID != "agent-2" {
			t.Fatalf("focus response=%#v error=%v, want only pinned idle agent-2", response, err)
		}
	}
}

func TestServiceFocusNextAgentSelectsOnlyIdleAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateIdle, StateBlocked, StateIdle})
	runtimeService := successfulFocusRuntime()
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", IdleOnly: true}

	for call, wantID := range []ID{"agent-4", "agent-2", "agent-4"} {
		response, err := service.FocusNextAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusNextAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("FocusNextAgent() call %d response = %#v, want focused %q", call+1, response, wantID)
		}
	}
}

func TestServiceFocusNextAgentPrioritizesNewIdleCompletion(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", IdleOnly: true}

	first, err := service.FocusNextAgent(context.Background(), request)
	if err != nil {
		t.Fatalf("first FocusNextAgent() error = %v", err)
	}
	if !first.Focused || first.Agent.Agent.ID != "agent-3" {
		t.Fatalf("first FocusNextAgent() response = %#v, want latest idle agent-3", first)
	}
	for call, wantID := range []ID{"agent-2", "agent-1"} {
		response, err := service.FocusNextAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("idle history FocusNextAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("idle history FocusNextAgent() call %d response = %#v, want %q", call+1, response, wantID)
		}
	}

	if _, err := store.UpdateState("agent-3", StateUpdate{State: StateWorking}); err != nil {
		t.Fatalf("UpdateState(agent-3, working) error = %v", err)
	}
	if _, err := store.UpdateState("agent-3", StateUpdate{State: StateIdle}); err != nil {
		t.Fatalf("UpdateState(agent-3, idle) error = %v", err)
	}

	completedAgain, err := service.FocusNextAgent(context.Background(), request)
	if err != nil {
		t.Fatalf("second FocusNextAgent() error = %v", err)
	}
	if !completedAgain.Focused || completedAgain.Agent.Agent.ID != "agent-3" {
		t.Fatalf("second FocusNextAgent() response = %#v, want newly idle agent-3", completedAgain)
	}

	next, err := service.FocusNextAgent(context.Background(), request)
	if err != nil {
		t.Fatalf("third FocusNextAgent() error = %v", err)
	}
	if !next.Focused || next.Agent.Agent.ID != "agent-2" {
		t.Fatalf("third FocusNextAgent() response = %#v, want next most recent idle agent-2", next)
	}
}

func TestServiceFocusNextAgentDefaultsToAllStates(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateIdle, StateBlocked, StateUnknown})
	service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1"}

	for call, wantID := range []ID{"agent-1", "agent-2", "agent-3", "agent-4", "agent-1"} {
		response, err := service.FocusNextAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusNextAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("FocusNextAgent() call %d response = %#v, want focused %q", call+1, response, wantID)
		}
	}
}

func TestServiceFocusAgentUpdatesCursorForNextAgent(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	runtimeService := &serviceFakeRuntime{focusFn: func(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
		return runtime.FocusPaneResponse{Pane: runtime.Pane{ID: request.PaneID, Status: runtime.PaneStatusRunning}}, nil
	}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

	if _, err := service.FocusAgent(context.Background(), FocusAgentRequest{
		AgentID: "agent-2", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	}); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	response, err := service.FocusNextAgent(context.Background(), FocusNextAgentRequest{
		SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	})
	if err != nil {
		t.Fatalf("FocusNextAgent() error = %v", err)
	}
	if !response.Focused || response.Agent.Agent.ID != "agent-3" {
		t.Fatalf("FocusNextAgent() response = %#v, want focused agent-3", response)
	}
}

func TestServiceFocusNextAgentRestartsWhenCursorAgentWasDeleted(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	runtimeService := &serviceFakeRuntime{focusFn: func(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
		return runtime.FocusPaneResponse{Pane: runtime.Pane{ID: request.PaneID, Status: runtime.PaneStatusRunning}}, nil
	}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

	if _, err := service.FocusAgent(context.Background(), FocusAgentRequest{
		AgentID: "agent-2", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	}); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	if err := store.Delete("agent-2"); err != nil {
		t.Fatalf("Delete(agent-2) error = %v", err)
	}
	response, err := service.FocusNextAgent(context.Background(), FocusNextAgentRequest{
		SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	})
	if err != nil {
		t.Fatalf("FocusNextAgent() error = %v", err)
	}
	if !response.Focused || response.Agent.Agent.ID != "agent-1" {
		t.Fatalf("FocusNextAgent() response = %#v, want focused agent-1", response)
	}
}

func TestServiceFocusNextAgentDoesNothingWithoutIdleAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateBlocked, StateUnknown})
	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	service.lastFocusedID = "agent-2"

	response, err := service.FocusNextAgent(context.Background(), FocusNextAgentRequest{
		SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", IdleOnly: true,
	})
	if err != nil || response.Focused || response.Agent.Agent.ID != "" {
		t.Fatalf("FocusNextAgent() response=%#v error=%v, want successful no-op", response, err)
	}
	if len(runtimeService.focused) != 0 || service.lastFocusedID != "agent-2" {
		t.Fatalf("no-op focus calls=%d cursor=%q", len(runtimeService.focused), service.lastFocusedID)
	}
}

func TestServiceFocusNextAgentDoesNothingForEmptyStore(t *testing.T) {
	store := NewMemoryStore(nil)
	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	service.lastFocusedID = "agent-previous"

	response, err := service.FocusNextAgent(context.Background(), FocusNextAgentRequest{
		SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	})
	if err != nil || response.Focused || response.Agent.Agent.ID != "" {
		t.Fatalf("FocusNextAgent() response=%#v error=%v, want successful no-op", response, err)
	}
	if len(runtimeService.focused) != 0 || service.lastFocusedID != "agent-previous" {
		t.Fatalf("empty-store no-op focus calls=%d cursor=%q", len(runtimeService.focused), service.lastFocusedID)
	}
}

func TestServiceFocusNextAgentRepeatedlySelectsOnlyIdleAgent(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateIdle, StateBlocked})
	runtimeService := successfulFocusRuntime()
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", IdleOnly: true}

	for call := 1; call <= 2; call++ {
		response, err := service.FocusNextAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusNextAgent() call %d error = %v", call, err)
		}
		if !response.Focused || response.Agent.Agent.ID != "agent-2" {
			t.Fatalf("FocusNextAgent() call %d response = %#v, want focused agent-2", call, response)
		}
		if got := len(runtimeService.focused); got != call {
			t.Fatalf("runtime focus calls after call %d = %d, want %d", call, got, call)
		}
		if service.lastFocusedID != "agent-2" {
			t.Fatalf("cursor after call %d = %q, want agent-2", call, service.lastFocusedID)
		}
	}
}

func TestServiceFocusNextAgentRestartsWhenCursorAgentBecomesWorking(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	service := NewService(ServiceOptions{RuntimeService: successfulFocusRuntime(), Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

	if _, err := service.FocusAgent(context.Background(), FocusAgentRequest{
		AgentID: "agent-2", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	}); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	if _, err := store.UpdateState("agent-2", StateUpdate{State: StateWorking}); err != nil {
		t.Fatalf("UpdateState(agent-2) error = %v", err)
	}

	response, err := service.FocusNextAgent(context.Background(), FocusNextAgentRequest{
		SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1", IdleOnly: true,
	})
	if err != nil {
		t.Fatalf("FocusNextAgent() error = %v", err)
	}
	if !response.Focused || response.Agent.Agent.ID != "agent-3" {
		t.Fatalf("FocusNextAgent() response = %#v, want latest idle agent-3", response)
	}
}

func TestServiceFocusNextAgentRejectsBlankSourceBeforeListingAgents(t *testing.T) {
	for _, request := range []FocusNextAgentRequest{
		{SourceZellijPaneID: "terminal_1"},
		{SourceZellijSession: "dashboard"},
		{SourceZellijSession: "   ", SourceZellijPaneID: "  "},
	} {
		store := &serviceListStore{Store: NewMemoryStore(nil)}
		runtimeService := &serviceFakeRuntime{}
		service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})

		if _, err := service.FocusNextAgent(context.Background(), request); !errors.Is(err, ErrAgentSourceRequired) {
			t.Fatalf("FocusNextAgent(%#v) error = %v, want %v", request, err, ErrAgentSourceRequired)
		}
		if store.listCalls != 0 || len(runtimeService.focused) != 0 {
			t.Fatalf("blank source listed or focused agents: list=%d focus=%d", store.listCalls, len(runtimeService.focused))
		}
	}
}

func TestServiceFocusNextAgentFailedFocusDoesNotAdvanceCursor(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	focusFailure := errors.New("focus failed")
	runtimeService := &serviceFakeRuntime{
		focusResponse: runtime.FocusPaneResponse{Pane: runtime.Pane{ID: "pane-1", Status: runtime.PaneStatusRunning}},
	}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1"}

	if _, err := service.FocusAgent(context.Background(), FocusAgentRequest{
		AgentID: "agent-1", SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	}); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	runtimeService.focusErr = focusFailure
	if _, err := service.FocusNextAgent(context.Background(), request); !errors.Is(err, focusFailure) {
		t.Fatalf("first FocusNextAgent() error = %v, want %v", err, focusFailure)
	}
	if service.lastFocusedID != "agent-1" {
		t.Fatalf("cursor after failed FocusNextAgent() = %q, want agent-1", service.lastFocusedID)
	}
	runtimeService.focusErr = nil
	response, err := service.FocusNextAgent(context.Background(), request)
	if err != nil {
		t.Fatalf("second FocusNextAgent() error = %v", err)
	}
	if !response.Focused || response.Agent.Agent.ID != "agent-2" {
		t.Fatalf("second FocusNextAgent() response = %#v, want focused agent-2", response)
	}
}

func TestServiceFocusNextAgentSerializesConcurrentSelection(t *testing.T) {
	store := NewMemoryStore(nil)
	seedFocusRecords(t, store)
	firstFocusStarted := make(chan struct{})
	secondCallStarted := make(chan struct{})
	secondFocusStarted := make(chan struct{})
	releaseFirstFocus := make(chan struct{})
	var focusCalls atomic.Int32
	runtimeService := &serviceFakeRuntime{focusFn: func(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
		switch focusCalls.Add(1) {
		case 1:
			close(firstFocusStarted)
			<-releaseFirstFocus
		case 2:
			close(secondFocusStarted)
		}
		return runtime.FocusPaneResponse{Pane: runtime.Pane{ID: request.PaneID, Status: runtime.PaneStatusRunning}}, nil
	}}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1"}
	type result struct {
		response FocusNextAgentResponse
		err      error
	}
	results := make(chan result, 2)

	go func() {
		response, err := service.FocusNextAgent(context.Background(), request)
		results <- result{response: response, err: err}
	}()
	select {
	case <-firstFocusStarted:
	case <-time.After(time.Second):
		close(releaseFirstFocus)
		t.Fatal("first FocusNextAgent() did not reach runtime focus")
	}
	go func() {
		close(secondCallStarted)
		response, err := service.FocusNextAgent(context.Background(), request)
		results <- result{response: response, err: err}
	}()
	select {
	case <-secondCallStarted:
	case <-time.After(time.Second):
		close(releaseFirstFocus)
		t.Fatal("second FocusNextAgent() did not start")
	}
	select {
	case <-secondFocusStarted:
		close(releaseFirstFocus)
		t.Fatal("second runtime focus started before first runtime focus was released")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirstFocus)
	select {
	case <-secondFocusStarted:
	case <-time.After(time.Second):
		t.Fatal("second runtime focus did not start after first runtime focus was released")
	}

	gotIDs := make([]ID, 0, 2)
	for index := 0; index < 2; index++ {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("FocusNextAgent() error = %v", result.err)
			}
			if !result.response.Focused {
				t.Fatalf("FocusNextAgent() response = %#v, want focused result", result.response)
			}
			gotIDs = append(gotIDs, result.response.Agent.Agent.ID)
		case <-time.After(time.Second):
			t.Fatal("FocusNextAgent() result timed out")
		}
	}
	if !((gotIDs[0] == "agent-1" && gotIDs[1] == "agent-2") || (gotIDs[0] == "agent-2" && gotIDs[1] == "agent-1")) {
		t.Fatalf("concurrent FocusNextAgent() IDs = %v, want agent-1 and agent-2", gotIDs)
	}
}

func seedFocusRecords(t *testing.T, store Store) {
	t.Helper()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	for _, record := range []Record{
		{ID: "agent-3", Kind: KindGemini, PaneID: "pane-3", State: StateIdle, CreatedAt: base.Add(2 * time.Second), StateChangedAt: base.Add(2 * time.Second)},
		{ID: "agent-1", Kind: KindCodex, PaneID: "pane-1", State: StateIdle, CreatedAt: base, StateChangedAt: base},
		{ID: "agent-2", Kind: KindClaude, PaneID: "pane-2", State: StateIdle, CreatedAt: base.Add(time.Second), StateChangedAt: base.Add(time.Second)},
	} {
		if _, err := store.Create(record); err != nil {
			t.Fatalf("Create(%q) error = %v", record.ID, err)
		}
	}
}

func seedRecordsWithStates(t *testing.T, store Store, states []State) {
	t.Helper()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	kinds := []Kind{KindCodex, KindClaude, KindGemini, KindCursor}
	for index, state := range states {
		id := ID(fmt.Sprintf("agent-%d", index+1))
		_, err := store.Create(Record{
			ID:             id,
			Kind:           kinds[index%len(kinds)],
			PaneID:         runtime.PaneID(fmt.Sprintf("pane-%d", index+1)),
			State:          state,
			CreatedAt:      base.Add(time.Duration(index) * time.Second),
			StateChangedAt: base.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatalf("Create(%q) error = %v", id, err)
		}
	}
}

func successfulFocusRuntime() *serviceFakeRuntime {
	return &serviceFakeRuntime{focusFn: func(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
		return runtime.FocusPaneResponse{Pane: runtime.Pane{ID: request.PaneID, Status: runtime.PaneStatusRunning}}, nil
	}}
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
	claimFn       func(context.Context, runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error)
	claimErr      error
	claimed       []runtime.ClaimPaneRequest
	cleanupFn     func(context.Context, runtime.CleanupRequest) (runtime.CleanupResponse, error)
	cleanupErr    error
	cleaned       []runtime.CleanupRequest
	listFn        func(context.Context) (runtime.ListPanesResponse, error)
	listResponse  runtime.ListPanesResponse
	listErr       error
	listed        int
	focusResponse runtime.FocusPaneResponse
	focusErr      error
	focused       []runtime.FocusPaneRequest
	focusFn       func(context.Context, runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error)
}

func (f *serviceFakeRuntime) ClaimPane(ctx context.Context, request runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error) {
	f.claimed = append(f.claimed, request)
	if f.claimFn != nil {
		return f.claimFn(ctx, request)
	}
	if f.claimErr != nil {
		return runtime.ClaimPaneResponse{}, f.claimErr
	}
	return runtime.ClaimPaneResponse{Pane: runtime.Pane{
		ID: request.ID, AgentID: request.AgentID, Role: request.Role,
		SessionID:    runtime.SessionID(request.ZellijSession),
		ZellijPaneID: request.ZellijPaneID,
		Command:      append([]string(nil), request.Command...), CWD: request.CWD,
	}}, nil
}

func (f *serviceFakeRuntime) Cleanup(ctx context.Context, request runtime.CleanupRequest) (runtime.CleanupResponse, error) {
	f.cleaned = append(f.cleaned, request)
	if f.cleanupFn != nil {
		return f.cleanupFn(ctx, request)
	}
	return runtime.CleanupResponse{}, f.cleanupErr
}

func (f *serviceFakeRuntime) ListPanes(ctx context.Context) (runtime.ListPanesResponse, error) {
	f.listed++
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return f.listResponse, f.listErr
}

func (f *serviceFakeRuntime) FocusPane(ctx context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
	f.focused = append(f.focused, request)
	if f.focusFn != nil {
		return f.focusFn(ctx, request)
	}
	return f.focusResponse, f.focusErr
}

type serviceListStore struct {
	Store
	listCalls int
}

func (s *serviceListStore) List() ([]Record, error) {
	s.listCalls++
	return s.Store.List()
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
	if got := runtimeService.claimed[0].CWD; got != wantCWD {
		t.Fatalf("ClaimPane CWD = %q, want %q", got, wantCWD)
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
