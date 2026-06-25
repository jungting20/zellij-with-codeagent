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
	Goal        string
	CWD         string
	Session     string
	RoleCommand []string
	AutoTest    bool
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
		Session: session,
		Layout:  "triple-horizontal",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name: session,
				Panes: []transport.ExecutionPlanPane{
					{
						ID:      "coder",
						Role:    "coding-agent",
						CWD:     cwd,
						Command: append(append([]string{}, roleCommand...), "coding-agent", cwd),
					},
					{
						ID:      "test",
						Role:    "test-runner",
						CWD:     cwd,
						Command: []string{"sh", "-lc", testScript(req.AutoTest)},
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
						Command: []string{"sh", "-lc", notesScript(session, cwd, goal)},
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

func testScript(autoTest bool) string {
	if autoTest {
		return strings.Join([]string{
			"printf '$ go test ./...\\n'",
			"go test ./...",
			"status=$?",
			"printf 'go test finished with exit=%s\\n' \"$status\"",
			"exec sh",
		}, "\n")
	}
	return strings.Join([]string{
		"printf 'Suggested test command: go test ./...\\n'",
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

func notesScript(session, cwd, goal string) string {
	return strings.Join([]string{
		"printf 'Session: %s\\n' " + shellQuote(session),
		"printf 'CWD: %s\\n' " + shellQuote(cwd),
		"printf 'Goal: %s\\n' " + shellQuote(goal),
		"printf '\\nUseful commands:\\n'",
		"printf 'zellij-agent ctl status\\n'",
		"printf 'zellij-agent ctl events --limit 20\\n'",
		"printf 'zellij-agent ctl snapshot coder --full\\n'",
		"printf 'zellij-agent ctl cleanup --task %s\\n' " + shellQuote(session),
		"exec sh",
	}, "\n")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
