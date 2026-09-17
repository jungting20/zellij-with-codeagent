package runtime

import (
	"context"
	"errors"
	"testing"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

type worktreeBackend struct {
	*fakeBackend
	ensured   []string
	ensureErr error
}

type delayedTabBackend struct {
	*fakeBackend
	polls int
}

func (b *delayedTabBackend) ListPanes(context.Context, zellij.ListPanesRequest) ([]zellij.Pane, error) {
	b.polls++
	if b.polls < 3 {
		return nil, nil
	}
	return []zellij.Pane{{ID: "terminal_2", TabID: 7}}, nil
}

func TestNewTabWaitsForTerminalPane(t *testing.T) {
	backend := &delayedTabBackend{fakeBackend: &fakeBackend{createTabID: 7}}
	service := NewService(Options{Backend: backend})
	response, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "delayed-child", ZellijSession: "worktree-agent", NewTab: true})
	if err != nil || response.Pane.ZellijPaneID != "terminal_2" || backend.polls != 3 || len(backend.closeTabRequests) != 0 {
		t.Fatalf("response = %+v, error = %v, polls = %d", response, err, backend.polls)
	}
}

func (b *worktreeBackend) EnsureSession(_ context.Context, session string) error {
	b.ensured = append(b.ensured, session)
	return b.ensureErr
}

func TestWorktreeChildTabUsesParentTitleAcrossSessions(t *testing.T) {
	for _, title := range []string{"부모 작업 pane", ""} {
		t.Run(title, func(t *testing.T) {
			backend := &worktreeBackend{fakeBackend: &fakeBackend{
				createTabID: 7,
				listPanesBySession: map[string][]zellij.Pane{
					"source":         {{ID: "terminal_1", Title: title, TabID: 3}},
					"worktree-agent": {{ID: "terminal_2", TabID: 7}},
				},
			}}
			reg := registry.New()
			if _, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "parent", SessionID: "source", ZellijPaneID: "terminal_1", CWD: "/repo/parent-project", Status: registry.PaneStatusRunning}); err != nil {
				t.Fatal(err)
			}
			service := NewService(Options{Registry: reg, Backend: backend})
			response, err := service.CreatePane(context.Background(), CreatePaneRequest{
				ID: "child", ParentPaneID: "parent", ZellijSession: "worktree-agent",
				NewTab: true, EnsureSession: true, CWD: "/tmp/worktree", Command: []string{"codex"},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantName := title
			if wantName == "" {
				wantName = "parent-project"
			}
			if len(backend.ensured) != 1 || backend.ensured[0] != "worktree-agent" || len(backend.createRequests) != 0 || len(backend.createTabRequests) != 1 {
				t.Fatalf("unexpected creation: %+v", backend)
			}
			req := backend.createTabRequests[0]
			if req.Session != "worktree-agent" || req.Name != wantName || req.CWD != "/tmp/worktree" || req.Command[0] != "codex" {
				t.Fatalf("tab request = %+v", req)
			}
			if response.Pane.ParentPaneID != "parent" || response.Pane.SessionID != "worktree-agent" || response.Pane.TabName != wantName {
				t.Fatalf("pane = %+v", response.Pane)
			}
		})
	}
}

func TestSessionCreationFailureDoesNotCreateTab(t *testing.T) {
	backend := &worktreeBackend{fakeBackend: &fakeBackend{}, ensureErr: errors.New("cannot create session")}
	service := NewService(Options{Backend: backend})
	_, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "child", ZellijSession: "worktree-agent", NewTab: true, EnsureSession: true})
	if !errors.Is(err, backend.ensureErr) || len(backend.createTabRequests) != 0 || len(service.registry.ListPanes()) != 0 {
		t.Fatalf("error = %v, tabs = %+v", err, backend.createTabRequests)
	}
}
