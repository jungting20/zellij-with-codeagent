package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"zellij-with-codeagent/internal/registry"
)

var (
	ErrInvalidExecutionPlan = errors.New("runtime: invalid execution plan")
)

type ExecutionPlanPaneSpec struct {
	ID                    PaneID
	Role                  string
	AgentID               AgentID
	Command               []string
	CWD                   string
	InitialInput          string
	InitialInputReadyText string
}

type ExecutionPlanTabSpec struct {
	Name         string
	LayoutString string
	Panes        []ExecutionPlanPaneSpec
}

type ApplyExecutionPlanRequest struct {
	RequestID     string
	Session       string
	ZellijSession string
	Layout        string
	Tabs          []ExecutionPlanTabSpec
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
	index   int
	created createdExecutionPlanPane
	err     error
}

type createdExecutionPlanPane struct {
	pane   Pane
	record registry.PaneRecord
}

func (s *Service) ApplyExecutionPlan(ctx context.Context, req ApplyExecutionPlanRequest) (ApplyExecutionPlanResponse, error) {
	if err := validateExecutionPlan(req); err != nil {
		return ApplyExecutionPlanResponse{}, err
	}

	taskID := TaskID(req.Session)
	createdAll := make([]createdExecutionPlanPane, 0)
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
			ID:                    firstSpec.ID,
			TaskID:                taskID,
			AgentID:               firstSpec.AgentID,
			Role:                  firstSpec.Role,
			Name:                  string(firstSpec.ID),
			ZellijSession:         req.ZellijSession,
			NewTab:                true,
			TabName:               tabName,
			LayoutString:          tabSpec.LayoutString,
			CWD:                   firstSpec.CWD,
			Command:               executionPlanCommand(firstSpec),
			InitialInput:          firstSpec.InitialInput,
			InitialInputReadyText: firstSpec.InitialInputReadyText,
		})
		if err != nil {
			cause := fmt.Errorf("initialize pane %q: %w", firstSpec.ID, err)
			return ApplyExecutionPlanResponse{}, errors.Join(cause, s.rollbackExecutionPlan(ctx, createdAll))
		}
		tabID = response.Pane.ZellijTabID
		createdFirst := createdExecutionPlanPane{pane: response.Pane, record: response.record}
		createdTabPanes = append(createdTabPanes, response.Pane)
		createdAll = append(createdAll, createdFirst)

		if len(tabSpec.Panes) > 1 {
			if tabID == nil {
				cause := fmt.Errorf("%w: first pane missing zellij tab id in tab %q", ErrInvalidExecutionPlan, tabName)
				return ApplyExecutionPlanResponse{}, errors.Join(cause, s.rollbackExecutionPlan(ctx, createdAll))
			}
			remaining, err := s.createRemainingExecutionPlanTabPanes(ctx, req.ZellijSession, taskID, tabName, *tabID, tabSpec.Panes[1:])
			if err != nil {
				return ApplyExecutionPlanResponse{}, errors.Join(err, s.rollbackExecutionPlan(ctx, append(createdAll, remaining...)))
			}
			for _, created := range remaining {
				createdTabPanes = append(createdTabPanes, created.pane)
			}
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

func (s *Service) createRemainingExecutionPlanTabPanes(ctx context.Context, zellijSession string, taskID TaskID, tabName string, tabID ZellijTabID, specs []ExecutionPlanPaneSpec) ([]createdExecutionPlanPane, error) {
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
				ID:                    spec.ID,
				TaskID:                taskID,
				AgentID:               spec.AgentID,
				Role:                  spec.Role,
				Name:                  string(spec.ID),
				ZellijSession:         zellijSession,
				TabName:               tabName,
				ZellijTabID:           &tabID,
				CWD:                   spec.CWD,
				Command:               executionPlanCommand(spec),
				InitialInput:          spec.InitialInput,
				InitialInputReadyText: spec.InitialInputReadyText,
			})
			if err != nil {
				cancel()
				results <- executionPlanPaneResult{index: i, err: fmt.Errorf("initialize pane %q: %w", spec.ID, err)}
				return
			}
			created := createdExecutionPlanPane{pane: response.Pane, record: response.record}
			results <- executionPlanPaneResult{index: i, created: created}
		}()
	}

	wg.Wait()
	close(results)

	return collectExecutionPlanPaneResults(len(specs), results)
}

func collectExecutionPlanPaneResults(count int, results <-chan executionPlanPaneResult) ([]createdExecutionPlanPane, error) {
	panes := make([]createdExecutionPlanPane, count)
	created := make([]createdExecutionPlanPane, 0, count)
	var resultErr error
	for result := range results {
		if result.created.pane.ID != "" {
			panes[result.index] = result.created
			created = append(created, result.created)
		}
		resultErr = errors.Join(resultErr, result.err)
	}
	if resultErr != nil {
		return created, resultErr
	}
	return panes, nil
}

func validateExecutionPlan(req ApplyExecutionPlanRequest) error {
	if strings.TrimSpace(req.ZellijSession) == "" {
		return ErrZellijSessionRequired
	}
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

func (s *Service) rollbackExecutionPlan(ctx context.Context, created []createdExecutionPlanPane) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	var rollbackErr error
	for _, createdPane := range created {
		rollbackErr = errors.Join(rollbackErr, s.cleanupCreatedPane(rollbackCtx, createdPane.record))
	}
	if rollbackErr == nil {
		return nil
	}
	return errors.Join(ErrCleanupPartial, rollbackErr)
}
