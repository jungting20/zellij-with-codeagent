package runtime

import (
	"context"
	"fmt"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

// resolveParentTab reconstructs grouping from the registry and verifies the live
// sibling location. No separate tab cache needs to survive daemon restart.
// The caller holds parentTabMu until the new pane is registered.
func (s *Service) resolveParentTab(ctx context.Context, req CreatePaneRequest) (CreatePaneRequest, error) {
	if err := ctx.Err(); err != nil {
		return req, err
	}
	if req.ParentPaneID == "" || !req.NewTab || req.ZellijTabID != nil || req.SameTabAsPaneID != "" || req.SameTabAsZellijPaneID != "" {
		return req, fmt.Errorf("%w: parent tab reuse requires a parent and new-tab fallback", ErrInvalidPaneTarget)
	}
	var siblings []registry.PaneRecord
	for _, r := range s.registry.ListPanes() {
		if string(r.ParentPaneID) == string(req.ParentPaneID) && string(r.SessionID) == req.ZellijSession &&
			(r.Status == registry.PaneStatusStarting || r.Status == registry.PaneStatusRunning) && r.ZellijPaneID != "" {
			siblings = append(siblings, r)
		}
	}
	if len(siblings) == 0 {
		return req, nil
	}
	panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: req.ZellijSession})
	if err != nil {
		return req, fmt.Errorf("inspect sibling worktree panes: %w", err)
	}
	for _, sibling := range siblings {
		for _, pane := range panes {
			if string(pane.ID) != string(sibling.ZellijPaneID) || pane.IsPlugin || pane.Exited {
				continue
			}
			tabID := ZellijTabID(pane.TabID)
			req.NewTab = false
			req.ZellijTabID = &tabID
			req.TabName = pane.TabName
			return req, nil
		}
	}
	return req, nil
}
