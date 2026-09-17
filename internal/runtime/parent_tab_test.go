package runtime

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"zellij-with-codeagent/internal/persistence"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func parentTabBackend() *worktreeBackend {
	return &worktreeBackend{fakeBackend: &fakeBackend{
		createTabID: 7, createID: "terminal_3",
		listPanesBySession: map[string][]zellij.Pane{
			"source":         {{ID: "terminal_1", Title: "same title", TabID: 1}, {ID: "terminal_4", Title: "same title", TabID: 1}},
			"worktree-agent": {{ID: "terminal_2", TabID: 7, TabName: "same title"}, {ID: "terminal_3", TabID: 7, TabName: "same title"}},
		},
	}}
}
func registerTabParent(t *testing.T, reg *registry.Registry, id registry.PaneID) {
	t.Helper()
	physical := registry.ZellijPaneID("terminal_1")
	if id == "other-parent" {
		physical = "terminal_4"
	}
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: id, SessionID: "source", ZellijPaneID: physical, CWD: "/repo", Status: registry.PaneStatusRunning}); err != nil {
		t.Fatal(err)
	}
}
func groupedChild(id, parent PaneID) CreatePaneRequest {
	return CreatePaneRequest{ID: id, ParentPaneID: parent, ZellijSession: "worktree-agent", NewTab: true, EnsureSession: true, ReuseParentTab: true, Command: []string{"codex"}}
}
func TestSiblingWorktreesShareTabAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	writer, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.NewPersistent(writer)
	if err != nil {
		t.Fatal(err)
	}
	registerTabParent(t, reg, "parent")
	backend := parentTabBackend()
	service := NewService(Options{Registry: reg, Backend: backend})
	if _, err := service.CreatePane(ctx, groupedChild("first", "parent")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	writer, err = persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(ctx)
	reg, err = registry.NewPersistent(writer)
	if err != nil {
		t.Fatal(err)
	}
	service = NewService(Options{Registry: reg, Backend: backend})
	result, err := service.CreatePane(ctx, groupedChild("second", "parent"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.createTabRequests) != 1 || len(backend.createRequests) != 1 || backend.createRequests[0].TabID == nil || *backend.createRequests[0].TabID != 7 || result.Pane.ParentPaneID != "parent" {
		t.Fatalf("tabs=%+v panes=%+v result=%+v", backend.createTabRequests, backend.createRequests, result)
	}
	// Equal titles do not combine children of distinct logical parents.
	registerTabParent(t, reg, "other-parent")
	backend.createTabID = 8
	backend.listPanesBySession["worktree-agent"] = append(backend.listPanesBySession["worktree-agent"], zellij.Pane{ID: "terminal_5", TabID: 8})
	if _, err := service.CreatePane(ctx, groupedChild("third", "other-parent")); err != nil {
		t.Fatal(err)
	}
	if len(backend.createTabRequests) != 2 {
		t.Fatal("different parents shared a tab")
	}
}
func TestConcurrentSiblingWorktreesCreateOnlyOneTab(t *testing.T) {
	reg := registry.New()
	registerTabParent(t, reg, "parent")
	backend := parentTabBackend()
	service := NewService(Options{Registry: reg, Backend: backend})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []PaneID{"first", "second"} {
		wg.Add(1)
		go func(id PaneID) {
			defer wg.Done()
			_, err := service.CreatePane(context.Background(), groupedChild(id, "parent"))
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(backend.createTabRequests) != 1 || len(backend.createRequests) != 1 {
		t.Fatalf("tabs=%d panes=%d", len(backend.createTabRequests), len(backend.createRequests))
	}
}
func TestParentTabUsesLiveLocationAndSkipsClosedSiblings(t *testing.T) {
	for _, status := range []registry.PaneStatus{registry.PaneStatusRunning, registry.PaneStatusClosed} {
		t.Run(string(status), func(t *testing.T) {
			reg := registry.New()
			registerTabParent(t, reg, "parent")
			oldTab := registry.ZellijTabID(5)
			_, err := reg.RegisterPane(registry.RegisterPaneRequest{ID: "sibling", ParentPaneID: "parent", SessionID: "worktree-agent", ZellijPaneID: "terminal_2", ZellijTabID: &oldTab, Status: status})
			if err != nil {
				t.Fatal(err)
			}
			backend := parentTabBackend()
			service := NewService(Options{Registry: reg, Backend: backend})
			result, err := service.CreatePane(context.Background(), groupedChild("new", "parent"))
			if err != nil {
				t.Fatal(err)
			}
			if result.Pane.ZellijTabID == nil || *result.Pane.ZellijTabID != 7 {
				t.Fatalf("%+v", result)
			}
			if status == registry.PaneStatusRunning && len(backend.createTabRequests) != 0 {
				t.Fatal("live sibling not reused")
			}
			if status == registry.PaneStatusClosed && len(backend.createTabRequests) != 1 {
				t.Fatal("closed sibling reused")
			}
		})
	}
}
