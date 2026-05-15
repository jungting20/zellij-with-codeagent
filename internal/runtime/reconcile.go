package runtime

import (
	"context"
	"fmt"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func (s *Service) Reconcile(ctx context.Context, _ ReconcileRequest) (ReconcileResponse, error) {
	livePanes, err := s.backend.ListPanes(ctx)
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
	if record.ZellijPaneID == "" || isTerminalStatus(record.Status) {
		if s.subs != nil {
			s.subs.StopPane(record.ID)
		}
		return record, nil
	}

	live, ok := liveByZellijID[record.ZellijPaneID]
	if !ok {
		updated, err := s.registry.UpdatePaneStatus(record.ID, registry.PaneStatusLost, "zellij pane missing during reconcile")
		if err == nil && s.subs != nil {
			s.subs.StopPane(record.ID)
		}
		return updated, err
	}

	if live.Exited {
		updated, err := s.registry.UpdatePaneStatus(record.ID, registry.PaneStatusExited, "zellij pane exited during reconcile")
		if err == nil && s.subs != nil {
			s.subs.StopPane(record.ID)
		}
		return updated, err
	}

	if record.Status == registry.PaneStatusRunning {
		return record, nil
	}
	return s.registry.UpdatePaneStatus(record.ID, registry.PaneStatusRunning, "zellij pane live during reconcile")
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
