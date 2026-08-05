package transport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

func TestAgentStartRequestRoundTripPreservesSourceAndArguments(t *testing.T) {
	payload := []byte(`{
		"kind": "codex",
			"cwd": "/workspace/project",
			"args": ["--model", "gpt-5"],
			"notify_on_idle": true,
			"source_session": "physical-a",
		"source_zellij_pane_id": "terminal_2"
	}`)
	var request StartAgentRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	converted := request.ToCodingAgent()
	if converted.Kind != codingagent.KindCodex || converted.CWD != "/workspace/project" || !converted.NotifyOnIdle || converted.SourceZellijSession != "physical-a" || converted.SourceZellijPaneID != "terminal_2" {
		t.Fatalf("StartAgentRequest.ToCodingAgent() = %#v", converted)
	}
	request.Args[0] = "mutated"
	if !reflect.DeepEqual(converted.ExtraArgs, []string{"--model", "gpt-5"}) {
		t.Fatalf("converted args = %#v, want cloned args", converted.ExtraArgs)
	}
	encoded, err := json.Marshal(StartAgentRequestFromCodingAgent(converted))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"kind":"codex"`, `"cwd":"/workspace/project"`, `"args":["--model","gpt-5"]`, `"notify_on_idle":true`, `"source_session":"physical-a"`, `"source_zellij_pane_id":"terminal_2"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("marshaled payload = %s, missing %s", encoded, field)
		}
	}
}

func TestAgentResponseConversionPreservesRecordTimestampsAndPane(t *testing.T) {
	createdAt := time.Unix(10, 123)
	changedAt := time.Unix(20, 456)
	updatedAt := time.Unix(30, 789)
	response := codingagent.ListAgentsResponse{Agents: []codingagent.AgentWithPane{{
		Agent: codingagent.Record{
			ID: "agent-1", Kind: codingagent.KindClaude, PaneID: "agent-1",
			State: codingagent.StateBlocked, StateReason: "approval required", MatchedRule: "permission",
			CreatedAt: createdAt, StateChangedAt: changedAt,
		},
		Pane: rt.Pane{ID: "agent-1", ZellijPaneID: "terminal_7", CWD: "/workspace/project", Status: rt.PaneStatusRunning, CreatedAt: createdAt, UpdatedAt: updatedAt},
	}}}

	converted := ListAgentsFromCodingAgent(response)
	if len(converted.Agents) != 1 {
		t.Fatalf("agents = %#v", converted.Agents)
	}
	agent := converted.Agents[0]
	if agent.Agent.ID != "agent-1" || agent.Agent.Kind != "claude" || agent.Agent.State != "blocked" || agent.Agent.StateReason != "approval required" || agent.Agent.MatchedRule != "permission" {
		t.Fatalf("agent = %#v", agent.Agent)
	}
	if !agent.Agent.CreatedAt.Equal(createdAt) || !agent.Agent.StateChangedAt.Equal(changedAt) || agent.Pane.ZellijPaneID != "terminal_7" || !agent.Pane.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("converted = %#v, want timestamps and joined pane", agent)
	}
}

func TestEventAgentStateFieldsAndNonAgentOmitEmpty(t *testing.T) {
	when := time.Unix(40, 0)
	converted := EventFromRuntime(eventbus.Event{
		Type: eventbus.TypeAgentStateChanged, AgentID: "agent-1", PaneID: "agent-1",
		AgentKind: "codex", PreviousState: "idle", AgentState: "working",
		MatchedRule: "screen_working", Reason: "visible work", Time: when,
	})
	if converted.AgentKind != "codex" || converted.PreviousState != "idle" || converted.AgentState != "working" || converted.MatchedRule != "screen_working" || converted.Reason != "visible work" {
		t.Fatalf("EventFromRuntime() = %#v", converted)
	}
	summary := EventSummaryFromRuntime(rt.EventSummary{
		Type: eventbus.TypeAgentStateChanged, AgentID: "agent-1", PaneID: "agent-1",
		AgentKind: "codex", PreviousState: "idle", AgentState: "working",
		MatchedRule: "screen_working", Reason: "visible work", Time: when,
	})
	if summary.AgentKind != converted.AgentKind || summary.PreviousState != converted.PreviousState || summary.AgentState != converted.AgentState || summary.MatchedRule != converted.MatchedRule || summary.Reason != converted.Reason {
		t.Fatalf("EventSummaryFromRuntime() = %#v, want agent fields %#v", summary, converted)
	}
	nonAgent, err := json.Marshal(EventFromRuntime(eventbus.Event{Type: eventbus.TypeServerReady, Time: when}))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"agent_kind", "previous_state", "agent_state", "matched_rule", "reason"} {
		if strings.Contains(string(nonAgent), field) {
			t.Fatalf("non-agent event = %s, want %s omitted", nonAgent, field)
		}
	}
}

func TestExecutionPlanPayloadJSONIncludesZellijSession(t *testing.T) {
	payload, err := json.Marshal(ExecutionPlanPayload{ZellijSession: "physical-a"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(payload), `"zellij_session":"physical-a"`) {
		t.Fatalf("marshaled payload = %s, want zellij_session", payload)
	}
}

func TestCreatePaneRequestToRuntimePreservesPayloadFields(t *testing.T) {
	tabID := 7
	source := CreatePaneRequest{
		ID:                    "pane-1",
		TaskID:                "task-1",
		AgentID:               "agent-1",
		Role:                  "coder",
		Name:                  "worker",
		ZellijSession:         "physical-a",
		NewTab:                true,
		TabName:               "main",
		ZellijTabID:           &tabID,
		Command:               []string{"go", "test"},
		CWD:                   "/tmp/work",
		InitialInput:          "implement ticket\n",
		InitialInputReadyText: "›",
	}

	converted := source.ToRuntime()

	if converted.ID != "pane-1" ||
		converted.TaskID != "task-1" ||
		converted.AgentID != "agent-1" ||
		converted.Role != "coder" ||
		converted.Name != "worker" ||
		converted.ZellijSession != "physical-a" ||
		!converted.NewTab ||
		converted.TabName != "main" ||
		converted.ZellijTabID == nil ||
		*converted.ZellijTabID != rt.ZellijTabID(tabID) ||
		converted.CWD != "/tmp/work" ||
		converted.InitialInput != "implement ticket\n" ||
		converted.InitialInputReadyText != "›" {
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

func TestSendMessageRequestToRuntimePreservesPayload(t *testing.T) {
	converted := SendMessageRequest{
		From: "planner",
		To:   "tester",
		Type: "task_request",
		Body: "run tests",
	}.ToRuntime()

	if converted.FromPaneID != "planner" || converted.ToPaneID != "tester" || converted.Type != "task_request" || converted.Body != "run tests" {
		t.Fatalf("SendMessageRequest.ToRuntime() = %#v, want payload fields preserved", converted)
	}
}

func TestExecutionPlanPayloadToRuntimePreservesNestedPayload(t *testing.T) {
	source := ExecutionPlanPayload{
		Session:       "feature-auth",
		ZellijSession: "physical-a",
		Layout:        "triple-horizontal",
		Tabs: []ExecutionPlanTab{{
			Name:         "frontend",
			LayoutString: `layout { pane; }`,
			Panes: []ExecutionPlanPane{{
				ID:                    "planner",
				Role:                  "planner",
				AgentID:               "agent-1",
				Command:               []string{"npm", "test"},
				CWD:                   "/tmp/app",
				InitialInput:          "inspect the auth flow",
				InitialInputReadyText: "›",
			}},
		}},
	}

	converted := source.ToRuntime("req-1")

	if converted.RequestID != "req-1" || converted.Session != "feature-auth" || converted.ZellijSession != "physical-a" || converted.Layout != "triple-horizontal" {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() = %#v, want envelope fields preserved", converted)
	}
	if len(converted.Tabs) != 1 || converted.Tabs[0].Name != "frontend" || len(converted.Tabs[0].Panes) != 1 {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() tabs = %#v, want nested tab and pane", converted.Tabs)
	}
	if converted.Tabs[0].LayoutString != `layout { pane; }` {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() LayoutString = %q", converted.Tabs[0].LayoutString)
	}
	pane := converted.Tabs[0].Panes[0]
	if pane.ID != "planner" ||
		pane.Role != "planner" ||
		pane.AgentID != "agent-1" ||
		pane.CWD != "/tmp/app" ||
		pane.InitialInput != "inspect the auth flow" ||
		pane.InitialInputReadyText != "›" {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() pane = %#v, want payload fields preserved", pane)
	}
	source.Tabs[0].Panes[0].Command[0] = "mutated"
	if !reflect.DeepEqual(pane.Command, []string{"npm", "test"}) {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() command = %#v, want cloned command", pane.Command)
	}
}

func TestExecutionPlanTabJSONUsesOptionalLayoutString(t *testing.T) {
	withLayout, err := json.Marshal(ExecutionPlanTab{Name: "ticket-worker", LayoutString: `layout { pane; }`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withLayout), `"layout_string":"layout { pane; }"`) {
		t.Fatalf("marshaled tab = %s, want layout_string", withLayout)
	}

	withoutLayout, err := json.Marshal(ExecutionPlanTab{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutLayout), "layout_string") {
		t.Fatalf("marshaled tab = %s, want layout_string omitted", withoutLayout)
	}
}

func TestExecutionPlanPayloadToRuntimePreservesZellijSessionWithNilTabs(t *testing.T) {
	converted := (ExecutionPlanPayload{
		Session:       "feature-auth",
		ZellijSession: "physical-a",
		Tabs:          nil,
	}).ToRuntime("req-1")

	if converted.ZellijSession != "physical-a" {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() ZellijSession = %q, want physical-a", converted.ZellijSession)
	}
	if converted.Tabs != nil {
		t.Fatalf("ExecutionPlanPayload.ToRuntime() tabs = %#v, want nil", converted.Tabs)
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
