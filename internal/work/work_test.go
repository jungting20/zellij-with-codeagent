package work

import (
	"strings"
	"testing"
)

func TestBuildPlanCreatesMixedWorkPanes(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:          "implement the mixed work command",
		CWD:           "/tmp/app",
		ZellijSession: "physical-a",
		RoleCommand:   []string{"/tmp/bin/zellij-agent", "role"},
		Project:       goProjectDetection(),
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if payload.Session == "" || !strings.HasPrefix(payload.Session, "work-implement-the-mixed-work-command-") {
		t.Fatalf("Session = %q, want stable work slug", payload.Session)
	}
	if payload.ZellijSession != "physical-a" {
		t.Fatalf("payload.ZellijSession = %q, want physical-a", payload.ZellijSession)
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
	if len(tab.Panes) != 5 {
		t.Fatalf("Panes = %#v, want five panes", tab.Panes)
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
	if panes[3].ID != "lazygit" || panes[3].Role != "lazygit" || panes[3].CWD != "/tmp/app" || panes[3].Command[0] != "sh" || panes[3].Command[1] != "-lc" {
		t.Fatalf("lazygit pane = %#v, want shell lazygit pane in cwd", panes[3])
	}
	if !strings.Contains(panes[3].Command[2], "lazygit") || !strings.Contains(panes[3].Command[2], "exec sh") {
		t.Fatalf("lazygit script = %q, want lazygit then interactive shell", panes[3].Command[2])
	}
	if panes[4].ID != "notes" || panes[4].Role != "notes" || panes[4].Command[0] != "sh" || panes[4].Command[1] != "-lc" {
		t.Fatalf("notes pane = %#v, want shell notes pane", panes[4])
	}
	if !strings.Contains(panes[4].Command[2], "zellij-agent ctl status") ||
		!strings.Contains(panes[4].Command[2], "zellij-agent ctl events --limit 20") ||
		!strings.Contains(panes[4].Command[2], "zellij-agent ctl snapshot coder --full") ||
		!strings.Contains(panes[4].Command[2], "zellij-agent ctl cleanup --task") {
		t.Fatalf("notes script = %q, want control command hints", panes[4].Command[2])
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
		Project:  goProjectDetection(),
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	script := payload.Tabs[0].Panes[1].Command[2]
	if !strings.Contains(script, "$ go test ./...") || !strings.Contains(script, "'go' 'test' './...'") || !strings.Contains(script, "finished with exit=%s") {
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

func TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal: "  fix the parser  ",
		CWD:  "/tmp/app",
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	panes := payload.Tabs[0].Panes
	if got := panes[0].InitialInput; got != "fix the parser" {
		t.Fatalf("coder InitialInput = %q, want exact trimmed goal", got)
	}
	if strings.HasSuffix(panes[0].InitialInput, "\n") {
		t.Fatalf("coder InitialInput = %q, want no newline", panes[0].InitialInput)
	}
	if got := panes[0].InitialInputReadyText; got != "›" {
		t.Fatalf("coder InitialInputReadyText = %q, want Codex prompt marker", got)
	}
	for _, pane := range panes[1:] {
		if pane.InitialInput != "" {
			t.Fatalf("pane %q InitialInput = %q, want coder-only prefill", pane.ID, pane.InitialInput)
		}
		if pane.InitialInputReadyText != "" {
			t.Fatalf("pane %q InitialInputReadyText = %q, want coder-only readiness", pane.ID, pane.InitialInputReadyText)
		}
	}
}

func TestBuildPlanUsesDetectedProjectCommands(t *testing.T) {
	tests := []struct {
		name            string
		project         ProjectDetection
		wantTestDisplay string
		wantBuild       string
	}{
		{
			name: "go",
			project: ProjectDetection{
				Profile:         ProjectProfileGo,
				Markers:         []string{"go.mod"},
				TestCommand:     []string{"go", "test", "./..."},
				BuildCommand:    []string{"go", "build", "./..."},
				FeedbackEnabled: true,
			},
			wantTestDisplay: "Suggested test command: go test ./...",
			wantBuild:       "Build command: go build ./...",
		},
		{
			name: "pnpm",
			project: ProjectDetection{
				Profile:         ProjectProfilePNPM,
				Markers:         []string{"package.json", "pnpm-lock.yaml"},
				TestCommand:     []string{"pnpm", "test"},
				BuildCommand:    []string{"pnpm", "build"},
				FeedbackEnabled: true,
			},
			wantTestDisplay: "Suggested test command: pnpm test",
			wantBuild:       "Build command: pnpm build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := BuildPlan(PlanRequest{
				Goal:    "ship it",
				CWD:     "/tmp/app",
				Project: tt.project,
			})
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			testScript := payload.Tabs[0].Panes[1].Command[2]
			notesScript := payload.Tabs[0].Panes[4].Command[2]
			if !strings.Contains(testScript, tt.wantTestDisplay) {
				t.Fatalf("test script = %q, want %q", testScript, tt.wantTestDisplay)
			}
			if !strings.Contains(notesScript, "Profile: "+string(tt.project.Profile)) ||
				!strings.Contains(notesScript, "Test command: "+strings.Join(tt.project.TestCommand, " ")) ||
				!strings.Contains(notesScript, tt.wantBuild) ||
				!strings.Contains(notesScript, "Feedback: enabled") {
				t.Fatalf("notes script = %q, want detected project preflight", notesScript)
			}
		})
	}
}

func TestBuildPlanAutoTestRunsDetectedCommand(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:     "run tests",
		CWD:      "/tmp/app",
		AutoTest: true,
		Project: ProjectDetection{
			Profile:         ProjectProfilePNPM,
			Markers:         []string{"package.json", "pnpm-lock.yaml"},
			TestCommand:     []string{"pnpm", "test"},
			FeedbackEnabled: true,
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	script := payload.Tabs[0].Panes[1].Command[2]
	if !strings.Contains(script, "$ pnpm test") || !strings.Contains(script, "'pnpm' 'test'") || !strings.Contains(script, "finished with exit=%s") {
		t.Fatalf("test script = %q, want detected pnpm test execution", script)
	}
}

func TestBuildPlanDisablesUnknownProjectFeedback(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:     "ship it",
		CWD:      "/tmp/app",
		AutoTest: true,
		Project: ProjectDetection{
			Profile:        ProjectProfileUnknown,
			DisabledReason: "project type not detected; use --test-command",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	testScript := payload.Tabs[0].Panes[1].Command[2]
	notesScript := payload.Tabs[0].Panes[4].Command[2]
	if !strings.Contains(testScript, "Feedback disabled: project type not detected; use --test-command") || strings.Contains(testScript, "go test") {
		t.Fatalf("test script = %q, want disabled feedback without hard-coded Go command", testScript)
	}
	if !strings.Contains(notesScript, "Feedback: disabled") || !strings.Contains(notesScript, "Reason: project type not detected; use --test-command") {
		t.Fatalf("notes script = %q, want disabled reason", notesScript)
	}
}

func goProjectDetection() ProjectDetection {
	return ProjectDetection{
		Profile:         ProjectProfileGo,
		Markers:         []string{"go.mod"},
		TestCommand:     []string{"go", "test", "./..."},
		BuildCommand:    []string{"go", "build", "./..."},
		FeedbackEnabled: true,
	}
}
