package runtime

import (
	"context"
	"errors"
	"fmt"

	"zellij-with-codeagent/internal/registry"
)

var (
	ErrInvalidExecutionPlan = errors.New("runtime: invalid execution plan")
)

// SupportedExecutionPlanLayouts lists layout values accepted in v1.
// Physical Zellij layout application is deferred; layout is validated metadata.
var SupportedExecutionPlanLayouts = map[string]struct{}{
	"triple-horizontal": {},
}

type ExecutionPlanPaneSpec struct {
	ID      PaneID
	Role    PaneRole
	AgentID AgentID
	Command []string
	CWD     string
}

type ApplyExecutionPlanRequest struct {
	RequestID string
	Session   string
	Layout    string
	Panes     []ExecutionPlanPaneSpec
}

type ApplyExecutionPlanResponse struct {
	RequestID string
	Session   string
	Layout    string
	Panes     []Pane
}

func (s *Service) ApplyExecutionPlan(ctx context.Context, req ApplyExecutionPlanRequest) (ApplyExecutionPlanResponse, error) {
	if err := validateExecutionPlan(req); err != nil {
		return ApplyExecutionPlanResponse{}, err
	}

	taskID := TaskID(req.Session)
	tabName := req.Session
	created := make([]Pane, 0, len(req.Panes))

	for i, spec := range req.Panes {
		createReq := CreatePaneRequest{
			ID:      spec.ID,
			TaskID:  taskID,
			AgentID: spec.AgentID,
			Role:    spec.Role,
			Name:    string(spec.ID),
			TabName: tabName,
			CWD:     spec.CWD,
			Command: executionPlanCommand(spec),
		}
		if i == 0 {
			createReq.NewTab = true
		} else {
			tabID := created[0].ZellijTabID
			if tabID == nil {
				_ = s.rollbackExecutionPlan(ctx, created)
				return ApplyExecutionPlanResponse{}, fmt.Errorf("%w: first pane missing zellij tab id", ErrInvalidExecutionPlan)
			}
			createReq.ZellijTabID = tabID
		}

		response, err := s.CreatePane(ctx, createReq)
		if err != nil {
			_ = s.rollbackExecutionPlan(ctx, created)
			return ApplyExecutionPlanResponse{}, err
		}
		created = append(created, response.Pane)
	}

	return ApplyExecutionPlanResponse{
		RequestID: req.RequestID,
		Session:   req.Session,
		Layout:    req.Layout,
		Panes:     created,
	}, nil
}

func validateExecutionPlan(req ApplyExecutionPlanRequest) error {
	if req.Session == "" {
		return fmt.Errorf("%w: session is required", ErrInvalidExecutionPlan)
	}
	if req.Layout == "" {
		return fmt.Errorf("%w: layout is required", ErrInvalidExecutionPlan)
	}
	if _, ok := SupportedExecutionPlanLayouts[req.Layout]; !ok {
		return fmt.Errorf("%w: unsupported layout %q", ErrInvalidExecutionPlan, req.Layout)
	}
	if len(req.Panes) == 0 {
		return fmt.Errorf("%w: at least one pane is required", ErrInvalidExecutionPlan)
	}

	seen := make(map[PaneID]struct{}, len(req.Panes))
	for _, spec := range req.Panes {
		if spec.ID == "" {
			return fmt.Errorf("%w: pane id is required", ErrInvalidExecutionPlan)
		}
		if _, dup := seen[spec.ID]; dup {
			return fmt.Errorf("%w: duplicate pane id %q", ErrInvalidExecutionPlan, spec.ID)
		}
		seen[spec.ID] = struct{}{}
	}
	return nil
}

func executionPlanCommand(spec ExecutionPlanPaneSpec) []string {
	if len(spec.Command) > 0 {
		return cloneStrings(spec.Command)
	}
	return DefaultExecutionPlanPaneCommand(string(spec.ID))
}

// DefaultExecutionPlanPaneCommand returns a shell that prints a readiness marker.
func DefaultExecutionPlanPaneCommand(paneID string) []string {
	script := fmt.Sprintf(`pane=%q
printf 'agentd_execution_plan_ready:%%s\n' "$pane"
exec sh`, paneID)
	return []string{"sh", "-lc", script}
}

func (s *Service) rollbackExecutionPlan(ctx context.Context, created []Pane) error {
	var rollbackErr error
	for _, pane := range created {
		if _, err := s.ClosePane(ctx, ClosePaneRequest{PaneID: pane.ID}); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
		if _, err := s.registry.RemovePane(registry.PaneID(pane.ID)); err != nil && !errors.Is(err, registry.ErrNotFound) {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}
