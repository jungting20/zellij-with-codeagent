package eventbus

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestBusSubscriptionWaitersExit(t *testing.T) {
	for _, action := range []string{"unsubscribe", "close", "cancel"} {
		t.Run(action, func(t *testing.T) {
			// The bubble cannot finish while a subscription goroutine is leaked.
			synctest.Test(t, func(t *testing.T) {
				bus := New()
				for range 20 {
					ctx := context.Background()
					cancel := func() {}
					if action == "cancel" {
						ctx, cancel = context.WithCancel(ctx)
						defer cancel()
					}
					ch, unsubscribe := bus.Subscribe(ctx)
					synctest.Wait()
					switch action {
					case "unsubscribe":
						unsubscribe()
						unsubscribe()
					case "close":
						bus.Close()
						bus.Close()
					case "cancel":
						cancel()
					}
					synctest.Wait()
					select {
					case _, ok := <-ch:
						if ok {
							t.Fatal("subscription is still open")
						}
					default:
						t.Fatal("subscription did not close")
					}
				}
			})
		})
	}
}
