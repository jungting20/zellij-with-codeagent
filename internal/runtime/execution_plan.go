package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

var (
	ErrInvalidExecutionPlan = errors.New("runtime: invalid execution plan")
)

const executionPlanInitialInputPollInterval = 50 * time.Millisecond

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
	Name  string
	Panes []ExecutionPlanPaneSpec
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
			ID:            firstSpec.ID,
			TaskID:        taskID,
			AgentID:       firstSpec.AgentID,
			Role:          firstSpec.Role,
			Name:          string(firstSpec.ID),
			ZellijSession: req.ZellijSession,
			NewTab:        true,
			TabName:       tabName,
			CWD:           firstSpec.CWD,
			Command:       executionPlanCommand(firstSpec),
		})
		if err != nil {
			_ = s.rollbackExecutionPlan(ctx, createdAll)
			return ApplyExecutionPlanResponse{}, err
		}
		tabID = response.Pane.ZellijTabID
		createdFirst := createdExecutionPlanPane{pane: response.Pane, record: response.record}
		createdTabPanes = append(createdTabPanes, response.Pane)
		createdAll = append(createdAll, createdFirst)
		if err := s.sendExecutionPlanInitialInput(ctx, createdFirst, firstSpec.InitialInput, firstSpec.InitialInputReadyText); err != nil {
			_ = s.rollbackExecutionPlan(ctx, createdAll)
			return ApplyExecutionPlanResponse{}, err
		}

		if len(tabSpec.Panes) > 1 {
			if tabID == nil {
				_ = s.rollbackExecutionPlan(ctx, createdAll)
				return ApplyExecutionPlanResponse{}, fmt.Errorf("%w: first pane missing zellij tab id in tab %q", ErrInvalidExecutionPlan, tabName)
			}
			remaining, err := s.createRemainingExecutionPlanTabPanes(ctx, req.ZellijSession, taskID, tabName, *tabID, tabSpec.Panes[1:])
			if err != nil {
				_ = s.rollbackExecutionPlan(ctx, append(createdAll, remaining...))
				return ApplyExecutionPlanResponse{}, err
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
				ID:            spec.ID,
				TaskID:        taskID,
				AgentID:       spec.AgentID,
				Role:          spec.Role,
				Name:          string(spec.ID),
				ZellijSession: zellijSession,
				TabName:       tabName,
				ZellijTabID:   &tabID,
				CWD:           spec.CWD,
				Command:       executionPlanCommand(spec),
			})
			if err != nil {
				cancel()
				results <- executionPlanPaneResult{index: i, err: err}
				return
			}
			created := createdExecutionPlanPane{pane: response.Pane, record: response.record}
			if err := s.sendExecutionPlanInitialInput(ctx, created, spec.InitialInput, spec.InitialInputReadyText); err != nil {
				cancel()
				results <- executionPlanPaneResult{index: i, created: created, err: err}
				return
			}
			results <- executionPlanPaneResult{index: i, created: created}
		}()
	}

	wg.Wait()
	close(results)

	panes := make([]createdExecutionPlanPane, len(specs))
	created := make([]createdExecutionPlanPane, 0, len(specs))
	var firstErr error
	for result := range results {
		if result.created.pane.ID != "" {
			panes[result.index] = result.created
			created = append(created, result.created)
		}
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
	}
	if firstErr != nil {
		return created, firstErr
	}
	return panes, nil
}

func (s *Service) sendExecutionPlanInitialInput(ctx context.Context, created createdExecutionPlanPane, initialInput, readyText string) error {
	if initialInput == "" {
		return nil
	}
	if err := s.waitForExecutionPlanInitialInputReady(ctx, created, readyText); err != nil {
		return err
	}
	current, err := s.registry.GetPane(created.record.ID)
	if err != nil {
		return fmt.Errorf("send initial input to pane %q: %w", created.pane.ID, err)
	}
	if current.Generation != created.record.Generation {
		return fmt.Errorf("send initial input to pane %q: %w", created.pane.ID, registry.ErrStaleRecord)
	}
	if err := s.backend.SendInput(ctx, zellij.SendInputRequest{
		Session: string(created.record.SessionID),
		PaneID:  zellij.PaneID(created.record.ZellijPaneID),
		Text:    initialInput,
	}); err != nil {
		_, _ = s.registry.UpdatePaneStatusGeneration(created.record.ID, created.record.Generation, registry.PaneStatusError, err.Error())
		return fmt.Errorf("send initial input to pane %q: %w", created.pane.ID, err)
	}
	return nil
}

func (s *Service) waitForExecutionPlanInitialInputReady(ctx context.Context, created createdExecutionPlanPane, readyText string) error {
	if readyText == "" {
		return nil
	}
	ticker := time.NewTicker(executionPlanInitialInputPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		output, err := s.backend.DumpScreen(ctx, zellij.DumpScreenRequest{
			Session: string(created.record.SessionID),
			PaneID:  zellij.PaneID(created.record.ZellijPaneID),
		})
		if err == nil && strings.Contains(output, readyText) {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			cause := ctx.Err()
			if lastErr != nil {
				cause = errors.Join(cause, lastErr)
			}
			return fmt.Errorf("wait for initial input readiness in pane %q: %w", created.pane.ID, cause)
		case <-ticker.C:
		}
	}
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
		if err := s.backend.ClosePane(rollbackCtx, zellij.ClosePaneRequest{Session: string(createdPane.record.SessionID), PaneID: zellij.PaneID(createdPane.record.ZellijPaneID)}); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
		if s.subs != nil {
			s.subs.StopPaneGeneration(createdPane.record.ID, createdPane.record.Generation)
		}
		if _, err := s.registry.RemovePaneGeneration(createdPane.record.ID, createdPane.record.Generation); err != nil && !errors.Is(err, registry.ErrNotFound) && !errors.Is(err, registry.ErrStaleRecord) {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}
