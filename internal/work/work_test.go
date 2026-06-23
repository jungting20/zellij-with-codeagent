package work

import (
	"strings"
	"testing"
)

func TestBuildPlanCreatesMixedWorkPanes(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:        "implement the mixed work command",
		CWD:         "/tmp/app",
		RoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if payload.Session == "" || !strings.HasPrefix(payload.Session, "work-implement-the-mixed-work-command-") {
		t.Fatalf("Session = %q, want stable work slug", payload.Session)
	}
	if payload.Layout != "triple-horizontal" {
		t.Fatalf("Layout = %q, want triple-horizontal", payload.Layout)
	}
	if len(payload.Tabs) != 1 {
		t.Fatalf("Tabs = %#v, want one tab", payload.Tabs)
	}
	tab := payload.Tabs[0]
	if tab.Name != payload.Session {
		t.Fatalf("Tab name = %q, want session %q", tab.Name, payload.Session)
	}
	if len(tab.Panes) != 4 {
		t.Fatalf("Panes = %#v, want four panes", tab.Panes)
	}

	panes := tab.Panes
	if panes[0].ID != "coder" || panes[0].Role != "coding-agent" || panes[0].CWD != "/tmp/app" {
		t.Fatalf("coder pane = %#v, want coding-agent in cwd", panes[0])
	}
	if got := panes[0].Command; len(got) != 4 || got[0] != "/tmp/bin/zellij-agent" || got[1] != "role" || got[2] != "coding-agent" || got[3] != "/tmp/app" {
		t.Fatalf("coder command = %#v, want zellij-agent role coding-agent /tmp/app", got)
	}
	if panes[1].ID != "test" || panes[1].Role != "test-runner" || panes[1].Command[0] != "sh" || panes[1].Command[1] != "-lc" {
		t.Fatalf("test pane = %#v, want shell test runner", panes[1])
	}
	if !strings.Contains(panes[1].Command[2], "go test ./...") || strings.Contains(panes[1].Command[2], "$ go test ./...") {
		t.Fatalf("test script = %q, want suggested test command without auto execution", panes[1].Command[2])
	}
	if panes[2].ID != "review" || panes[2].Role != "review-assistant" || panes[2].Command[0] != "sh" || panes[2].Command[1] != "-lc" {
		t.Fatalf("review pane = %#v, want shell review assistant", panes[2])
	}
	if !strings.Contains(panes[2].Command[2], "codex exec --sandbox read-only --cd '/tmp/app' -") || !strings.Contains(panes[2].Command[2], "Do not edit files") {
		t.Fatalf("review script = %q, want read-only codex exec review", panes[2].Command[2])
	}
	if panes[3].ID != "notes" || panes[3].Role != "notes" || panes[3].Command[0] != "sh" || panes[3].Command[1] != "-lc" {
		t.Fatalf("notes pane = %#v, want shell notes pane", panes[3])
	}
	if !strings.Contains(panes[3].Command[2], "zellij-agent ctl status") ||
		!strings.Contains(panes[3].Command[2], "zellij-agent ctl events --limit 20") ||
		!strings.Contains(panes[3].Command[2], "zellij-agent ctl snapshot coder --full") ||
		!strings.Contains(panes[3].Command[2], "zellij-agent ctl cleanup --task") {
		t.Fatalf("notes script = %q, want control command hints", panes[3].Command[2])
	}
}

func TestBuildPlanUsesExplicitSession(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:    "fix parser",
		CWD:     "/tmp/app",
		Session: "custom-session",
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if payload.Session != "custom-session" || payload.Tabs[0].Name != "custom-session" {
		t.Fatalf("payload session/tab = %q/%q, want custom-session", payload.Session, payload.Tabs[0].Name)
	}
	if got := RequestID(payload.Session); got != "req_custom-session" {
		t.Fatalf("RequestID() = %q, want req_custom-session", got)
	}
}

func TestBuildPlanAutoTestRunsGoTestOnce(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:     "run tests",
		CWD:      "/tmp/app",
		AutoTest: true,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	script := payload.Tabs[0].Panes[1].Command[2]
	if !strings.Contains(script, "printf '$ go test ./...\\n'") || !strings.Contains(script, "go test ./...") || !strings.Contains(script, "go test finished with exit=%s") {
		t.Fatalf("test script = %q, want auto go test execution and exit marker", script)
	}
}

func TestBuildPlanRejectsMissingInputs(t *testing.T) {
	if _, err := BuildPlan(PlanRequest{CWD: "/tmp/app"}); err == nil {
		t.Fatalf("BuildPlan() error = nil, want missing goal error")
	}
	if _, err := BuildPlan(PlanRequest{Goal: "ship it"}); err == nil {
		t.Fatalf("BuildPlan() error = nil, want missing cwd error")
	}
}

func TestSessionFromGoalHandlesNonASCII(t *testing.T) {
	got := SessionFromGoal("작업 기능 만들어줘")
	if !strings.HasPrefix(got, "work-goal-") {
		t.Fatalf("SessionFromGoal() = %q, want non-ASCII fallback slug", got)
	}
	if len(got) > 64 {
		t.Fatalf("SessionFromGoal() length = %d, want <= 64", len(got))
	}
}
