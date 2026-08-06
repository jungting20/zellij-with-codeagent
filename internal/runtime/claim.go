package runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

// ClaimPane registers an existing terminal pane without changing its Zellij state.
func (s *Service) ClaimPane(ctx context.Context, req ClaimPaneRequest) (ClaimPaneResponse, error) {
	req.ID = PaneID(strings.TrimSpace(string(req.ID)))
	req.TaskID = TaskID(strings.TrimSpace(string(req.TaskID)))
	req.AgentID = AgentID(strings.TrimSpace(string(req.AgentID)))
	req.Role = strings.TrimSpace(req.Role)
	req.ZellijSession = strings.TrimSpace(req.ZellijSession)
	req.ZellijPaneID = ZellijPaneID(strings.TrimSpace(string(req.ZellijPaneID)))

	if req.ZellijSession == "" {
		return ClaimPaneResponse{}, ErrZellijSessionRequired
	}
	if req.ID == "" {
		return ClaimPaneResponse{}, ErrMissingPaneID
	}
	if req.ZellijPaneID == "" {
		return ClaimPaneResponse{}, fmt.Errorf("%w: Zellij pane ID is required", ErrInvalidPaneTarget)
	}

	panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: req.ZellijSession})
	if err != nil {
		return ClaimPaneResponse{}, err
	}

	matches := make([]zellij.Pane, 0, 1)
	for _, pane := range panes {
		if pane.ID == zellij.PaneID(req.ZellijPaneID) {
			matches = append(matches, pane)
		}
	}
	switch len(matches) {
	case 0:
		return ClaimPaneResponse{}, fmt.Errorf("%w: Zellij pane %s", ErrPaneNotFound, req.ZellijPaneID)
	case 1:
		if matches[0].IsPlugin {
			return ClaimPaneResponse{}, fmt.Errorf("%w: Zellij pane %s is a plugin pane", ErrInvalidPaneTarget, req.ZellijPaneID)
		}
	default:
		return ClaimPaneResponse{}, fmt.Errorf("%w: Zellij pane %s is ambiguous", ErrInvalidPaneTarget, req.ZellijPaneID)
	}

	match := matches[0]
	ownershipToken, err := s.newOwnershipToken()
	if err != nil {
		return ClaimPaneResponse{}, fmt.Errorf("generate pane ownership token: %w", err)
	}
	tabID := registry.ZellijTabID(match.TabID)
	record, err := s.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:             registry.PaneID(req.ID),
		OwnershipToken: registry.OwnershipToken(ownershipToken),
		SessionID:      registry.SessionID(req.ZellijSession),
		TabID:          registry.TabID(strconv.Itoa(match.TabID)),
		TaskID:         registry.TaskID(req.TaskID),
		AgentID:        registry.AgentID(req.AgentID),
		ZellijPaneID:   registry.ZellijPaneID(req.ZellijPaneID),
		ZellijTabID:    &tabID,
		TabName:        match.TabName,
		Role:           req.Role,
		Command:        cloneStrings(req.Command),
		CWD:            req.CWD,
	})
	if err != nil {
		if errors.Is(err, registry.ErrZellijPaneAlreadyRegistered) {
			return ClaimPaneResponse{}, fmt.Errorf("%w: Zellij pane %s is already managed", ErrInvalidPaneTarget, req.ZellijPaneID)
		}
		return ClaimPaneResponse{}, err
	}

	if s.observer != nil {
		s.observer.PaneOpened(record)
	}
	if s.subs != nil {
		s.subs.StartPane(registry.PaneID(req.ID))
	}

	return ClaimPaneResponse{Pane: paneFromRecord(record)}, nil
}
