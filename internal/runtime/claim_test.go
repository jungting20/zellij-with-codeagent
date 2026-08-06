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

func TestClaimPaneRegistersExistingZellijPaneWithoutMutation(t *testing.T) {
	backend := &fakeBackend{listPanes: []zellij.Pane{{
		ID: "terminal_2", TabID: 7, TabName: "work", Command: "zsh",
	}}}
	observer := &recordingPaneObserver{}
	service := NewService(Options{Registry: registry.New(), Backend: backend, PaneObserver: observer})

	response, err := service.ClaimPane(context.Background(), ClaimPaneRequest{
		ID: "agent-1", AgentID: "agent-1", Role: "coding-agent",
		ZellijSession: "session-a", ZellijPaneID: "terminal_2",
		Command: []string{"codex", "--dangerously-bypass-approvals-and-sandbox"},
		CWD:     "/workspace",
	})
	if err != nil {
		t.Fatalf("ClaimPane() error = %v", err)
	}

	if response.Pane.ID != "agent-1" || response.Pane.ZellijPaneID != "terminal_2" {
		t.Fatalf("Pane IDs = %q/%q, want agent-1/terminal_2", response.Pane.ID, response.Pane.ZellijPaneID)
	}
	if response.Pane.OwnershipToken == "" {
		t.Fatal("claimed pane ownership token is empty")
	}
	if response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != 7 || response.Pane.TabID != "7" || response.Pane.TabName != "work" {
		t.Fatalf("Pane tab = %#v, want Zellij tab 7 named work", response.Pane)
	}
	if got, want := response.Pane.Command, []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Pane command = %#v, want %#v", got, want)
	}
	if response.Pane.CWD != "/workspace" {
		t.Fatalf("Pane CWD = %q, want /workspace", response.Pane.CWD)
	}

	backend.mu.Lock()
	createCalls := len(backend.createRequests)
	createTabCalls := len(backend.createTabRequests)
	sendCalls := len(backend.sendRequests)
	backend.mu.Unlock()
	if createCalls != 0 || createTabCalls != 0 || sendCalls != 0 {
		t.Fatalf("backend mutations = create:%d tab:%d input:%d, want none", createCalls, createTabCalls, sendCalls)
	}
	observer.mu.Lock()
	opened := append([]registry.PaneRecord(nil), observer.opened...)
	observer.mu.Unlock()
	if len(opened) != 1 || opened[0].ID != "agent-1" {
		t.Fatalf("PaneOpened records = %#v, want claimed agent-1", opened)
	}
}

func TestClaimPaneRejectsInvalidOrUnresolvablePhysicalTarget(t *testing.T) {
	tests := []struct {
		name  string
		req   ClaimPaneRequest
		panes []zellij.Pane
		want  error
	}{
		{name: "blank session", req: ClaimPaneRequest{ID: "agent-1", ZellijSession: "  ", ZellijPaneID: "terminal_2"}, want: ErrZellijSessionRequired},
		{name: "blank physical ID", req: ClaimPaneRequest{ID: "agent-1", ZellijSession: "session-a", ZellijPaneID: "  "}, want: ErrInvalidPaneTarget},
		{name: "missing pane", req: ClaimPaneRequest{ID: "agent-1", ZellijSession: "session-a", ZellijPaneID: "terminal_2"}, want: ErrPaneNotFound},
		{name: "duplicate matches", req: ClaimPaneRequest{ID: "agent-1", ZellijSession: "session-a", ZellijPaneID: "terminal_2"}, panes: []zellij.Pane{{ID: "terminal_2", TabID: 7}, {ID: "terminal_2", TabID: 8}}, want: ErrInvalidPaneTarget},
		{name: "plugin pane", req: ClaimPaneRequest{ID: "agent-1", ZellijSession: "session-a", ZellijPaneID: "terminal_2"}, panes: []zellij.Pane{{ID: "terminal_2", TabID: 7, IsPlugin: true}}, want: ErrInvalidPaneTarget},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestService(&fakeBackend{listPanes: test.panes})

			_, err := service.ClaimPane(context.Background(), test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("ClaimPane() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClaimPaneRejectsAlreadyManagedPhysicalPane(t *testing.T) {
	backend := &fakeBackend{listPanes: []zellij.Pane{{ID: "terminal_2", TabID: 7}}}
	service := newTestService(backend)
	if _, err := service.registry.RegisterPane(registry.RegisterPaneRequest{
		ID: "existing", SessionID: "session-a", ZellijPaneID: "terminal_2",
	}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}

	_, err := service.ClaimPane(context.Background(), ClaimPaneRequest{
		ID: "agent-1", ZellijSession: "session-a", ZellijPaneID: "terminal_2",
	})
	if !errors.Is(err, ErrInvalidPaneTarget) {
		t.Fatalf("ClaimPane() error = %v, want %v", err, ErrInvalidPaneTarget)
	}
}

func TestClaimPaneStartsSubscription(t *testing.T) {
	backend := &fakeBackend{listPanes: []zellij.Pane{{ID: "terminal_2", TabID: 7}}}
	service := NewService(Options{
		Registry:           registry.New(),
		Backend:            backend,
		EventBus:           eventbus.New(),
		SubscriptionRunner: &scriptedSubscriptionRunner{},
	})

	_, err := service.ClaimPane(context.Background(), ClaimPaneRequest{
		ID: "agent-1", ZellijSession: "session-a", ZellijPaneID: "terminal_2",
	})
	if err != nil {
		t.Fatalf("ClaimPane() error = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		backend.mu.Lock()
		requests := append([]zellij.SubscribeRequest(nil), backend.subscribeRequests...)
		backend.mu.Unlock()
		if len(requests) > 0 {
			if got := requests[0]; got.Session != "session-a" || got.PaneID != "terminal_2" || !got.JSON {
				t.Fatalf("Subscribe request = %#v, want session-a terminal_2 JSON", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for claimed pane subscription")
		}
		time.Sleep(time.Millisecond)
	}
}
