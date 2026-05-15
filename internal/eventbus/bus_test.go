package eventbus

import (
	"context"
	"testing"
	"time"
)

func TestBusFanOutDeliversToAllSubscribers(t *testing.T) {
	bus := New()

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	ch1, _ := bus.Subscribe(ctx1)
	ch2, _ := bus.Subscribe(ctx2)

	e := Event{Type: TypeRawOutput, PaneID: "pane-1", Message: "hello", Time: time.Now()}
	bus.Publish(e)

	select {
	case got := <-ch1:
		if got.Message != "hello" || got.PaneID != "pane-1" {
			t.Fatalf("subscriber 1 got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber 1 timed out")
	}

	select {
	case got := <-ch2:
		if got.Message != "hello" {
			t.Fatalf("subscriber 2 got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber 2 timed out")
	}
}

func TestBusSubscribeCancelClosesChannel(t *testing.T) {
	bus := New()

	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := bus.Subscribe(ctx)

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed")
	}
}

func TestBusCloseStopsDelivery(t *testing.T) {
	bus := New()

	ctx := context.Background()
	ch, _ := bus.Subscribe(ctx)

	bus.Close()

	bus.Publish(Event{Type: TypeRawOutput, Message: "after close"})

	select {
	case got, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event after close: %#v", got)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBusRecentReturnsEventsInPublicationOrder(t *testing.T) {
	bus := New()
	base := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	bus.Publish(Event{Type: TypeServerReady, PaneID: "pane-1", Message: "ready", Time: base})
	bus.Publish(Event{Type: TypeTestFailed, PaneID: "pane-2", Message: "failed", Time: base.Add(time.Second)})
	bus.Publish(Event{Type: TypeTestPassed, PaneID: "pane-2", Message: "passed", Time: base.Add(2 * time.Second)})

	recent := bus.Recent(2)
	if len(recent) != 2 {
		t.Fatalf("Recent(2) length = %d, want 2", len(recent))
	}
	if recent[0].Type != TypeTestFailed || recent[1].Type != TypeTestPassed {
		t.Fatalf("Recent(2) = %#v, want last two events in publication order", recent)
	}
}

func TestBusRecentClonesHistory(t *testing.T) {
	bus := New()
	bus.Publish(Event{Type: TypeServerReady, PaneID: "pane-1", Message: "ready"})

	recent := bus.Recent(0)
	recent[0].Message = "mutated"

	again := bus.Recent(0)
	if again[0].Message != "ready" {
		t.Fatalf("Recent() leaked mutable history, got %#v", again[0])
	}
}
