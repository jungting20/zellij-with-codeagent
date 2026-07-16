# Ticket Worker Ticket Completion Design

## Summary

Extend the project-configured ticket-worker pool so a coding-agent worker reports
the completed ticket ID in a structured completion line. After validating that
line, the deterministic worker manager runs the project's configured ticket
completion command and closes the worker pane only when the command succeeds.

The completion command runs outside the coding-agent pane, from the project root,
without a shell. A failed command leaves the pane and its capacity slot occupied
and is not retried automatically. After an operator completes the ticket manually
and closes the pane, the manager discovers the missing active pane during polling
and releases the slot.

## Goals

- Complete a ticket after a coding agent reports successful implementation and
  verification, but before the manager closes its worker pane.
- Carry the ticket ID in a structured, pane-scoped completion line.
- Keep ticket-system details project-configurable rather than embedding a
  particular ticket CLI in `zellij-agent`.
- Preserve existing exact-marker behavior for configurations without a completion
  command.
- Preserve failed completion panes for diagnosis and manual recovery.
- Update `ticket-worker init` and `init --force` to generate the new configuration
  fields.

## Non-goals

- Claiming tickets in the manager.
- Teaching the manager how to query ticket status.
- Automatically retrying a failed ticket completion command.
- Adding interactive recovery controls to the read-only monitoring pane.
- Inferring successful work from process exit or pane closure.
- Supporting arbitrary shell fragments or ticket-ID interpolation in commands.

## Project Configuration

The version-1 schema gains two worker fields:

```yaml
version: 1
max_workers: 3
poll_interval: 30s

worker:
  command: ["coding-agent", "..."]
  completion_marker: "ZELLIJ_AGENT_WORKER_DONE"
  complete_command: ["ticket", "complete"]
  complete_timeout: 30s
```

`complete_command` is an optional, non-empty argument vector. When it is absent,
the manager retains the current behavior: it waits for an exact standalone
`completion_marker` line and then closes the pane.

When `complete_command` is present, `complete_timeout` defaults to 30 seconds and
must be positive. Each command element must contain non-whitespace content. The
manager appends the parsed ticket ID as the final argument, runs the command from
the configured project root, and does not invoke a shell.

The generated `ticket-worker init` template includes active `complete_command`
and `complete_timeout` example fields. Users replace both the coding-agent command
and ticket CLI example with project-specific values. `init --force` writes the
same current template. Configuration remains version 1 because the fields are
additive and configurations that omit `complete_command` remain valid.

## Coding-Agent Output Contract

The project-provided coding-agent prompt must instruct the agent to print the
following standalone line only after implementation and verification succeed:

```text
ZELLIJ_AGENT_WORKER_DONE ticket_id=TICKET-123
```

The general form is:

```text
<completion_marker> ticket_id=<ticket-id>
```

The line must be the final standalone line of the successful response. The manager
accepts ticket IDs matching:

```text
[A-Za-z0-9][A-Za-z0-9._:-]*
```

This keeps the ID a single unambiguous argument and prevents whitespace or control
characters from entering command arguments and logs. A line with the configured
prefix but a missing or malformed ticket ID is a completion protocol failure; it
must not run the completion command or close the pane.

Because `worker.command` is an opaque project-owned argument vector, the runtime
does not modify agent-specific prompt arguments. The project config or the command
it invokes owns the prompt text and must include this output contract.

## Marker Watch Extension

The existing marker watch supports exact standalone-line matching. It gains a
prefix mode that returns the actual matched output line to the caller. Exact mode
and its existing response contract remain compatible for other consumers.

For ticket workers with `complete_command`, the manager watches for the configured
completion marker followed by a space. The watch remains scoped to the logical
pane ID. It returns the complete matched line so the manager can validate the
suffix and parse the ticket ID. Matching is bounded and event-driven; the manager
does not poll or parse pane snapshots.

Malformed structured lines are returned to the ticket-worker manager as protocol
failures rather than being treated as successful exact markers. Output from a
different pane never completes the current worker.

## Manager State and Data Flow

The manager extends each slot with completion state and parsed ticket information:

```text
occupied -> completing -> empty
                    \
                     -> completion_failed
```

The successful flow is:

1. A pane-scoped watcher receives a structured completion line.
2. The manager verifies the pane ID, marker prefix, suffix shape, and ticket ID.
3. The slot moves from `occupied` to `completing`.
4. A bounded command runner executes `complete_command + ticketID` from the
   project root.
5. A successful zero exit status allows the manager to request `ClosePane` through
   the existing local transport.
6. A successful close, or existing close-race reconciliation that proves the pane
   is absent, marks the slot complete and empty.
7. Normal polling may fill the empty slot with a new worker.

The command runner must not block the manager's single-owner event loop. It sends a
typed result back to the loop, and only that loop mutates slot state. The runner
enforces `complete_timeout`, terminates the child process on timeout or manager
cancellation, and retains bounded stdout/stderr for manager diagnostics.

All pane inspection, creation, and closure continues through `RuntimeService` and
the local transport. The manager never invokes Zellij directly.

## Failure and Manual Recovery

A malformed completion line, command start failure, timeout, non-zero exit, or
other completion-command failure moves the slot to `completion_failed`. The
manager records a bounded diagnostic, leaves the pane and slot occupied, does not
call `ClosePane`, and does not run the command again on later polling ticks.

An operator recovers the slot by completing the ticket manually and closing the
worker pane. On every normal polling tick, the manager reconciles occupied and
completion-failed slots against runtime inspection. A slot's pane is active only
when the inspection contains the exact logical pane ID, task ID, physical Zellij
session, and a `starting` or `running` status. If no such active pane exists, the
manager releases the slot. It may refill that slot according to the manager's
normal polling behavior.

The manager does not verify that the operator actually changed ticket state. Once
an operator manually closes a failed pane, releasing its slot is an explicit trust
boundary. Records from another task or physical session do not satisfy or release
the slot.

If a coding-agent process exits and Zellij closes its pane before the manager can
complete the ticket, the manager cannot preserve a pane that no longer exists.
The existing close-race reconciliation remains applicable after a successful
completion command. A failure followed by an already absent pane is retained as a
diagnostic until normal polling observes the absence and releases capacity.

Canceling the manager cancels an in-flight completion command and preserves its
worker panes, matching existing manager cancellation behavior.

## Observability

The manager pane logs:

- the slot, pane, and parsed ticket ID when completion starts;
- successful command completion before pane closure;
- timeout, exit, and bounded output diagnostics on failure;
- manual-close reconciliation and slot release.

Ticket IDs and command output are treated as untrusted log content and must not
introduce control characters. The existing read-only monitor remains unchanged;
operators can inspect the manager pane output and runtime state without adding a
new manager-state API.

## Testing Strategy

### Configuration and initialization

- Existing version-1 configurations without `complete_command` still load and use
  exact-marker behavior.
- `complete_command` rejects empty arrays and blank elements.
- `complete_timeout` defaults to 30 seconds and rejects non-positive or malformed
  durations.
- Unknown fields remain rejected.
- `init` and `init --force` write the complete updated template, and the generated
  template loads successfully.

### Marker watch and transport

- Exact standalone-line matching remains unchanged.
- Prefix mode ignores unrelated panes and lines.
- Prefix mode returns the complete matched line.
- Cancellation, malformed requests, and bounded buffering preserve existing
  behavior.

### Manager behavior

- A valid structured line appends the ticket ID to the configured argv.
- The completion command runs from the project root without a shell.
- `ClosePane` is called only after a successful zero exit status.
- Missing or malformed ticket IDs do not run the command or close the pane.
- Start failures, non-zero exits, timeouts, and cancellation retain the pane and
  occupied slot.
- Polling does not retry a failed completion command.
- Manual pane closure is detected using pane ID, task, session, and active status;
  it releases the slot and allows refill.
- Existing close-race reconciliation still completes a slot after the ticket
  command succeeds.
- Legacy exact-marker configurations keep their current close and refill behavior.

### Final verification

Run:

```bash
go test ./...
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
cmp -s bin/zellij-agent ~/.config/custom-cli
```

The focused ticket-worker and transport tests must pass before the full suite. The
unified binary must be rebuilt and immediately registered after implementation, as
required by the repository guidelines.
