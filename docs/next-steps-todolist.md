# Next Steps Todo List

Updated: 2026-07-13

## Current Baseline

- [x] `zellij-agent` provides unified `daemon`, `ctl`, `planner`, `work`, `chrome`, `code-review`, `debate-background`, and `role` command groups.
- [x] The daemon owns managed Zellij pane creation, input, snapshots, events, reconciliation, cleanup, and session inspection through `RuntimeService` and the Unix-socket transport.
- [x] `work` creates a mixed coding workspace, debate flows coordinate multiple agents, and the Chrome roles track tabs and network requests.
- [x] `go test ./...` passes as of 2026-07-13.
- [x] `./scripts/test-race-core.sh` passes without concurrent event publication and subscription shutdown races.

## Recommended Order

1. Stabilize the event bus concurrency boundary.
2. Build a runtime supervisor dashboard.
3. Make `work` goal-aware, then add a bounded deterministic feedback loop.
4. Consolidate and productize the Chrome network workflow.

This order first removes a daemon reliability risk, then turns the existing runtime data into a visible product, and only after that adds more autonomous behavior.

## P0. Fix Event Bus Concurrency

### Why

Before this fix, the following command reported a data race between closing a subscriber channel and publishing to a copied channel reference:

```bash
go test -race ./internal/eventbus ./internal/runtime ./internal/transport
```

`Bus.Publish` copied subscriber channels while holding the bus lock and sent after releasing it. Subscription cancellation could remove and close one of those channels before the send completed. Publication and subscriber shutdown now share the bus lifetime lock.

### Tasks

- [x] Add a regression test that repeatedly publishes while subscriptions are canceled or unregistered.
- [x] Synchronize subscriber lifetime so publish, unregister, and bus close cannot race or send to a closed channel.
- [x] Preserve the current non-blocking policy for slow subscribers.
- [x] Decide whether dropped events need a counter or diagnostic event. Deferred because delivery diagnostics are outside this concurrency fix.
- [x] Add race-enabled core tests to a checked-in verification script.

### Verification

```bash
./scripts/test-race-core.sh
go test ./...
```

## P1. Add `zellij-agent dashboard`

### Goal

Turn the existing runtime introspection and event data into a live supervisor TUI. This is the recommended first user-facing feature because most of the underlying data already exists, while the current CLI requires separate `status`, `events`, `snapshot`, and `cleanup` commands.

### Initial Scope

- [ ] Add a `zellij-agent dashboard` command backed by the existing transport boundary.
- [ ] Show a session/task → tab → pane hierarchy with lifecycle status badges.
- [ ] Show the selected pane's latest output and recent semantic events.
- [ ] Support refresh/watch behavior without blocking event publishers.
- [ ] Provide keyboard actions for snapshot, input, reconcile, and task cleanup.
- [ ] Reuse the Bubble Tea and Lip Gloss interaction patterns already used by `tab-network`.
- [ ] Keep the first version local-only and avoid adding LLM behavior or persistence.

### Success Criteria

- A user can understand the current runtime state without opening separate status and event panes.
- A user can inspect and intervene in a managed pane without leaving the dashboard.
- Dashboard actions continue to use the transport or `RuntimeService`; they never call Zellij directly.
- Model/update/view logic has unit tests, and a real-Zellij manual smoke flow is documented.

## P2. Make `work` Goal-Aware

### Phase A: Project-Adaptive Launcher

- [ ] Deliver the supplied goal to the interactive coder pane as its initial prompt.
- [ ] Detect common project types such as Go, Node, and Rust.
- [ ] Select useful default test/build commands from detected project files.
- [ ] Add explicit overrides such as `--profile`, `--test-command`, `--no-review`, and `--no-lazygit` only where they solve a real workflow need.
- [ ] Check optional tools such as `codex` and `lazygit` before submission and print actionable fallback information.

### Phase B: Bounded Deterministic Feedback Loop

- [ ] Start with a preset workflow rather than a general LLM planner.
- [ ] Observe `test_failed` and `test_passed` events from the runtime.
- [ ] On failure, capture the test pane snapshot and forward the evidence to the coder pane.
- [ ] Re-run the configured test command after the coder reports readiness.
- [ ] Stop on success, user cancellation, timeout, or a configured retry limit.
- [ ] Keep explicit user intervention points visible in the dashboard.

### Success Criteria

- `zellij-agent work "<goal>"` opens a workspace that is immediately oriented around that goal.
- At least one Go-project preset can execute a failure → evidence forwarding → retry → pass flow without an LLM choosing arbitrary runtime operations.
- The loop cannot retry forever or clean up unmanaged panes.

## P3. Productize the Chrome Network Workflow

### Goal

Build on the recent Chrome tab and network work while removing the ambiguity between `tab-watcher` and `tab-network --spawn-on-new-tab`.

### Tasks

- [ ] Choose one component as the official owner of new-tab detection and pane lifecycle.
- [ ] Deprecate or simplify the overlapping watcher path.
- [ ] Reshape the user-facing flow around clear operations such as `chrome start`, `attach`, `list`, and `stop`.
- [ ] Promote commonly used forwarded options to discoverable top-level flags.
- [ ] Export a selected request as cURL.
- [ ] Export captured requests as HAR.
- [ ] Compare repeated requests or responses for the same API endpoint.
- [ ] Define and test tab-close and managed-pane cleanup behavior.
- [ ] Update the Chrome design document and manual smoke test to match the final lifecycle.

### Success Criteria

- A user can predict which Chrome process, target, and managed pane owns each tracking session.
- Opening and closing tabs does not create duplicate or orphaned managed panes.
- Captured request data can be reused outside the TUI through cURL or HAR export.

## Later, After the First Product Loop

The following work remains valuable but should follow real usage of the dashboard and goal-aware workflow:

- Durable registry, task, request, and event history across daemon restarts.
- Idempotent execution requests with status, cancel, retry, and replay by `request_id`.
- Reconnectable event streams with sequence or cursor support.
- Subscription restart/backoff and meaningful degraded runtime health.
- A general LLM planner that produces validated execution plans and replans from runtime evidence.

Before unattended autonomous operation, persistence, idempotency, and subscription recovery should be promoted from this later list into required reliability work.
