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
