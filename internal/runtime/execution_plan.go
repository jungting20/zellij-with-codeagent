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
	Role    string
	AgentID AgentID
	Command []string
	CWD     string
}

type ExecutionPlanTabSpec struct {
	Name  string
	Panes []ExecutionPlanPaneSpec
}

type ApplyExecutionPlanRequest struct {
	RequestID string
	Session   string
	Layout    string
	Tabs      []ExecutionPlanTabSpec
}

type ExecutionPlanTabResult struct {
	Name  string
	Panes []Pane
}

type ApplyExecutionPlanResponse struct {
	RequestID string
	Session   string
	Layout    string
	Tabs      []ExecutionPlanTabResult
}

func (s *Service) ApplyExecutionPlan(ctx context.Context, req ApplyExecutionPlanRequest) (ApplyExecutionPlanResponse, error) {
	if err := validateExecutionPlan(req); err != nil {
		return ApplyExecutionPlanResponse{}, err
	}

	taskID := TaskID(req.Session)
	createdAll := make([]Pane, 0)
	tabResults := make([]ExecutionPlanTabResult, 0, len(req.Tabs))

	for _, tabSpec := range req.Tabs {
		tabName := tabSpec.Name
		if tabName == "" {
			tabName = req.Session
		}

		var tabID *ZellijTabID
		createdTabPanes := make([]Pane, 0, len(tabSpec.Panes))

		for j, spec := range tabSpec.Panes {
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
			if j == 0 {
				createReq.NewTab = true
			} else {
				if tabID == nil {
					_ = s.rollbackExecutionPlan(ctx, createdAll)
					return ApplyExecutionPlanResponse{}, fmt.Errorf("%w: first pane missing zellij tab id in tab %q", ErrInvalidExecutionPlan, tabName)
				}
				createReq.ZellijTabID = tabID
			}

			response, err := s.CreatePane(ctx, createReq)
			if err != nil {
				_ = s.rollbackExecutionPlan(ctx, createdAll)
				return ApplyExecutionPlanResponse{}, err
			}
			if j == 0 {
				tabID = response.Pane.ZellijTabID
			}
			createdTabPanes = append(createdTabPanes, response.Pane)
			createdAll = append(createdAll, response.Pane)
		}

		tabResults = append(tabResults, ExecutionPlanTabResult{
			Name:  tabName,
			Panes: createdTabPanes,
		})
	}

	return ApplyExecutionPlanResponse{
		RequestID: req.RequestID,
		Session:   req.Session,
		Layout:    req.Layout,
		Tabs:      tabResults,
	}, nil
}

func validateExecutionPlan(req ApplyExecutionPlanRequest) error {
	if req.Session == "" {
		return fmt.Errorf("%w: session is required", ErrInvalidExecutionPlan)
	}
	if req.Layout != "" {
		if _, ok := SupportedExecutionPlanLayouts[req.Layout]; !ok {
			return fmt.Errorf("%w: unsupported layout %q", ErrInvalidExecutionPlan, req.Layout)
		}
	}
	if len(req.Tabs) == 0 {
		return fmt.Errorf("%w: at least one tab is required", ErrInvalidExecutionPlan)
	}

	seen := make(map[PaneID]struct{})
	for _, tab := range req.Tabs {
		if len(tab.Panes) == 0 {
			return fmt.Errorf("%w: tab %q must contain at least one pane", ErrInvalidExecutionPlan, tab.Name)
		}
		for _, spec := range tab.Panes {
			if spec.ID == "" {
				return fmt.Errorf("%w: pane id is required", ErrInvalidExecutionPlan)
			}
			if _, dup := seen[spec.ID]; dup {
				return fmt.Errorf("%w: duplicate pane id %q", ErrInvalidExecutionPlan, spec.ID)
			}
			seen[spec.ID] = struct{}{}
		}
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
