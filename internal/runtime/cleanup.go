package runtime

import (
	"context"
	"errors"
	"fmt"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

func (s *Service) Cleanup(ctx context.Context, req CleanupRequest) (CleanupResponse, error) {
	records := s.registry.ListPanes()
	requested := requestedPaneIDs(req.PaneIDs)
	targets := requestedCleanupTargets(req.Targets)
	qualifiedOnly := len(targets) > 0
	requestedOnly := len(requested) > 0 && !qualifiedOnly
	response := CleanupResponse{}

	for _, record := range records {
		if qualifiedOnly {
			target, ok := targets[PaneID(record.ID)]
			if !ok {
				continue
			}
			delete(targets, PaneID(record.ID))
			if target.OwnershipToken == "" || target.OwnershipToken != OwnershipToken(record.OwnershipToken) {
				response.Skipped = append(response.Skipped, paneFromRecord(record))
				continue
			}
		}
		if !qualifiedOnly && !cleanupMatches(record, req, requested, requestedOnly) {
			continue
		}
		delete(requested, PaneID(record.ID))

		if isTerminalStatus(record.Status) {
			if s.subs != nil {
				s.subs.StopPaneGeneration(record.ID, record.Generation)
			}
			released, err := s.releaseCleanupRecord(record)
			if err != nil {
				response.Failed = append(response.Failed, CleanupFailure{
					Pane:  paneFromRecord(record),
					Error: err.Error(),
				})
				continue
			}
			response.Skipped = append(response.Skipped, paneFromRecord(released))
			continue
		}

		if err := s.backend.ClosePane(ctx, zellij.ClosePaneRequest{Session: string(record.SessionID), PaneID: zellij.PaneID(record.ZellijPaneID)}); err != nil {
			updated, updateErr := s.registry.UpdatePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusError, err.Error())
			if errors.Is(updateErr, registry.ErrNotFound) || errors.Is(updateErr, registry.ErrStaleRecord) {
				record.Status = registry.PaneStatusClosed
				record.StatusMessage = "closed before runtime cleanup"
				response.Skipped = append(response.Skipped, paneFromRecord(record))
				continue
			}
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

		if s.subs != nil {
			s.subs.StopPaneGeneration(record.ID, record.Generation)
		}
		released, err := s.releaseCleanupRecord(record)
		if err != nil {
			response.Failed = append(response.Failed, CleanupFailure{
				Pane:  paneFromRecord(record),
				Error: err.Error(),
			})
			continue
		}
		released.Status = registry.PaneStatusClosed
		released.StatusMessage = "closed by runtime cleanup"
		response.Closed = append(response.Closed, paneFromRecord(released))
	}
	if qualifiedOnly {
		for id := range targets {
			response.Failed = append(response.Failed, CleanupFailure{Pane: Pane{ID: id}, Error: ErrPaneNotFound.Error()})
		}
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

func requestedCleanupTargets(targets []CleanupTarget) map[PaneID]CleanupTarget {
	requested := make(map[PaneID]CleanupTarget, len(targets))
	for _, target := range targets {
		if target.PaneID != "" {
			requested[target.PaneID] = target
		}
	}
	return requested
}

func (s *Service) releaseCleanupRecord(record registry.PaneRecord) (registry.PaneRecord, error) {
	removed, err := s.registry.RemovePaneGeneration(record.ID, record.Generation)
	if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrStaleRecord) {
		return record, nil
	}
	if err != nil {
		return registry.PaneRecord{}, err
	}
	return removed, nil
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
	if req.Role != "" && req.Role != record.Role {
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
