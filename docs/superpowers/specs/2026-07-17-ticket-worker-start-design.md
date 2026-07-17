# Ticket Worker Start Design

## Goal

Change `zellij-agent ticket-worker start` from a manual single-ticket status
transition into the launch command for the existing `ticket-manager` role. The
command creates one runtime-managed manager anchor pane, and that manager owns
claiming queued tickets and creating coding-agent panes.

The former `ticket-worker start ID [--json]` interface is removed. Individual
tickets become `in_progress` only through `ticket-worker next` or the manager's
use of the same atomic FIFO claim operation.

## Command Contract

The public command is:

```text
zellij-agent ticket-worker start [--socket PATH] [--timeout DURATION] [--zellij-session NAME]
```

- `--socket` defaults to the standard daemon socket.
- `--timeout` defaults to 15 seconds and must be positive. It bounds submission
  of the launch request, not the manager lifetime.
- `--zellij-session` selects the physical Zellij session. When omitted, the
  command uses `ZELLIJ_SESSION_NAME`; absence of both is an error.
- Positional arguments, including the former ticket ID, are usage errors.

`ticket-worker --help` describes `start` as starting the ticket manager. The
remaining queue commands keep their current contracts: `init`, `add`, `list`,
`next`, `show`, `done`, `cancel`, and `reopen`.

## Preconditions

The command discovers the enclosing Git root from its current working
directory. Before contacting the daemon it opens the existing ticket database
and loads the existing worker configuration. This validates that
`ticket-worker init` has completed and that both the SQLite schema and
`.zellij-agent/worker/config.yaml` are usable. Validation must not create or
modify project state.

## Launch Architecture

Add a pure ticket-worker plan builder under `internal/ticketworker`. The CLI
parses and validates user input, asks the builder for a
`transport.ExecutionPlanPayload`, validates the resulting request envelope,
and submits it through an injected transport client. Pane creation remains
behind the daemon's `RuntimeService`; neither the CLI nor the plan builder
calls Zellij directly.

The execution plan contains one new tab and one pane:

- tab name: `ticket-worker`;
- pane role: `ticket-manager`;
- pane working directory: the canonical project root;
- pane command: the current `zellij-agent` executable followed by `role
  ticket-manager`, its socket, logical task ID, anchor pane ID, physical Zellij
  session, and project root.

The manager command receives the same socket and Zellij session used for the
launch request. Its `--task` value matches the execution-plan session, and its
`--anchor-pane` value matches its own logical pane ID. This lets the role find
its registered anchor before it starts claiming tickets.

## Identity and Duplicate Starts

Runtime pane IDs are daemon-global, so fixed literal IDs would make unrelated
projects collide. Derive a stable short identity from the canonical project
root and use it in the logical execution session, request ID, and anchor pane
ID. Human-facing tab and role names remain `ticket-worker` and
`ticket-manager`.

The identity is stable for the same project rather than time-based. Submitting
the exact same start request while its manager is active therefore uses the
runtime's existing idempotent create behavior and returns the existing pane;
it does not start a second manager. A different request for the same logical
pane, including an attempt to move an active manager to another physical
Zellij session, fails instead of allowing concurrent managers for one project.
After the old pane reaches a terminal state, the existing runtime lifecycle
allows the same stable identity to create a replacement manager.

This enforces the manager's current operational contract of at most one active
manager per project within a daemon. Durable manager adoption across daemon
restarts remains outside this change; existing `in_progress` tickets are not
automatically adopted.

## Client and Output Behavior

`cmd/zellij-agent` injects its current executable path into the plan builder and
uses the existing auto-start transport client factory. A missing daemon may
therefore be started by the unified CLI in the same way as other launch
commands.

On success the command prints a concise execution-plan response containing the
request, logical session, tab, and manager pane status. Failures identify the
stage: project discovery, database/config validation, Zellij-session
resolution, plan validation, or daemon submission. The manager continues in
its pane after the CLI submission context ends.

## Runtime Flow After Launch

The new command does not duplicate manager behavior. Once registered, the
existing `ticket-manager` role:

1. verifies its anchor and connects to the daemon event stream;
2. atomically claims FIFO `ready` tickets up to `max_workers`;
3. creates coding-agent panes in the manager's tab and sends rendered prompts;
4. marks tickets `done` only after their exact completion marker is observed;
5. closes completed panes, refills capacity, and polls for newly added tickets;
6. closes active workers and safely requeues unfinished tickets on shutdown.

## Error Handling

- Missing initialization or invalid configuration stops before pane creation.
- A missing physical Zellij session is a usage/environment error.
- A non-positive timeout is a usage error.
- Generated-plan validation failure is reported as an internal launch error and
  no request is submitted.
- Transport errors preserve the existing client diagnostic, including daemon
  startup failure when automatic startup cannot succeed.
- Output writer failures return a command failure rather than reporting a
  successful launch.

No ticket is manually transitioned or claimed by `ticket-worker start`, so a
failed launch cannot mutate ticket status.

## Testing

Pure plan-builder tests cover stable per-project identities, separation between
different roots, exact task/anchor alignment, executable and socket forwarding,
the physical Zellij session, the single-tab/single-pane shape, and invalid
inputs.

CLI tests use a fake submission client and cover:

- successful precondition validation and exact submitted payload;
- Zellij-session flag and environment resolution;
- default and custom socket forwarding;
- non-positive timeout and positional-argument rejection;
- missing database and invalid config without submission;
- submission and output-writer failures;
- help text and removal of the former `start ID` transition behavior;
- unchanged behavior of all other ticket queue commands.

Unified-binary tests verify dispatch, injected executable use, and help output.
The full `go test ./...` suite remains the required regression check. A real
Zellij smoke run may additionally verify that the manager becomes the anchor of
a new `ticket-worker` tab and creates coding-agent panes beside itself.

## Out of Scope

This change does not add stop/status commands, a dashboard pane, manager state
persistence, adoption of pre-existing `in_progress` tickets, multiple managers
for one project, ticket search, or changes to the existing manager scheduling
and completion algorithms.
