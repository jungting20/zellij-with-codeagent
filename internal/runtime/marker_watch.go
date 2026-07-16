package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"zellij-with-codeagent/internal/eventbus"
)

func (s *Service) WaitForOutputMarker(ctx context.Context, req WaitForOutputMarkerRequest) (WaitForOutputMarkerResponse, error) {
	if strings.TrimSpace(req.Marker) == "" || strings.ContainsAny(req.Marker, "\r\n") {
		return WaitForOutputMarkerResponse{}, fmt.Errorf("runtime: marker must be one non-empty line")
	}

	subscriptionCtx, cancelSubscription := context.WithCancel(ctx)
	defer cancelSubscription()
	events, unsubscribe := s.bus.Subscribe(subscriptionCtx)
	defer unsubscribe()

	if err := ctx.Err(); err != nil {
		return WaitForOutputMarkerResponse{}, err
	}
	inspected, err := s.InspectPane(ctx, InspectPaneRequest{PaneID: req.PaneID})
	if err != nil {
		return WaitForOutputMarkerResponse{}, err
	}
	if line, ok := findMarkerLine(inspected.Pane.LastOutput, req.Marker, req.MatchPrefix); ok {
		return WaitForOutputMarkerResponse{
			PaneID:      req.PaneID,
			Marker:      req.Marker,
			MatchedLine: line,
			MatchedAt:   time.Now(),
		}, nil
	}

	for {
		select {
		case <-ctx.Done():
			return WaitForOutputMarkerResponse{}, ctx.Err()
		case event, ok := <-events:
			if !ok {
				if err := ctx.Err(); err != nil {
					return WaitForOutputMarkerResponse{}, err
				}
				return WaitForOutputMarkerResponse{}, errors.New("runtime: event subscription closed")
			}
			if event.Type != eventbus.TypeRawOutput || event.PaneID != string(req.PaneID) {
				continue
			}
			line, ok := findMarkerLine(event.Message, req.Marker, req.MatchPrefix)
			if !ok {
				continue
			}
			return WaitForOutputMarkerResponse{
				PaneID:      req.PaneID,
				Marker:      req.Marker,
				MatchedLine: line,
				MatchedAt:   event.Time,
			}, nil
		}
	}
}

func findMarkerLine(text, marker string, matchPrefix bool) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if (!matchPrefix && line == marker) || (matchPrefix && strings.HasPrefix(line, marker)) {
			return line, true
		}
	}
	return "", false
}
