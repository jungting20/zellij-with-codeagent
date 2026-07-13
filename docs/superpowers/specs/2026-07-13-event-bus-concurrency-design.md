# Event Bus Concurrency Fix Design

## Goal

Remove the data race between event publication and subscription shutdown while preserving the event bus's existing public behavior.

The fix must keep event publication non-blocking for slow subscribers, close subscription channels when their subscriptions end, retain recent event history, and avoid broad runtime or transport changes.

## Problem

`eventbus.Bus.Publish` currently locks the bus, records the event, copies subscriber channels, and unlocks before sending to those channels. A subscription can be unregistered after its channel is copied but before `Publish` sends to it. Unregistration removes and closes the channel, so the later send races with the close and can panic.

The failure is reproducible with:

```bash
go test -race -count=1 ./internal/eventbus ./internal/runtime ./internal/transport
```

The race detector identifies the competing operations in `internal/eventbus/bus.go`: subscription channel closure in `Subscribe`'s unregister function and channel send in `Publish`.

## Requirements

- Publishing and subscription shutdown must not send to and close the same channel concurrently.
- A slow subscriber must not block publishers.
- Subscription cancellation, explicit unregister, and bus close must still close subscriber channels.
- Each subscriber channel must be closed at most once.
- Publishing after bus close must remain a no-op.
- Recent event history behavior and ordering must remain unchanged.
- The fix must stay within the event bus boundary unless tests reveal another root cause.

## Non-Goals

- Durable event history.
- Guaranteed delivery or retry for dropped events.
- Event sequence numbers, cursors, or reconnect support.
- Changing runtime subscription restart behavior.
- Replacing the event bus with another messaging implementation.

## Approaches Considered

### 1. Deliver While Holding the Bus Lock

Keep the bus mutex locked while iterating over current subscribers and performing non-blocking sends.

This is the selected approach. It is the smallest change and uses the existing mutex as the single lifetime boundary for subscriber registration, delivery, removal, and channel closure. The send operation uses a `select` with a `default` branch, so it cannot wait on a slow subscriber.

The trade-off is that subscription registration and cancellation wait while one publication scans the subscriber map. This is acceptable for a local daemon with a small subscriber set and bounded, non-blocking work per subscriber.

### 2. Add Per-Subscriber State and Locking

Replace raw channels in the subscriber map with objects that coordinate closed state and delivery through their own mutexes.

This could reduce time under the bus-wide lock, but it introduces additional lock ordering and lifecycle complexity without a demonstrated scaling requirement.

### 3. Stop Closing Subscription Channels

Use a separate completion signal and leave event channels open.

This avoids send-versus-close races but changes the documented subscription contract and risks leaving consumers waiting indefinitely. It is not selected.

## Design

`Bus.Publish` will acquire the bus mutex and keep it until event delivery attempts finish. While holding the mutex, it will:

1. Return immediately if the bus is closed.
2. Append the event to bounded recent history and prune overflow as it does today.
3. Iterate over the current subscriber map.
4. Attempt a non-blocking send to each channel.
5. Drop the event for a subscriber whose buffer is full.
6. Release the mutex after all delivery attempts finish.

The temporary copied channel slice is no longer needed.

The unregister function and `Bus.Close` will continue to acquire the same mutex before removing and closing channels. Therefore, they cannot close a channel while `Publish` is attempting to send to it. After `Publish` releases the mutex, unregister or close may safely remove and close the channel.

The existing per-subscription `sync.Once`, combined with checking membership in the subscriber map, continues to ensure that explicit unregister and context cancellation do not close a channel twice. If `Bus.Close` wins first, it removes the channel, and a later unregister call finds no subscriber to close.

## Concurrency Invariants

- The bus mutex protects `closed`, `subs`, and `history`.
- A channel can be closed only while the bus mutex is held.
- An event can be sent to a subscriber channel only while the bus mutex is held.
- Delivery to a full channel is dropped immediately rather than blocking.
- No event is delivered after `Bus.Close` obtains the mutex and marks the bus closed.

## Testing

Development will follow a red-green cycle:

1. Add an event bus regression test that repeatedly races `Publish` against unregister or context cancellation.
2. Run the focused test with the race detector and confirm it fails for the existing send-versus-close race.
3. Apply the minimal `Publish` locking change.
4. Re-run the focused race test and confirm it passes.
5. Run the existing event bus tests, the core race suite, and the full repository test suite.

Verification commands:

```bash
go test -race -count=1 ./internal/eventbus
go test -race -count=1 ./internal/eventbus ./internal/runtime ./internal/transport
go test ./...
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

## Documentation Update

After verification, update `docs/next-steps-todolist.md` to mark the implemented P0 tasks complete. The dropped-event diagnostics decision will remain unchecked unless the implementation adds that behavior; it is not required for the concurrency fix.
