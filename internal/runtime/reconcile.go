package runtime

import (
	"context"
	"errors"
	"fmt"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func (s *Service) Reconcile(ctx context.Context, _ ReconcileRequest) (ReconcileResponse, error) {
	livePanes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{})
	if err != nil {
		s.publishRuntimeHealth(fmt.Sprintf("reconcile failed: %v", err))
		return ReconcileResponse{}, err
	}

	liveByZellijID := make(map[registry.ZellijPaneID]zellij.Pane, len(livePanes))
	for _, pane := range livePanes {
		if pane.IsPlugin || pane.ID == "" {
			continue
		}
		liveByZellijID[registry.ZellijPaneID(pane.ID)] = pane
	}

	records := s.registry.ListPanes()
	managedByZellijID := make(map[registry.ZellijPaneID]bool, len(records))
	response := ReconcileResponse{
		Panes: make([]Pane, 0, len(records)),
	}

	for _, record := range records {
		if record.ZellijPaneID != "" {
			managedByZellijID[record.ZellijPaneID] = true
		}

		reconciled, err := s.reconcileRecord(record, liveByZellijID)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrStaleRecord) {
				continue
			}
			s.publishRuntimeHealth(fmt.Sprintf("reconcile pane %s failed: %v", record.ID, err))
			return response, err
		}

		pane := paneFromRecord(reconciled)
		response.Panes = append(response.Panes, pane)
		switch pane.Status {
		case PaneStatusRunning, PaneStatusStarting:
			response.Active = append(response.Active, pane)
		case PaneStatusExited:
			response.Exited = append(response.Exited, pane)
		case PaneStatusLost:
			response.Lost = append(response.Lost, pane)
		}
	}

	for id := range liveByZellijID {
		if !managedByZellijID[id] {
			response.Unmanaged = append(response.Unmanaged, ZellijPaneID(id))
		}
	}

	return response, nil
}

func (s *Service) reconcileRecord(record registry.PaneRecord, liveByZellijID map[registry.ZellijPaneID]zellij.Pane) (registry.PaneRecord, error) {
	if record.Status == registry.PaneStatusExited {
		removed, err := s.registry.RemovePaneGeneration(record.ID, record.Generation)
		if err == nil && s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		return removed, err
	}

	if record.ZellijPaneID == "" || isTerminalStatus(record.Status) {
		current, err := s.currentPaneGeneration(record)
		if err != nil {
			return registry.PaneRecord{}, err
		}
		if s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		return current, nil
	}

	live, ok := liveByZellijID[record.ZellijPaneID]
	if !ok {
		updated, err := s.registry.UpdatePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusLost, "zellij pane missing during reconcile")
		if err == nil && s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		return updated, err
	}

	if live.Exited {
		removed, err := s.registry.RemovePaneGeneration(record.ID, record.Generation)
		if err == nil && s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		removed.Status = registry.PaneStatusExited
		removed.StatusMessage = "zellij pane exited during reconcile"
		return removed, err
	}

	if record.Status == registry.PaneStatusRunning {
		return s.currentPaneGeneration(record)
	}
	return s.registry.UpdatePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusRunning, "zellij pane live during reconcile")
}

func (s *Service) currentPaneGeneration(record registry.PaneRecord) (registry.PaneRecord, error) {
	current, err := s.registry.GetPane(record.ID)
	if err != nil {
		return registry.PaneRecord{}, err
	}
	if current.Generation != record.Generation {
		return registry.PaneRecord{}, registry.ErrStaleRecord
	}
	return current, nil
}

func (s *Service) publishRuntimeHealth(message string) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(eventbus.Event{
		Type:    eventbus.TypeHealthChanged,
		Message: message,
	})
}
