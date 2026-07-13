package dashboard

import (
	"reflect"
	"testing"

	"zellij-with-codeagent/internal/transport"
)

func TestBuildTreeSortsHierarchyAndGroupsMissingIDs(t *testing.T) {
	panes := []transport.Pane{
		{ID: "pane-b", SessionID: "session-b", TaskID: "task-b", TabID: "tab-2", TabName: "work"},
		{ID: "pane-a", SessionID: "session-a", TaskID: "task-a", TabID: "tab-1", TabName: "code"},
		{ID: "pane-c"},
	}
	tree := buildTree(panes)
	rows := flattenTree(tree, defaultExpanded(tree))
	var got []string
	for _, row := range rows {
		got = append(got, row.node.kind+":"+row.node.label)
	}
	want := []string{
		"session:session-a", "task:task-a", "tab:code (tab-1)", "pane:pane-a",
		"session:session-b", "task:task-b", "tab:work (tab-2)", "pane:pane-b",
		"session:ungrouped", "task:ungrouped", "tab:ungrouped", "pane:pane-c",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestFlattenTreeHonorsExpansion(t *testing.T) {
	tree := buildTree([]transport.Pane{{ID: "pane-a", SessionID: "s", TaskID: "t", TabID: "tab"}})
	rows := flattenTree(tree, map[string]bool{})
	if len(rows) != 1 || rows[0].node.kind != "session" {
		t.Fatalf("rows = %#v, want one collapsed session", rows)
	}
}

func TestBuildTreeCopiesPaneValues(t *testing.T) {
	panes := []transport.Pane{{ID: "pane-a", SessionID: "s", TaskID: "t", TabID: "tab"}}
	tree := buildTree(panes)
	panes[0].ID = "changed"
	rows := flattenTree(tree, defaultExpanded(tree))
	if got := rows[len(rows)-1].node.pane.ID; got != "pane-a" {
		t.Fatalf("pane id = %q, want copied pane-a", got)
	}
}
