package registry

import (
	"context"
	"path/filepath"
	"testing"
	"zellij-with-codeagent/internal/persistence"
)

func TestPersistentHierarchyRestoresIndexesAndGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewPersistent(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []SessionID{"a", "b"} {
		_, err = reg.RegisterPane(RegisterPaneRequest{ID: PaneID(session), ParentPaneID: "source-parent", SessionID: session, TabID: "0", ZellijPaneID: "1", OwnershipToken: "owned", Command: []string{"codex"}, Status: PaneStatusRunning})
		if err != nil {
			t.Fatal(err)
		}
	}
	reg.UpdatePaneOutput("a", "must remain transient")
	reg.UpdatePaneStatus("a", PaneStatusError, "test error")
	removed, err := reg.RemovePane("b")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	w, err = persistence.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(context.Background())
	restored, err := NewPersistent(w)
	if err != nil {
		t.Fatal(err)
	}
	pane, err := restored.GetLatestByZellijPaneID("1")
	if err != nil {
		t.Fatal(err)
	}
	if pane.ParentPaneID != "source-parent" || pane.ID != "a" || pane.Status != PaneStatusError || pane.LastOutput != "" || pane.OwnershipToken != "owned" || pane.Command[0] != "codex" {
		t.Fatalf("restored pane=%+v", pane)
	}
	tab, err := restored.GetTab("b", "0")
	if err != nil || len(tab.Panes) != 0 {
		t.Fatal(tab, err)
	}
	next, err := restored.RegisterPane(RegisterPaneRequest{ID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation <= removed.Generation {
		t.Fatalf("generation=%d after deleted %d", next.Generation, removed.Generation)
	}
}
