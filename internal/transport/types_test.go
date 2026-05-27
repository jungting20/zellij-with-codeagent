package transport

import (
	"testing"

	rt "zellij-with-codeagent/internal/runtime"
)

func TestPaneFromRuntimeIncludesLogicalHierarchyAndClonesCommand(t *testing.T) {
	source := rt.Pane{
		ID:           "pane-1",
		SessionID:    "session-1",
		TabID:        "tab-1",
		TaskID:       "task-1",
		AgentID:      "agent-1",
		ZellijPaneID: "terminal_1",
		Role:         "coder",
		Command:      []string{"go", "test"},
		Status:       rt.PaneStatusRunning,
	}

	converted := PaneFromRuntime(source)

	if converted.SessionID != "session-1" || converted.TabID != "tab-1" {
		t.Fatalf("PaneFromRuntime() hierarchy = (%q, %q), want session-1 tab-1", converted.SessionID, converted.TabID)
	}
	source.Command[0] = "mutated"
	if converted.Command[0] != "go" {
		t.Fatalf("PaneFromRuntime() command was aliased: %#v", converted.Command)
	}
}

func TestSessionFromRuntimeSortsTabsAndPanes(t *testing.T) {
	session := rt.SessionRecord{
		ID: "session-1",
		Tabs: map[rt.TabID]rt.TabRecord{
			"tab-b": {
				ID:   "tab-b",
				Name: "B",
				Panes: map[rt.PaneID]rt.PaneRecord{
					"pane-2": {ID: "pane-2", SessionID: "session-1", TabID: "tab-b", Status: rt.PaneStatusRunning},
					"pane-1": {ID: "pane-1", SessionID: "session-1", TabID: "tab-b", Status: rt.PaneStatusRunning},
				},
			},
			"tab-a": {
				ID:   "tab-a",
				Name: "A",
				Panes: map[rt.PaneID]rt.PaneRecord{
					"pane-4": {ID: "pane-4", SessionID: "session-1", TabID: "tab-a", Status: rt.PaneStatusRunning},
					"pane-3": {ID: "pane-3", SessionID: "session-1", TabID: "tab-a", Status: rt.PaneStatusRunning},
				},
			},
		},
	}

	converted := SessionFromRuntime(session)

	if len(converted.Tabs) != 2 || converted.Tabs[0].ID != "tab-a" || converted.Tabs[1].ID != "tab-b" {
		t.Fatalf("SessionFromRuntime() tabs = %#v, want sorted by id", converted.Tabs)
	}
	if got := converted.Tabs[0].Panes; len(got) != 2 || got[0].ID != "pane-3" || got[1].ID != "pane-4" {
		t.Fatalf("SessionFromRuntime() panes in tab-a = %#v, want sorted by id", got)
	}
}
