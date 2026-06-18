package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"zellij-with-codeagent/internal/registry"
)

var (
	ErrInvalidExecutionPlan = errors.New("runtime: invalid execution plan")
)

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

type executionPlanPaneResult struct {
	index int
	pane  Pane
	err   error
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

		firstSpec := tabSpec.Panes[0]
		response, err := s.CreatePane(ctx, CreatePaneRequest{
			ID:      firstSpec.ID,
			TaskID:  taskID,
			AgentID: firstSpec.AgentID,
			Role:    firstSpec.Role,
			Name:    string(firstSpec.ID),
			NewTab:  true,
			TabName: tabName,
			CWD:     firstSpec.CWD,
			Command: executionPlanCommand(firstSpec),
		})
		if err != nil {
			_ = s.rollbackExecutionPlan(ctx, createdAll)
			return ApplyExecutionPlanResponse{}, err
		}
		tabID = response.Pane.ZellijTabID
		createdTabPanes = append(createdTabPanes, response.Pane)
		createdAll = append(createdAll, response.Pane)

		if len(tabSpec.Panes) > 1 {
			if tabID == nil {
				_ = s.rollbackExecutionPlan(ctx, createdAll)
				return ApplyExecutionPlanResponse{}, fmt.Errorf("%w: first pane missing zellij tab id in tab %q", ErrInvalidExecutionPlan, tabName)
			}
			remaining, err := s.createRemainingExecutionPlanTabPanes(ctx, taskID, tabName, *tabID, tabSpec.Panes[1:])
			if err != nil {
				_ = s.rollbackExecutionPlan(ctx, append(createdAll, remaining...))
				return ApplyExecutionPlanResponse{}, err
			}
			createdTabPanes = append(createdTabPanes, remaining...)
			createdAll = append(createdAll, remaining...)
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

func (s *Service) createRemainingExecutionPlanTabPanes(ctx context.Context, taskID TaskID, tabName string, tabID ZellijTabID, specs []ExecutionPlanPaneSpec) ([]Pane, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan executionPlanPaneResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		i, spec := i, spec
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := s.CreatePane(ctx, CreatePaneRequest{
				ID:          spec.ID,
				TaskID:      taskID,
				AgentID:     spec.AgentID,
				Role:        spec.Role,
				Name:        string(spec.ID),
				TabName:     tabName,
				ZellijTabID: &tabID,
				CWD:         spec.CWD,
				Command:     executionPlanCommand(spec),
			})
			if err != nil {
				cancel()
				results <- executionPlanPaneResult{index: i, err: err}
				return
			}
			results <- executionPlanPaneResult{index: i, pane: response.Pane}
		}()
	}

	wg.Wait()
	close(results)

	panes := make([]Pane, len(specs))
	created := make([]Pane, 0, len(specs))
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		panes[result.index] = result.pane
		created = append(created, result.pane)
	}
	if firstErr != nil {
		return created, firstErr
	}
	return panes, nil
}

func validateExecutionPlan(req ApplyExecutionPlanRequest) error {
	if req.Session == "" {
		return fmt.Errorf("%w: session is required", ErrInvalidExecutionPlan)
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
