package transport

import (
	"reflect"
	"testing"

	rt "zellij-with-codeagent/internal/runtime"
)

func TestCreatePaneRequestToRuntimePreservesPayloadFields(t *testing.T) {
	tabID := 7
	source := CreatePaneRequest{
		ID:          "pane-1",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		Role:        "coder",
		Name:        "worker",
		NewTab:      true,
		TabName:     "main",
		ZellijTabID: &tabID,
		Command:     []string{"go", "test"},
		CWD:         "/tmp/work",
	}

	converted := source.ToRuntime()

	if converted.ID != "pane-1" ||
		converted.TaskID != "task-1" ||
		converted.AgentID != "agent-1" ||
		converted.Role != "coder" ||
		converted.Name != "worker" ||
		!converted.NewTab ||
		converted.TabName != "main" ||
		converted.ZellijTabID == nil ||
		*converted.ZellijTabID != rt.ZellijTabID(tabID) ||
		converted.CWD != "/tmp/work" {
		t.Fatalf("CreatePaneRequest.ToRuntime() = %#v, want all scalar fields preserved", converted)
	}
	source.Command[0] = "mutated"
	if !reflect.DeepEqual(converted.Command, []string{"go", "test"}) {
		t.Fatalf("CreatePaneRequest.ToRuntime() command = %#v, want cloned command", converted.Command)
	}
}

func TestCleanupRequestToRuntimeFiltersEmptyPaneIDs(t *testing.T) {
	converted := CleanupRequest{
		PaneIDs: []string{"pane-1", "", "pane-2"},
		TaskID:  "task-1",
		Role:    "coder",
	}.ToRuntime()

	if !reflect.DeepEqual(converted.PaneIDs, []rt.PaneID{"pane-1", "pane-2"}) {
		t.Fatalf("CleanupRequest.ToRuntime() pane ids = %#v, want non-empty ids only", converted.PaneIDs)
	}
	if converted.TaskID != "task-1" || converted.Role != "coder" {
		t.Fatalf("CleanupRequest.ToRuntime() = %#v, want task and role preserved", converted)
	}
}

func TestExecutionPlanPayloadToRuntimePreservesNestedPayload(t *testing.T) {
	source := ExecutionPlanPayload{
		Session: "feature-auth",
		Layout:  "triple-horizontal",
		Tabs: []ExecutionPlanTab{{
			Name: "frontend",
			Panes: []ExecutionPlanPane{{
				ID:      "planner",
				Role:    "planner",
				AgentID: "agent-1",
				Command: []string{"npm", "test"},
				CWD:     "/tmp/app",
			}},
		}},
	}

	converted := source.ToRuntime("req-1")

	if converted.RequestID != "req-1" || converted.Session != "feature-auth" || converted.Layout != "triple-horizontal" {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() = %#v, want envelope fields preserved", converted)
	}
	if len(converted.Tabs) != 1 || converted.Tabs[0].Name != "frontend" || len(converted.Tabs[0].Panes) != 1 {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() tabs = %#v, want nested tab and pane", converted.Tabs)
	}
	pane := converted.Tabs[0].Panes[0]
	if pane.ID != "planner" || pane.Role != "planner" || pane.AgentID != "agent-1" || pane.CWD != "/tmp/app" {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() pane = %#v, want payload fields preserved", pane)
	}
	source.Tabs[0].Panes[0].Command[0] = "mutated"
	if !reflect.DeepEqual(pane.Command, []string{"npm", "test"}) {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() command = %#v, want cloned command", pane.Command)
	}
}

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
