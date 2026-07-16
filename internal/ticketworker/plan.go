package ticketworker

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zellij-with-codeagent/internal/transport"
)

var ErrInvalidPlanRequest = errors.New("ticket-worker: invalid plan request")

type PlanRequest struct {
	CWD           string
	ConfigPath    string
	Session       string
	ZellijSession string
	Executable    []string
	SocketPath    string
	Timeout       time.Duration
	Config        Config
}

func BuildPlan(req PlanRequest) (transport.ExecutionPlanPayload, error) {
	cwd, err := absoluteRequiredPath("cwd", req.CWD)
	if err != nil {
		return transport.ExecutionPlanPayload{}, err
	}
	configPath, err := absoluteRequiredPath("config path", req.ConfigPath)
	if err != nil {
		return transport.ExecutionPlanPayload{}, err
	}
	executable, err := normalizeExecutable(req.Executable)
	if err != nil {
		return transport.ExecutionPlanPayload{}, err
	}
	if req.Config.MaxWorkers <= 0 {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: max_workers must be positive", ErrInvalidPlanRequest)
	}
	zellijSession := strings.TrimSpace(req.ZellijSession)
	if zellijSession == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: zellij session is required", ErrInvalidPlanRequest)
	}

	session := strings.TrimSpace(req.Session)
	if session == "" {
		session = SessionID(cwd, time.Now())
	}
	managerCommand := append(append([]string{}, executable...),
		"ticket-worker", "manager",
		"--cwd", cwd,
		"--config", configPath,
		"--task", session,
		"--anchor", "ticket-worker-manager",
		"--zellij-session", zellijSession,
	)
	if req.Timeout > 0 {
		managerCommand = append(managerCommand, "--timeout", req.Timeout.String())
	}
	monitorCommand := append(append([]string{}, executable...),
		"dashboard",
		"--task", session,
		"--read-only",
		"--capacity", strconv.Itoa(req.Config.MaxWorkers),
	)
	if socketPath := strings.TrimSpace(req.SocketPath); socketPath != "" {
		managerCommand = append(managerCommand, "--socket", socketPath)
		monitorCommand = append(monitorCommand, "--socket", socketPath)
	}

	return transport.ExecutionPlanPayload{
		Session:       session,
		ZellijSession: zellijSession,
		Layout:        "triple-horizontal",
		Tabs: []transport.ExecutionPlanTab{{
			Name: "ticket-worker",
			Panes: []transport.ExecutionPlanPane{
				{
					ID:      "ticket-worker-manager",
					Role:    "ticket-worker-manager",
					Command: managerCommand,
					CWD:     cwd,
				},
				{
					ID:      "ticket-worker-monitor",
					Role:    "ticket-worker-monitor",
					Command: monitorCommand,
					CWD:     cwd,
				},
			},
		}},
	}, nil
}

func RequestID(session string) string {
	return "req_" + session
}

func SessionID(cwd string, now time.Time) string {
	hash := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("ticket-worker-%s-%x", now.UTC().Format("20060102-150405"), hash[:4])
}

func absoluteRequiredPath(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidPlanRequest, name)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s: %v", ErrInvalidPlanRequest, name, err)
	}
	return abs, nil
}

func normalizeExecutable(command []string) ([]string, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("%w: executable is required", ErrInvalidPlanRequest)
	}
	normalized := make([]string, len(command))
	for i, arg := range command {
		normalized[i] = strings.TrimSpace(arg)
		if normalized[i] == "" {
			return nil, fmt.Errorf("%w: executable[%d] must not be empty", ErrInvalidPlanRequest, i)
		}
	}
	return normalized, nil
}
