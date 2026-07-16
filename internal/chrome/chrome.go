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
	ZellijSession  string
	SocketPath     string
	RoleCommand    []string
	TabNetworkArgs []string
	NoWatch        bool
	Now            func() time.Time
}

func BuildPlan(req PlanRequest) (transport.ExecutionPlanPayload, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: cwd is required", ErrInvalidPlanRequest)
	}
	if err := validateTabNetworkArgs(req.TabNetworkArgs); err != nil {
		return transport.ExecutionPlanPayload{}, err
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
	role := "tab-network"
	idPrefix := "chrome-tab-network"
	command := append(append([]string{}, roleCommand...), "tab-network")
	if req.NoWatch {
		command = append(command, req.TabNetworkArgs...)
	} else {
		zellijSession := strings.TrimSpace(req.ZellijSession)
		if zellijSession == "" {
			return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: zellij session is required", ErrInvalidPlanRequest)
		}
		if req.SocketPath != "" {
			command = append(command, "--socket", req.SocketPath)
		}
		command = append(command,
			"--session", session,
			"--role-bin", roleCommand[0],
			"--spawn-on-new-tab",
			"--zellij-session", zellijSession,
		)
		command = append(command, req.TabNetworkArgs...)
	}

	return transport.ExecutionPlanPayload{
		Session:       session,
		ZellijSession: strings.TrimSpace(req.ZellijSession),
		Layout:        "single-tab",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name: "chrome",
				Panes: []transport.ExecutionPlanPane{
					{
						ID:      paneID(idPrefix, now()),
						Role:    role,
						CWD:     cwd,
						Command: command,
					},
				},
			},
		},
	}, nil
}

func validateTabNetworkArgs(args []string) error {
	for _, arg := range args {
		if arg == "--zellij-session" || strings.HasPrefix(arg, "--zellij-session=") ||
			arg == "-zellij-session" || strings.HasPrefix(arg, "-zellij-session=") {
			return fmt.Errorf("%w: tab-network args may not set --zellij-session", ErrInvalidPlanRequest)
		}
	}
	return nil
}

func RequestID(session string) string {
	return "req_" + session
}

func paneID(prefix string, t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("%s-%s-%09d", prefix, t.Format("20060102-150405"), t.Nanosecond())
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
