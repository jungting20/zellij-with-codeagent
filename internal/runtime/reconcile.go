package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

type livePaneKey struct {
	session registry.SessionID
	paneID  registry.ZellijPaneID
}

func (s *Service) Reconcile(ctx context.Context, _ ReconcileRequest) (ReconcileResponse, error) {
	records := s.registry.ListPanes()
	sessionSet := make(map[registry.SessionID]bool)
	for _, record := range records {
		if !isTerminalStatus(record.Status) {
			sessionSet[record.SessionID] = true
		}
	}

	sessions := make([]registry.SessionID, 0, len(sessionSet))
	for sessionID := range sessionSet {
		sessions = append(sessions, sessionID)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })

	liveByKey := make(map[livePaneKey]zellij.Pane)
	liveKeys := make([]livePaneKey, 0)
	for _, sessionID := range sessions {
		livePanes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: string(sessionID)})
		if err != nil {
			s.publishRuntimeHealth(fmt.Sprintf("reconcile failed for session %q: %v", sessionID, err))
			return ReconcileResponse{}, err
		}
		for _, pane := range livePanes {
			if pane.IsPlugin || pane.ID == "" {
				continue
			}
			key := livePaneKey{session: sessionID, paneID: registry.ZellijPaneID(pane.ID)}
			if _, exists := liveByKey[key]; !exists {
				liveKeys = append(liveKeys, key)
			}
			liveByKey[key] = pane
		}
	}

	managedByKey := make(map[livePaneKey]bool, len(records))
	response := ReconcileResponse{
		Panes: make([]Pane, 0, len(records)),
	}

	for _, record := range records {
		if record.ZellijPaneID != "" {
			managedByKey[livePaneKey{session: record.SessionID, paneID: record.ZellijPaneID}] = true
		}

		reconciled, err := s.reconcileRecord(record, liveByKey)
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

	for _, key := range liveKeys {
		if !managedByKey[key] {
			response.Unmanaged = append(response.Unmanaged, ZellijPaneID(key.paneID))
		}
	}

	return response, nil
}

func (s *Service) reconcileRecord(record registry.PaneRecord, liveByKey map[livePaneKey]zellij.Pane) (registry.PaneRecord, error) {
	if record.Status == registry.PaneStatusExited {
		removed, claimed, err := s.registry.RemovePaneGenerationClaimingClosure(record.ID, record.Generation)
		if err == nil && claimed {
			s.notifyReconciledPaneClosed(removed)
		}
		return removed, err
	}

	if isTerminalStatus(record.Status) {
		current, claimed, err := s.registry.ClaimPaneClosureGeneration(record.ID, record.Generation, record.Status, record.StatusMessage)
		if err == nil && claimed {
			s.notifyReconciledPaneClosed(current)
		} else if s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		return current, err
	}

	if record.ZellijPaneID == "" {
		current, err := s.currentPaneGeneration(record)
		if err != nil {
			return registry.PaneRecord{}, err
		}
		if s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		return current, nil
	}

	live, ok := liveByKey[livePaneKey{session: record.SessionID, paneID: record.ZellijPaneID}]
	if !ok {
		updated, claimed, err := s.registry.ClaimPaneClosureGeneration(record.ID, record.Generation, registry.PaneStatusLost, "zellij pane missing during reconcile")
		if err == nil && claimed {
			s.notifyReconciledPaneClosed(updated)
		}
		return updated, err
	}

	if live.Exited {
		removed, claimed, err := s.registry.RemovePaneGenerationClaimingClosure(record.ID, record.Generation)
		if err == nil && !isTerminalStatus(removed.Status) {
			removed.Status = registry.PaneStatusExited
			removed.StatusMessage = "zellij pane exited during reconcile"
		}
		if err == nil && claimed {
			s.notifyReconciledPaneClosed(removed)
		}
		return removed, err
	}

	if record.Status == registry.PaneStatusRunning {
		return s.currentPaneGeneration(record)
	}
	current, _, err := s.registry.UpdateActivePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusRunning, "zellij pane live during reconcile")
	return current, err
}

func (s *Service) notifyReconciledPaneClosed(record registry.PaneRecord) {
	if s.subs != nil {
		s.subs.StopPaneGeneration(record.ID, record.Generation)
	}
	if s.observer != nil {
		s.observer.PaneClosed(record)
	}
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
