package chrome

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"zellij-with-codeagent/internal/transport"
)

var ErrInvalidPlanRequest = errors.New("chrome: invalid plan request")

type PlanRequest struct {
	CWD            string
	Session        string
	RoleCommand    []string
	TabNetworkArgs []string
	Now            func() time.Time
}

func BuildPlan(req PlanRequest) (transport.ExecutionPlanPayload, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: cwd is required", ErrInvalidPlanRequest)
	}

	session := strings.TrimSpace(req.Session)
	if session == "" {
		session = "chrome"
	}
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}

	roleCommand := normalizeRoleCommand(req.RoleCommand)
	command := append(append([]string{}, roleCommand...), "tab-network")
	command = append(command, req.TabNetworkArgs...)

	return transport.ExecutionPlanPayload{
		Session: session,
		Layout:  "single-tab",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name: "chrome",
				Panes: []transport.ExecutionPlanPane{
					{
						ID:      paneID(now()),
						Role:    "tab-network",
						CWD:     cwd,
						Command: command,
					},
				},
			},
		},
	}, nil
}

func RequestID(session string) string {
	return "req_" + session
}

func paneID(t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("chrome-tab-network-%s-%09d", t.Format("20060102-150405"), t.Nanosecond())
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
