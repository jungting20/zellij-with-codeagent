# Ticket Worker Race Recovery Design

## Goal

Eliminate two ticket-worker lifecycle races:

1. The manager process tries to create workers before its own logical pane is registered as the same-tab anchor.
2. A completed worker exits and its subscription removes the registry record before the manager closes it, leaving the slot permanently occupied.

The fix stays inside the ticket-worker manager. General runtime close semantics remain strict, and worker commands may exit immediately after printing the exact completion marker.

## Manager Startup Readiness

`ManagerClient` gains the existing transport `InspectRuntime` operation. `Manager.Run` waits for its anchor before the first call to `fillEmptySlots`.

An anchor is ready only when one runtime pane matches all of these values:

- logical pane ID equals `AnchorPaneID`;
- task ID equals `TaskID`;
- physical session ID equals `ZellijSession`;
- status is `starting` or `running`.

The manager polls runtime inspection at a short fixed interval. `ManagerOptions.StartupTimeout` bounds the wait. The internal `ticket-worker manager --timeout` value supplies this timeout, preserving the existing default of 15 seconds.

Runtime inspection errors are retryable during the readiness window. Context cancellation stops the wait immediately. If the deadline expires, `Run` returns an error identifying the anchor that was not ready and creates no workers.

## Completed Worker Close Reconciliation

The manager keeps the existing requirement that only an exact completion-marker response initiates close handling.

After a valid marker:

1. Call `ClosePane` normally.
2. If close succeeds, mark the slot complete and empty.
3. If close fails, call `InspectRuntime`.
4. If no pane matches the worker's logical pane ID, task ID, and physical session, treat the worker as already closed and mark the slot complete and empty.
5. If the pane still exists, or inspection fails, preserve the occupied slot and record the close error.

This rule handles both transport `not_found` and the observed post-close `runtime_error: registry record not found`. It does not classify errors by their text. Absence in current runtime state is the evidence that the completed worker no longer consumes managed capacity.

The already-closed rule applies only after the manager has verified the expected pane ID and exact completion marker. It does not change `RuntimeService.ClosePane`, transport error mapping, or `ctl` behavior.

## Slot State

Successful close and confirmed absence share one completion helper. The helper records `MatchedAt` when provided, otherwise uses the manager clock, logs the outcome, clears `paneID`, sets the slot to empty, and clears `lastError`.

A close error with a still-present pane keeps the existing safety behavior: the slot remains occupied, so the manager cannot exceed configured capacity. A failed inspection also preserves the slot because absence was not established.

Refill continues on the next configured polling tick after a slot becomes empty. Startup readiness does not alter normal capacity, marker, cancellation, or refill behavior.

## Interfaces

`ManagerClient` adds:

```go
InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
```

`ManagerOptions` adds:

```go
StartupTimeout time.Duration
```

The manager CLI passes its parsed `--timeout` value into `StartupTimeout`. Tests may use shorter timeouts and controlled inspection responses.

## Errors and Logging

- Readiness timeout returns a concise anchor-not-ready error and performs zero worker creates.
- Context cancellation returns the context error and performs zero worker creates when cancellation happens before readiness.
- Readiness inspection errors are retained as context for the final timeout error when available.
- A close error followed by confirmed absence logs that the worker was already closed and releases the slot.
- A close error followed by a present worker or failed inspection logs the close failure and preserves the slot.

## Testing

Automated tests cover:

- zero worker creates before anchor readiness;
- immediate initial fill after the matching anchor appears;
- rejection of same-ID panes with the wrong task or physical session;
- readiness timeout and context cancellation;
- existing successful close and next-tick refill behavior;
- close `not_found` with an absent record releasing the slot;
- close `runtime_error` with an absent record releasing the slot;
- close failure with a present record preserving capacity;
- close failure plus inspection failure preserving capacity;
- no regression to marker validation or manager cancellation behavior.

Verification uses `go test ./...`. After implementation, build `bin/zellij-agent`, immediately copy it to `~/.config/custom-cli`, and compare the registered artifact with the build.
