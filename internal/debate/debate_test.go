package debate

import (
	"context"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestWaitForMarkersAcceptsCompactMarkerWithoutFieldSpaces(t *testing.T) {
	marker := completionMarker("debate_1782189440197965000", 1, "agy")
	compactMarker := strings.ReplaceAll(marker, " ", "")
	client := &markerEventClient{
		events: []transport.Event{
			{
				Type:    "raw_output",
				PaneID:  "debate-agy",
				Message: "answer complete\n" + compactMarker + "\n",
			},
		},
	}

	statuses, err := waitForMarkers(context.Background(), client, map[string]string{"debate-agy": marker}, time.Second)
	if err != nil {
		t.Fatalf("waitForMarkers() error = %v", err)
	}
	if got := statuses["debate-agy"].Status; got != paneStatusDone {
		t.Fatalf("status = %q, want %q", got, paneStatusDone)
	}
}

type markerEventClient struct {
	events []transport.Event
}

func (c *markerEventClient) SendInput(context.Context, string, transport.SendInputRequest) error {
	return nil
}

func (c *markerEventClient) SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	return transport.SnapshotOutputResponse{}, nil
}

func (c *markerEventClient) SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	return transport.ExecutionPlanResponse{}, nil
}

func (c *markerEventClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	events := make(chan transport.Event, len(c.events))
	for _, event := range c.events {
		events <- event
	}
	close(events)
	errs := make(chan error)
	close(errs)
	return &transport.EventStream{
		Events: events,
		Errors: errs,
		Close:  func() error { return nil },
	}, nil
}
