package ticketworker

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"zellij-with-codeagent/internal/transport"
)

type StartPlanRequest struct {
	Root          string
	ZellijSession string
	SocketPath    string
	Executable    []string
}

const ticketWorkerLayout = `layout {
    pane name="ticket-manager"

    swap_tiled_layout name="ticket-worker" {
        tab min_panes=2 split_direction="horizontal" {
            pane size="50%"
            pane size="50%" split_direction="vertical" {
                children
            }
        }
    }
}`

func BuildStartPlan(req StartPlanRequest) (transport.ExecutionPlanPayload, error) {
	root := filepath.Clean(strings.TrimSpace(req.Root))
	if root == "." || !filepath.IsAbs(root) {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("ticket-worker start plan: absolute root is required")
	}
	zellijSession := strings.TrimSpace(req.ZellijSession)
	if zellijSession == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("ticket-worker start plan: Zellij session is required")
	}
	socketPath := strings.TrimSpace(req.SocketPath)
	if socketPath == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("ticket-worker start plan: socket path is required")
	}
	executable, err := normalizeStartExecutable(req.Executable)
	if err != nil {
		return transport.ExecutionPlanPayload{}, err
	}

	suffix := startIdentity(root)
	session := "ticket-worker-" + suffix
	anchor := "ticket-manager-" + suffix
	command := append(executable,
		"role", "ticket-manager",
		"--socket", socketPath,
		"--task", session,
		"--anchor-pane", anchor,
		"--zellij-session", zellijSession,
		root,
	)

	return transport.ExecutionPlanPayload{
		Session:       session,
		ZellijSession: zellijSession,
		Layout:        "single-tab",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name:         "ticket-worker",
				LayoutString: ticketWorkerLayout,
				Panes: []transport.ExecutionPlanPane{
					{
						ID:      anchor,
						Role:    "ticket-manager",
						Command: command,
						CWD:     root,
					},
				},
			},
		},
	}, nil
}

func StartRequestID(session string) string {
	return "req_" + session
}

func startIdentity(root string) string {
	digest := sha256.Sum256([]byte(root))
	return fmt.Sprintf("%x", digest[:4])
}

func normalizeStartExecutable(value []string) ([]string, error) {
	if len(value) == 0 {
		return nil, fmt.Errorf("ticket-worker start plan: executable is required")
	}
	normalized := make([]string, len(value), len(value)+11)
	for i, part := range value {
		normalized[i] = strings.TrimSpace(part)
		if normalized[i] == "" {
			return nil, fmt.Errorf("ticket-worker start plan: executable[%d] must not be empty", i)
		}
	}
	return normalized, nil
}
