package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

const paneInitialInputPollInterval = 50 * time.Millisecond

func (s *Service) initializeCreatedPane(
	ctx context.Context,
	created CreatePaneResponse,
	initialInput string,
	readyText string,
) error {
	if initialInput == "" {
		return nil
	}
	if err := s.waitForPaneInitialInputReady(ctx, created, readyText); err != nil {
		return err
	}
	current, err := s.registry.GetPane(created.record.ID)
	if err != nil {
		return fmt.Errorf("send initial input to pane %q: %w", created.Pane.ID, err)
	}
	if current.Generation != created.record.Generation {
		return fmt.Errorf("send initial input to pane %q: %w", created.Pane.ID, registry.ErrStaleRecord)
	}
	if err := s.backend.SendInput(ctx, zellij.SendInputRequest{
		Session: string(created.record.SessionID),
		PaneID:  zellij.PaneID(created.record.ZellijPaneID),
		Text:    initialInput,
	}); err != nil {
		return fmt.Errorf("send initial input to pane %q: %w", created.Pane.ID, err)
	}
	return nil
}

func (s *Service) waitForPaneInitialInputReady(
	ctx context.Context,
	created CreatePaneResponse,
	readyText string,
) error {
	if readyText == "" {
		return nil
	}
	ticker := time.NewTicker(paneInitialInputPollInterval)
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
			return fmt.Errorf("wait for initial input readiness in pane %q: %w", created.Pane.ID, cause)
		case <-ticker.C:
		}
	}
}
