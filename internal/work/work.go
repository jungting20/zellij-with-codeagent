package work

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"

	"zellij-with-codeagent/internal/transport"
)

var ErrInvalidPlanRequest = errors.New("work: invalid plan request")

type PlanRequest struct {
	Goal          string
	CWD           string
	Session       string
	ZellijSession string
	RoleCommand   []string
	AutoTest      bool
	Project       ProjectDetection
}

func BuildPlan(req PlanRequest) (transport.ExecutionPlanPayload, error) {
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: goal is required", ErrInvalidPlanRequest)
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: cwd is required", ErrInvalidPlanRequest)
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		session = SessionFromGoal(goal)
	}
	roleCommand := normalizeRoleCommand(req.RoleCommand)

	return transport.ExecutionPlanPayload{
		Session:       session,
		ZellijSession: strings.TrimSpace(req.ZellijSession),
		Layout:        "triple-horizontal",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name: session,
				Panes: []transport.ExecutionPlanPane{
					{
						ID:                    "coder",
						Role:                  "coding-agent",
						CWD:                   cwd,
						Command:               append(append([]string{}, roleCommand...), "coding-agent", cwd),
						InitialInput:          goal,
						InitialInputReadyText: "›",
					},
					{
						ID:      "test",
						Role:    "test-runner",
						CWD:     cwd,
						Command: []string{"sh", "-lc", testScript(req.AutoTest, req.Project)},
					},
					{
						ID:      "review",
						Role:    "review-assistant",
						CWD:     cwd,
						Command: []string{"sh", "-lc", reviewScript(cwd, goal)},
					},
					{
						ID:      "lazygit",
						Role:    "lazygit",
						CWD:     cwd,
						Command: []string{"sh", "-lc", lazygitScript()},
					},
					{
						ID:      "notes",
						Role:    "notes",
						CWD:     cwd,
						Command: []string{"sh", "-lc", notesScript(session, cwd, goal, req.Project)},
					},
				},
			},
		},
	}, nil
}

func RequestID(session string) string {
	return "req_" + session
}

func SessionFromGoal(goal string) string {
	hash := hash8(goal)
	slug := asciiSlug(goal)
	if slug == "" {
		return "work-goal-" + hash
	}
	maxSlugLen := 64 - len("work--") - len(hash)
	if len(slug) > maxSlugLen {
		slug = strings.Trim(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		slug = "goal"
	}
	return "work-" + slug + "-" + hash
}

func normalizeRoleCommand(command []string) []string {
	var normalized []string
	for _, part := range command {
		part = strings.TrimSpace(part)
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	if len(normalized) == 0 {
		return []string{"zellij-agent", "role"}
	}
	return normalized
}

func asciiSlug(value string) string {
	var b strings.Builder
	previousDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			previousDash = false
		case r < unicode.MaxASCII:
			if !previousDash && b.Len() > 0 {
				b.WriteByte('-')
				previousDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func hash8(value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("%08x", h.Sum32())
}

func testScript(autoTest bool, project ProjectDetection) string {
	if !project.FeedbackEnabled || len(project.TestCommand) == 0 {
		reason := strings.TrimSpace(project.DisabledReason)
		if reason == "" {
			reason = "no test command resolved; use --test-command"
		}
		return strings.Join([]string{
			printLine("Feedback disabled: " + reason),
			"exec sh",
		}, "\n")
	}

	display := displayCommand(project.TestCommand)
	if autoTest {
		resultLabel := display
		if len(project.TestCommand) > 2 {
			resultLabel = displayCommand(project.TestCommand[:2])
		}
		return strings.Join([]string{
			printLine("$ " + display),
			shellCommand(project.TestCommand),
			"status=$?",
			"printf '%s finished with exit=%s\\n' " + shellQuote(resultLabel) + " \"$status\"",
			"exec sh",
		}, "\n")
	}
	return strings.Join([]string{
		printLine("Suggested test command: " + display),
		"exec sh",
	}, "\n")
}

func reviewScript(cwd, goal string) string {
	prompt := "Review this work plan for goal: " + goal + "\nDo not edit files. Report risks, bugs, and missing tests."
	return strings.Join([]string{
		"printf %s " + shellQuote(prompt) + " | codex exec --sandbox read-only --cd " + shellQuote(cwd) + " -",
		"exec sh",
	}, "\n")
}

func lazygitScript() string {
	return strings.Join([]string{
		"lazygit",
		"exec sh",
	}, "\n")
}

func notesScript(session, cwd, goal string, project ProjectDetection) string {
	markers := strings.Join(project.Markers, ", ")
	if markers == "" {
		markers = "(none)"
	}
	testCommand := displayCommand(project.TestCommand)
	if testCommand == "" {
		testCommand = "(none)"
	}
	buildCommand := displayCommand(project.BuildCommand)
	if buildCommand == "" {
		buildCommand = "(none)"
	}
	feedback := "disabled"
	if project.FeedbackEnabled && len(project.TestCommand) > 0 {
		feedback = "enabled"
	}

	lines := []string{
		"printf 'Session: %s\\n' " + shellQuote(session),
		"printf 'CWD: %s\\n' " + shellQuote(cwd),
		"printf 'Goal: %s\\n' " + shellQuote(goal),
		printLine("Profile: " + string(project.Profile)),
		printLine("Markers: " + markers),
		printLine("Test command: " + testCommand),
		printLine("Build command: " + buildCommand),
		printLine("Feedback: " + feedback),
	}
	if feedback == "disabled" && strings.TrimSpace(project.DisabledReason) != "" {
		lines = append(lines, printLine("Reason: "+project.DisabledReason))
	}
	lines = append(lines,
		"printf '\\nUseful commands:\\n'",
		"printf 'zellij-agent ctl status\\n'",
		"printf 'zellij-agent ctl events --limit 20\\n'",
		"printf 'zellij-agent ctl snapshot coder --full\\n'",
		"printf 'zellij-agent ctl cleanup --task %s\\n' "+shellQuote(session),
		"exec sh",
	)
	return strings.Join(lines, "\n")
}

func displayCommand(command []string) string {
	return strings.Join(command, " ")
}

func shellCommand(command []string) string {
	quoted := make([]string, 0, len(command))
	for _, arg := range command {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func printLine(value string) string {
	return "printf '%s\\n' " + shellQuote(value)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
