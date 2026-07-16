# Ticket Worker Pane Pool Design

## Summary

Add a project-configured ticket worker pool to `zellij-agent`. A user initializes a small project-local configuration and starts a managed Zellij tab containing a deterministic worker-manager pane, a read-only monitoring pane, and up to a configured number of worker panes.

Each worker pane runs the same project-provided command. That command owns all ticket-specific behavior: atomically claiming the next ticket, invoking the coding agent and its existing skills, implementing and verifying the change, updating the ticket system, and finally printing a fixed completion marker. The worker manager owns only pane-pool lifecycle. It detects the marker for a specific logical pane, closes that pane, and fills the open slot on a later polling tick.

The MVP intentionally excludes failed/waiting worker policy, automatic stalled-worker cleanup, and recovery of workers after the manager restarts.

## Goals

- Start a visible ticket-processing workspace with one command.
- Keep at most `max_workers` ticket worker panes active, with a default of three.
- Let each project provide a single worker command without coupling `zellij-agent` to a ticket system.
- Detect worker completion with an exact, fixed marker scoped by logical pane ID.
- Close completed panes and refill their slots on the next polling tick.
- Show the pool in a read-only monitoring pane.
- Preserve worker panes if the worker manager is interrupted or closed accidentally.
- Keep all Zellij mutations behind `RuntimeService` and the local transport.

## Non-goals

- Claiming tickets inside `zellij-agent`.
- Understanding ticket IDs or ticket-system states in the worker manager.
- Handling worker-specific failed or waiting states.
- Treating unchanged output or process exit as successful completion.
- Automatically retrying or closing stalled workers.
- Recovering or adopting existing worker panes after a manager restart.
- Persisting pool state across daemon restarts.
- Adding controls to the monitoring pane.

## User Experience

From a project root, the user initializes the project configuration:

```bash
zellij-agent ticket-worker init
```

This creates:

```text
.zellij-agent/
└── worker/
    └── config.yaml
```

After configuring the project command, the user starts the workspace:

```bash
zellij-agent ticket-worker start
```

`start` creates a new managed tab with this logical layout:

```text
┌────────────────── ticket-worker tab ──────────────────┐
│ worker-manager pane        │ monitoring pane          │
│ - slot ownership           │ - read-only pool state   │
│ - marker watches           │ - recent completions     │
│ - create and close         │ - unresolved/errors      │
├────────────────┬───────────┴──────┬───────────────────┤
│ worker slot 1  │ worker slot 2    │ worker slot 3     │
└────────────────┴──────────────────┴───────────────────┘
```

The `start` command may override selected configuration for one run:

```bash
zellij-agent ticket-worker start --max-workers 5
```

## Project Configuration

The initial schema is deliberately small:

```yaml
version: 1

max_workers: 3
poll_interval: 30s

worker:
  command: ["go", "run", "./cmd/ticket-worker"]
  completion_marker: "ZELLIJ_AGENT_WORKER_DONE"
```

Semantics:

- `version` must be `1`.
- `max_workers` must be positive and defaults to `3` when omitted.
- `poll_interval` must be positive and defaults to `30s` when omitted.
- `worker.command` is a non-empty argument vector executed from the project root. It is not interpreted through a shell.
- `worker.completion_marker` must be a non-empty single line. Newline characters are rejected.
- Explicit `start` flags override configuration values for that invocation.
- Unknown configuration fields are rejected to catch mistakes early.

`ticket-worker init` refuses to overwrite an existing configuration. `--force` permits replacement. The generated configuration is syntactically valid and contains an example command. `start` validates the schema and non-empty command before creating a tab; failure to execute a project-specific command is reported as a worker infrastructure error after launch.

## Component Responsibilities

### Ticket-worker CLI

The unified binary gains a `ticket-worker` command group:

```text
zellij-agent ticket-worker init
zellij-agent ticket-worker start
```

`init` scaffolds project configuration. `start` discovers the project root, loads and validates the configuration, submits the initial manager/monitoring execution plan, and reports the created workspace. The plan launches implementation-private manager and monitor entrypoints with the absolute configuration path, project root, task/session ID, and manager logical pane ID, so both long-running panes can reconnect to the daemon without depending on the launcher's process lifetime.

### Worker manager

The worker manager is a deterministic long-running process, not an LLM agent. It owns:

- slot state and the `max_workers` limit;
- creation of uniquely identified worker panes;
- one exact-marker watch per active worker pane;
- completion history used by the monitoring view;
- closing completed workers;
- refilling open slots on polling ticks.

Only the manager event loop mutates slot state. Per-pane marker watchers send typed completion results into that loop; they never claim slots or create replacement workers directly.

### Project worker command

The project-provided command owns the domain workflow:

1. atomically claim the next ticket;
2. invoke the coding agent and existing ticket-processing skill;
3. implement and verify the ticket;
4. update the ticket system;
5. print the configured completion marker as an exact standalone line.

If there is no ticket, the project command decides how to represent that outcome. To release the slot and allow a later poll, it may print the same completion marker after confirming there is no work. `zellij-agent` does not interpret ticket-specific results.

### Monitoring pane

The monitoring pane is read-only. It displays the worker-manager session filtered from runtime state, including:

- configured capacity;
- active, completed, and unresolved slots;
- logical pane IDs and lifecycle states;
- pane creation and last-update times;
- infrastructure errors from create, watch, or close operations.

Completed workers are derived from worker panes that the manager closed after a marker match; unresolved workers remain non-closed or have a non-success terminal state. The monitoring pane does not need a separate manager-state API in the MVP. It does not create, close, retry, or send input to workers.

### Runtime and transport

All pane operations continue through `RuntimeService`. The design requires generic runtime/transport capabilities for:

- creating a pane in the same tab as a referenced logical pane ID;
- watching one logical pane for an exact output line;
- closing one logical pane;
- filtering the existing dashboard to one ticket-worker task/session and running it read-only.

The worker manager uses the local transport or its CLI wrappers. It never invokes Zellij directly and never treats physical Zellij pane or tab IDs as contract identifiers.

## Same-tab Pane Creation

The initial manager and monitoring panes are created through an execution plan in a new tab. Each later worker creation references the logical manager pane as its same-tab anchor:

```text
same_tab_as: ticket-worker-manager
        │
        ▼
RuntimeService resolves the manager registry record
        │
        ▼
RuntimeService uses its current ZellijTabID internally
        │
        ▼
worker pane is created in the managed tab
```

Resolution occurs inside the runtime boundary. A missing, terminal, or tab-less anchor fails the create request without falling back to an arbitrary current tab.

## Worker Identity and Slot State

Closed registry records remain observable, so a logical pane ID is not reused. Each worker gets a slot number and monotonically increasing launch sequence:

```text
ticket-worker-slot-1-0001
ticket-worker-slot-2-0001
ticket-worker-slot-1-0002
```

The manager maintains an in-memory record similar to:

```go
type workerSlot struct {
    Number       int
    Sequence     uint64
    PaneID       string
    State        slotState
    StartedAt    time.Time
    CompletedAt  time.Time
    LastError    string
}
```

The manager has a single event loop. Its inputs are:

- polling ticks;
- marker-match results;
- marker-watch failures;
- pane create results;
- pane close results;
- process cancellation.

## Completion Detection

Completion is scoped by both logical pane ID and exact marker equality:

```text
event.pane_id == watchedPaneID
and trim(event.line) == configuredCompletionMarker
```

Substring matches do not count. A marker from another pane only completes that pane's watch. The fixed marker is safe because each watcher is already bound to a unique logical pane ID; a per-run nonce is unnecessary.

The marker-watch path keeps only the bounded data required to recognize one line. Unrelated worker output is not accumulated by the manager. Existing runtime output summaries and event history retain their current bounded behavior for dashboard and diagnostics consumers.

When a marker matches:

1. the watcher completes and unsubscribes;
2. the manager asks the runtime to close that logical pane;
3. after a successful close, the slot becomes empty and completion history is updated;
4. the next polling tick may fill the slot with a new uniquely identified pane.

A process exit, pane-close event, unchanged output, or five minutes of silence is not a completion signal.

## Polling and Capacity

On startup, the manager immediately attempts to fill every empty slot. Later, it only attempts refills on `poll_interval` ticks. It never has more non-empty slots than `max_workers`.

The manager does not know whether tickets exist. It simply launches the configured command. The project command and its existing skill own ticket availability and atomic claiming. This keeps `zellij-agent` independent of any ticket-system protocol.

## Cancellation and Error Handling

- Invalid configuration fails before any tab or pane is created.
- A worker create failure leaves the slot empty, records an infrastructure error, and retries on the next polling tick.
- A marker-watch failure leaves the worker pane and slot intact. The monitoring pane shows the error; automatic recovery is deferred.
- A worker exit without the exact marker remains unresolved and is not automatically replaced.
- A worker close failure keeps the slot occupied and prevents replacement, preserving the capacity limit.
- Canceling or closing the worker manager stops new creation and marker watches but deliberately does not close workers.
- The monitoring pane remains read-only and may continue showing daemon-owned worker state after the manager stops.
- Manager restart recovery is not attempted in the MVP; starting another manager while prior workers remain is an operator error to be addressed by later recovery work.

## Testing Strategy

### Configuration and CLI tests

- `init` creates the expected directory and valid version-1 configuration.
- `init` refuses overwrite without `--force`.
- malformed YAML, unknown fields, invalid durations, empty commands, and multiline markers fail before workspace creation.
- command-line overrides take precedence over configuration.

### Manager unit tests

- initial fill never exceeds `max_workers`;
- every launch receives a unique logical pane ID;
- exact marker equality completes only the corresponding pane;
- substrings, unrelated output, and markers from other panes do not complete a worker;
- successful close makes the slot eligible on the next polling tick, not before;
- create failure retries on a later tick;
- close failure prevents slot replacement;
- watch failure and marker-less exit remain unresolved;
- cancellation preserves all worker panes.

### Runtime and transport tests

- same-tab creation resolves a logical anchor and never exposes physical IDs to the caller;
- missing or invalid anchors fail without backend pane creation;
- exact-marker watches are pane-scoped, bounded, cancelable, and unsubscribe after a match;
- single-pane close uses the logical pane ID and stops its subscription lifecycle;
- dashboard filtering excludes unrelated sessions and read-only mode exposes no mutation actions.

### Verification

- Run `gofmt` on edited Go files.
- Run focused package tests while implementing.
- Run `go test ./...` before completion.
- Build `bin/zellij-agent` and copy it to `~/.config/custom-cli` immediately after the final unified-binary rebuild, as required by the repository workflow.
- Optionally run a real-Zellij smoke flow that initializes a fixture project, starts a two-worker pool, prints the marker in one worker, observes its close, and verifies replacement on the next tick.

## Deferred Work

- Discovering and adopting surviving workers after manager restart.
- Persisted manager state and completion history.
- Failed, waiting, and stalled worker policies.
- Automatic retry limits and backoff.
- Ticket-aware labels and metrics supplied by a richer project protocol.
- Monitoring-pane controls.
- Multiple worker command profiles in one project.
