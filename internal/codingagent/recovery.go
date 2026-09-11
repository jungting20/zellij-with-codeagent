package codingagent

import (
	"context"
	"errors"
	"fmt"
	"zellij-with-codeagent/internal/runtime"
)

// RestoreMonitoring rebuilds transient state before RuntimeService recovery.
func (s *Service) RestoreMonitoring(ctx context.Context) error {
	records, err := s.store.List()
	if err != nil {
		return err
	}
	for _, record := range records {
		inspected, err := s.RuntimeService.InspectPane(ctx, runtime.InspectPaneRequest{PaneID: record.PaneID})
		if err != nil {
			if errors.Is(err, runtime.ErrPaneNotFound) {
				if err = s.store.Delete(record.ID); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if inspected.Pane.Role != "coding-agent" || inspected.Pane.AgentID != runtime.AgentID(record.ID) {
			return fmt.Errorf("stored agent %s does not own pane %s", record.ID, record.PaneID)
		}
		changed, err := s.store.UpdateState(record.ID, StateUpdate{State: StateUnknown, Reason: "daemon restarted; awaiting fresh observation"})
		if err != nil {
			return err
		}
		profile, _ := LookupProfile(record.Kind)
		if profile.TracksState {
			if err = s.monitor.Start(changed.Current); err != nil {
				return err
			}
		}
	}
	return nil
}
