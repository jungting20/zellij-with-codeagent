# Work Registry Cleanup Reuse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make successful runtime/dashboard cleanup release logical pane IDs so the same `work` plan can run again through the same daemon, without delayed operations from the old run affecting the replacement run.

**Architecture:** Registry records receive a monotonically increasing generation. Every asynchronous operation captures the record it started with and uses generation-conditional status, output, removal, and subscription-stop operations. Successful cleanup removes the current generation instead of retaining a terminal tombstone. Subscription state is owned by `(logical pane ID, generation)` and is revalidated during installation.

**Tech stack:** Go, standard `testing`, `RuntimeService`, registry, subscription manager, fake Zellij backend.

## Constraints

- Work directly on `main`; do not create a worktree or commit.
- Preserve the runtime boundary: clients and planners do not call Zellij directly.
- Cleanup never removes unmanaged panes or records outside its filters.
- Backend close failures remain registered as `error` for diagnosis and retry.
- Successful cleanup responses retain closed pane metadata after registry release.
- Delayed old-generation operations must not mutate, remove, stop, or report events for a replacement pane.
- Rebuild `bin/zellij-agent` and immediately copy it to `~/.config/custom-cli`.

## Task 1: Release successful cleanup records

**Files:**

- `internal/runtime/cleanup.go`
- `internal/runtime/cleanup_test.go`
- `internal/runtime/execution_plan_test.go`

**Implementation:**

- Add `releaseCleanupRecord`, using `RemovePaneGeneration(record.ID, record.Generation)`.
- Treat `ErrNotFound` and `ErrStaleRecord` as idempotent race outcomes.
- Release matching terminal records as `Skipped`.
- After a successful backend close, stop only the captured subscription generation and release the record as `Closed`.
- On backend close failure, update only the captured generation. Keep a genuinely current failure registered as `error`; treat missing/stale outcomes as cleanup already completed elsewhere.

**Regression coverage:**

- Matching and partial cleanup release successful records.
- Terminal tombstones are released.
- Subscription-first removal does not create a false partial failure.
- `ApplyExecutionPlan -> Cleanup -> ApplyExecutionPlan` reuses the same logical pane IDs.

## Task 2: Add registry generation guards

**Files:**

- `internal/registry/types.go`
- `internal/registry/registry.go`
- `internal/registry/registry_test.go`

**Implementation:**

- Add `PaneRecord.Generation` and `Registry.nextGeneration`.
- Increment the generation for every successful registration.
- Add `ErrStaleRecord`.
- Add generation-conditional APIs:
  - `UpdatePaneStatusGeneration`
  - `UpdatePaneOutputGeneration`
  - `RemovePaneGeneration`
- Preserve existing generation-blind methods for synchronous compatibility by delegating with generation `0` as a wildcard.

**Regression coverage:**

- After removing and re-registering the same logical ID, old-generation status, output, and removal operations all fail with `ErrStaleRecord` and leave the replacement untouched.

## Task 3: Isolate subscription generations

**Files:**

- `internal/runtime/subscriptions.go`
- `internal/runtime/subscriptions_test.go`

**Implementation:**

- Store `paneSubscription` ownership containing context, cancel function, done signal, and `(pane ID, generation)` key.
- Let teardown remove a map entry only when it still owns that exact subscription.
- Add `StopPaneGeneration`; cleanup, close, and reconcile use it instead of generation-blind stop.
- During `StartPane`, install a captured generation, replace only older subscriptions, re-read the registry, detach stale installs, and retry with the current record.
- Use generation-conditional status/output/removal operations throughout a subscription run.
- Suppress startup, parse, stream, exit, and health events when the subscription is canceled, detached, or stale.
- Treat an initial `ErrNotFound` lookup as expected detach and stay silent.
- Key viewport deduplication by pane ID and generation.

**Regression coverage:**

- Cleanup-first and subscription-first close races are idempotent.
- An old run cannot remove a replacement record or replacement subscription.
- Canceled startup cannot publish stale errors.
- A record change during subscription installation retries and subscribes the replacement.
- An old-generation stop cannot cancel the replacement subscription.
- Starting a missing/detached pane emits no misleading health event.

## Task 4: Guard other asynchronous runtime paths

**Files:**

- `internal/runtime/service.go`
- `internal/runtime/service_test.go`
- `internal/runtime/execution_plan.go`
- `internal/runtime/execution_plan_test.go`
- `internal/runtime/reconcile.go`
- `internal/runtime/reconcile_test.go`

**Implementation:**

- Use captured generations for SendInput/SendMessage error status updates, SnapshotOutput status/output updates, and ClosePane status updates.
- Retain each execution-plan pane's internal registry record alongside its public result. Initial input validates that generation and targets the captured Zellij pane; rollback closes the captured Zellij pane, stops only that subscription generation, and removes only that registry generation.
- Use captured generations for reconcile status changes and removals.
- Validate the generation even in reconcile branches that otherwise perform no registry mutation.
- Skip stale/missing reconcile snapshot entries without publishing a runtime failure.

**Regression coverage:**

- A delayed snapshot cannot write old output into a replacement pane.
- Delayed initial-input delivery cannot send an old goal to a replacement pane.
- An old execution-plan rollback cannot close or remove a replacement pane.
- A delayed reconcile mutation cannot mark or remove a replacement pane.
- Reconcile does not return a phantom old pane from an already-running/terminal fast path.

## Task 5: Document and verify same-daemon reuse

**Files:**

- `docs/manual-smoke-test.md`
- `docs/next-steps-todolist.md`

**Documentation:**

- Add a manual dashboard cleanup and same-command resubmission flow using one daemon and socket.
- Mark same-daemon logical-ID reuse as complete in Phase A.

**Verification:**

```bash
gofmt -w <edited-go-files>
go test ./... -count=1
./scripts/test-race-core.sh
git diff --check
go build -p 1 -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
shasum -a 256 bin/zellij-agent ~/.config/custom-cli/zellij-agent
```

For the real lifecycle smoke, use one isolated daemon, submit `work`, confirm cleanup in the dashboard, and submit the identical `work` command again. The second submission must create all panes without `registry record already exists`. Do not commit; hand the diff back to the user for review.
