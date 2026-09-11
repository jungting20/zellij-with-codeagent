package runtime

import (
	"context"
	"fmt"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

// Recover is called once before serving requests. No pane is created, closed,
// or sent input during recovery. Failed inspection leaves stored records intact.
func (s *Service) Recover(ctx context.Context) error {
	records := s.registry.ListPanes()
	if len(records) == 0 {
		return nil
	}
	var active map[string]bool
	if lister, ok := s.backend.(interface {
		ActiveSessions(context.Context) ([]string, error)
	}); ok {
		sessions, err := lister.ActiveSessions(ctx)
		if err != nil {
			return err
		}
		active = make(map[string]bool)
		for _, session := range sessions {
			active[session] = true
		}
	}
	live := make(map[livePaneKey]zellij.Pane)
	inspected := make(map[registry.SessionID]bool)
	for _, record := range records {
		if isTerminalStatus(record.Status) || inspected[record.SessionID] {
			continue
		}
		inspected[record.SessionID] = true
		if active != nil && !active[string(record.SessionID)] {
			continue
		}
		panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: string(record.SessionID)})
		if err != nil {
			return fmt.Errorf("recover session %q: %w", record.SessionID, err)
		}
		for _, pane := range panes {
			if !pane.IsPlugin {
				live[livePaneKey{session: record.SessionID, paneID: registry.ZellijPaneID(pane.ID)}] = pane
			}
		}
	}
	for _, record := range records {
		// Bind generation before reconcile so terminal notifications can remove agents.
		if s.observer != nil {
			s.observer.PaneOpened(record)
		}
		candidate, exists := live[livePaneKey{session: record.SessionID, paneID: record.ZellijPaneID}]
		if !isTerminalStatus(record.Status) && (!exists || record.OwnershipToken == "" || (record.ZellijTabID != nil && int(*record.ZellijTabID) != candidate.TabID)) {
			updated, _, err := s.registry.ClaimPaneClosureGeneration(record.ID, record.Generation, registry.PaneStatusLost, "stored pane identity could not be recovered")
			if err != nil {
				return err
			}
			s.notifyReconciledPaneClosed(updated)
			continue
		}
		current, err := s.reconcileRecord(record, live)
		if err != nil {
			return err
		}
		if !isTerminalStatus(current.Status) && s.subs != nil {
			s.subs.StartPane(current.ID)
		}
	}
	return nil
}

// StopObservations stops background producers without closing Zellij panes.
func (s *Service) StopObservations() {
	if s.subs != nil {
		s.subs.Close()
	}
}
