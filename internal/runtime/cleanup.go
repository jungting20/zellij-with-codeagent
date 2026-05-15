package runtime

import (
	"context"
	"fmt"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func (s *Service) Cleanup(ctx context.Context, req CleanupRequest) (CleanupResponse, error) {
	records := s.registry.ListPanes()
	requested := requestedPaneIDs(req.PaneIDs)
	requestedOnly := len(requested) > 0
	response := CleanupResponse{}

	for _, record := range records {
		if !cleanupMatches(record, req, requested, requestedOnly) {
			continue
		}
		delete(requested, PaneID(record.ID))

		if isTerminalStatus(record.Status) {
			response.Skipped = append(response.Skipped, paneFromRecord(record))
			continue
		}

		if err := s.backend.ClosePane(ctx, zellij.ClosePaneRequest{PaneID: zellij.PaneID(record.ZellijPaneID)}); err != nil {
			updated, updateErr := s.registry.UpdatePaneStatus(record.ID, registry.PaneStatusError, err.Error())
			if updateErr != nil {
				response.Failed = append(response.Failed, CleanupFailure{
					Pane:  paneFromRecord(record),
					Error: errorsMessage(err, updateErr),
				})
				continue
			}
			response.Failed = append(response.Failed, CleanupFailure{
				Pane:  paneFromRecord(updated),
				Error: err.Error(),
			})
			continue
		}

		updated, err := s.registry.UpdatePaneStatus(record.ID, registry.PaneStatusClosed, "closed by runtime cleanup")
		if err != nil {
			response.Failed = append(response.Failed, CleanupFailure{
				Pane:  paneFromRecord(record),
				Error: err.Error(),
			})
			continue
		}
		if s.subs != nil {
			s.subs.StopPane(record.ID)
		}
		response.Closed = append(response.Closed, paneFromRecord(updated))
	}

	if requestedOnly {
		for id := range requested {
			response.Failed = append(response.Failed, CleanupFailure{
				Pane:  Pane{ID: id},
				Error: ErrPaneNotFound.Error(),
			})
		}
	}

	if len(response.Failed) > 0 {
		return response, fmt.Errorf("%w: %d pane(s) failed", ErrCleanupPartial, len(response.Failed))
	}
	return response, nil
}

func requestedPaneIDs(ids []PaneID) map[PaneID]bool {
	requested := make(map[PaneID]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			requested[id] = true
		}
	}
	return requested
}

func cleanupMatches(record registry.PaneRecord, req CleanupRequest, requested map[PaneID]bool, requestedOnly bool) bool {
	if requestedOnly {
		return requested[PaneID(record.ID)]
	}
	if req.TaskID != "" && registry.TaskID(req.TaskID) != record.TaskID {
		return false
	}
	if req.Role != "" && registry.PaneRole(req.Role) != record.Role {
		return false
	}
	return true
}

func isTerminalStatus(status registry.PaneStatus) bool {
	switch status {
	case registry.PaneStatusClosed, registry.PaneStatusExited, registry.PaneStatusLost:
		return true
	default:
		return false
	}
}

func errorsMessage(err error, updateErr error) string {
	if updateErr == nil {
		return err.Error()
	}
	return fmt.Sprintf("%v; registry update failed: %v", err, updateErr)
}
