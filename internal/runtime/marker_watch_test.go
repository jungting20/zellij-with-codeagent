package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
)

func TestWaitForOutputMarkerMatchesExactTrimmedLineForRequestedPane(t *testing.T) {
	service, bus := newMarkerWatchService(t, "worker-1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := make(chan struct {
		response WaitForOutputMarkerResponse
		err      error
	}, 1)
	go func() {
		response, err := service.WaitForOutputMarker(ctx, WaitForOutputMarkerRequest{
			PaneID: "worker-1",
			Marker: "DONE",
		})
		result <- struct {
			response WaitForOutputMarkerResponse
			err      error
		}{response: response, err: err}
	}()

	assertMarkerWaitPending(t, result)
	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-2", Message: "DONE", Time: time.Unix(1, 0)})
	assertMarkerWaitPending(t, result)
	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-1", Message: "NOT_DONE", Time: time.Unix(2, 0)})
	assertMarkerWaitPending(t, result)

	matchedAt := time.Unix(3, 0)
	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-1", Message: "log\n  DONE  \n", Time: matchedAt})
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("WaitForOutputMarker() error = %v", got.err)
		}
		if got.response.PaneID != "worker-1" || got.response.Marker != "DONE" || got.response.MatchedLine != "DONE" || !got.response.MatchedAt.Equal(matchedAt) {
			t.Fatalf("WaitForOutputMarker() = %#v, want worker-1 DONE at %v", got.response, matchedAt)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for exact marker")
	}
}

func TestWaitForOutputMarkerMatchesAlreadyVisibleOutput(t *testing.T) {
	service, _ := newMarkerWatchService(t, "worker-1")
	if _, err := service.registry.UpdatePaneOutput("worker-1", "starting\n  DONE  \n"); err != nil {
		t.Fatalf("UpdatePaneOutput() error = %v", err)
	}

	before := time.Now()
	response, err := service.WaitForOutputMarker(context.Background(), WaitForOutputMarkerRequest{
		PaneID: "worker-1",
		Marker: "DONE",
	})
	after := time.Now()
	if err != nil {
		t.Fatalf("WaitForOutputMarker() error = %v", err)
	}
	if response.PaneID != "worker-1" || response.Marker != "DONE" || response.MatchedLine != "DONE" {
		t.Fatalf("WaitForOutputMarker() = %#v, want worker-1 DONE", response)
	}
	if response.MatchedAt.Before(before) || response.MatchedAt.After(after) {
		t.Fatalf("MatchedAt = %v, want between %v and %v", response.MatchedAt, before, after)
	}
}

func TestWaitForOutputMarkerMatchesPrefixAndReturnsFullLine(t *testing.T) {
	service, bus := newMarkerWatchService(t, "worker-1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := make(chan struct {
		response WaitForOutputMarkerResponse
		err      error
	}, 1)
	go func() {
		response, err := service.WaitForOutputMarker(ctx, WaitForOutputMarkerRequest{
			PaneID:      "worker-1",
			Marker:      "ZELLIJ_AGENT_WORKER_DONE ",
			MatchPrefix: true,
		})
		result <- struct {
			response WaitForOutputMarkerResponse
			err      error
		}{response: response, err: err}
	}()

	assertMarkerWaitPending(t, result)
	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-2", Message: "ZELLIJ_AGENT_WORKER_DONE ticket_id=OTHER"})
	assertMarkerWaitPending(t, result)
	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-1", Message: "ZELLIJ_AGENT_WORKER_DONE"})
	assertMarkerWaitPending(t, result)

	matchedAt := time.Unix(4, 0)
	line := "ZELLIJ_AGENT_WORKER_DONE ticket_id=TICKET-123"
	bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-1", Message: "log\n  " + line + "  \n", Time: matchedAt})

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("WaitForOutputMarker() error = %v", got.err)
		}
		if got.response.PaneID != "worker-1" || got.response.Marker != "ZELLIJ_AGENT_WORKER_DONE " || got.response.MatchedLine != line || !got.response.MatchedAt.Equal(matchedAt) {
			t.Fatalf("WaitForOutputMarker() = %#v", got.response)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for prefix marker")
	}
}

func TestWaitForOutputMarkerRejectsInvalidMarker(t *testing.T) {
	service, _ := newMarkerWatchService(t, "worker-1")
	for _, marker := range []string{"", "   ", "DONE\nNEXT", "DONE\rNEXT"} {
		t.Run(marker, func(t *testing.T) {
			_, err := service.WaitForOutputMarker(context.Background(), WaitForOutputMarkerRequest{
				PaneID: "worker-1",
				Marker: marker,
			})
			if err == nil {
				t.Fatalf("WaitForOutputMarker(marker=%q) error = nil, want invalid marker", marker)
			}
		})
	}
}

func TestWaitForOutputMarkerReturnsPaneNotFound(t *testing.T) {
	service, _ := newMarkerWatchService(t, "worker-1")
	_, err := service.WaitForOutputMarker(context.Background(), WaitForOutputMarkerRequest{
		PaneID: "missing",
		Marker: "DONE",
	})
	if !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("WaitForOutputMarker() error = %v, want %v", err, ErrPaneNotFound)
	}
}

func TestWaitForOutputMarkerReturnsCancellation(t *testing.T) {
	service, _ := newMarkerWatchService(t, "worker-1")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := service.WaitForOutputMarker(ctx, WaitForOutputMarkerRequest{
			PaneID: "worker-1",
			Marker: "DONE",
		})
		result <- err
	}()

	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForOutputMarker() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForOutputMarker() did not return after cancellation")
	}
}

func newMarkerWatchService(t *testing.T, paneID PaneID) (*Service, *eventbus.Bus) {
	t.Helper()
	reg := registry.New()
	if _, err := reg.RegisterPane(registry.RegisterPaneRequest{
		ID:           registry.PaneID(paneID),
		SessionID:    "session-1",
		ZellijPaneID: "terminal_1",
	}); err != nil {
		t.Fatalf("RegisterPane() error = %v", err)
	}
	bus := eventbus.New()
	return NewService(Options{Registry: reg, EventBus: bus}), bus
}

func assertMarkerWaitPending(t *testing.T, result <-chan struct {
	response WaitForOutputMarkerResponse
	err      error
}) {
	t.Helper()
	select {
	case got := <-result:
		t.Fatalf("WaitForOutputMarker() returned early: response=%#v error=%v", got.response, got.err)
	case <-time.After(20 * time.Millisecond):
	}
}
