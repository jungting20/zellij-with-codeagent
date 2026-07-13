# Runtime Supervisor Dashboard Design

## Goal

Add a local-only `zellij-agent dashboard` Bubble Tea TUI that lets a user understand, inspect, and intervene in the daemon-owned runtime without switching among separate `ctl status`, `ctl events`, `ctl snapshot`, and `ctl cleanup` commands.

The first release covers the full P1 scope from `docs/next-steps-todolist.md`: runtime hierarchy, pane output, recent semantic events, live refresh, snapshot, input, reconcile, task cleanup, model tests, and a real-Zellij manual smoke flow. It does not add LLM behavior, persistence, or direct Zellij calls.

## Architecture

The dashboard remains a transport client. It must not import or invoke `internal/zellij`; every read and mutation goes through the existing Unix-socket transport APIs.

Two focused packages divide command lifecycle from TUI behavior:

- `internal/cli/dashboard` parses command flags, constructs the transport client, owns signal-aware context cancellation, starts Bubble Tea, and maps startup or terminal errors to process exit codes.
- `internal/dashboard` owns the Bubble Tea model, hierarchy projection, asynchronous commands, update logic, and rendering. It depends on a narrow client interface implemented by `transport.Client` so tests can use deterministic fakes.

The existing `internal/supervisor.BuildView` helper is a runtime-service-level read model used by in-process callers. The dashboard operates across the transport boundary and therefore does not adapt or bypass that helper. Shared behavior should be extracted only if implementation reveals genuinely identical transport-independent logic.

## Command Surface

The unified binary gains this command:

```text
zellij-agent dashboard [--socket PATH] [--timeout DURATION] [--refresh-interval DURATION] [--event-limit N]
```

Defaults:

- `--socket`: the existing `internal/cli.DefaultSocketPath` value.
- `--timeout`: `10s`, matching the existing unified CLI command groups.
- `--refresh-interval`: `2s`.
- `--event-limit`: `100`.

The command uses the same auto-starting transport-client construction as the other unified command groups. A non-positive refresh interval or event limit is rejected as a usage error. The command is local-only and exposes no listener or remote address option.

## Runtime Client Boundary

The dashboard client interface provides exactly the operations required by P1:

```go
type Client interface {
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
	Reconcile(context.Context) (transport.ReconcileResponse, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}
```

No new daemon endpoint is needed. `Reconcile` is added to any narrower existing CLI-facing interface that does not currently expose it, without changing the wire protocol.

## Hierarchy Projection

`InspectRuntimeResponse.Panes` is projected into a stable four-level tree:

```text
session -> task -> tab -> pane
```

Grouping keys use `Pane.SessionID`, `Pane.TaskID`, and `Pane.TabID`. Display labels prefer `Pane.TabName` for tabs while retaining the tab ID to disambiguate identical names. Empty grouping identifiers are placed under an explicit `ungrouped` node at their level. Nodes are sorted by display label and then stable identifier; panes are sorted by logical pane ID. The same response always produces the same tree regardless of input slice ordering.

All groups start expanded on initial load. Expansion state and the selected logical node ID survive refreshes when the node still exists. If the selected node disappears, selection moves to the nearest surviving visible row, then to the first row. Empty runtimes render an explanatory state instead of a synthetic pane.

Pane rows show role and a lifecycle badge for `starting`, `running`, `exited`, `closed`, `lost`, or `error`. Unknown status strings remain visible with a neutral style rather than being discarded.

## Layout and Rendering

The normal layout has a left hierarchy pane and a right detail area. The right side is split vertically:

```text
+----------------------+-----------------------------------+
| session/task/tab/    | selected pane output              |
| pane tree            |                                   |
|                      +-----------------------------------+
|                      | recent semantic events            |
+----------------------+-----------------------------------+
| connection, action result, error, and key hints          |
+----------------------------------------------------------+
```

The hierarchy is navigable at every terminal size. When the terminal is too narrow for two columns, the view stacks hierarchy, output, and events vertically. Content is clipped to the available width and height with ANSI-aware helpers already used by `tab-network`; rendering must not panic for zero or very small dimensions.

The output area displays the latest non-ANSI snapshot for the selected pane. It is refreshed when pane selection changes, when the user presses `s`, and after a successful mutating action. Until a snapshot completes, the last successful output remains visible with a loading indicator. Snapshot failures preserve that output and surface an error in the status line.

The event area shows the newest configured number of semantic events in chronological display order. `raw_output` events remain refresh signals but are excluded from this event list because the selected pane output area already represents them. Events matching the selected pane are emphasized; events without a pane or belonging to other panes remain visible so runtime-wide health and lifecycle changes are not hidden.

Lip Gloss styles follow the restrained patterns already used by `tab-network`: a bold title, muted metadata and hints, distinct lifecycle colors, reverse-video selection, and a red error state. Color is decorative; text labels carry all status meaning.

## Keyboard Interaction

Normal mode supports:

- `j`, `k`, down, up: move through visible hierarchy rows.
- `enter`: expand or collapse the selected group; it has no effect on a pane row.
- `s`: refresh the selected pane snapshot.
- `i`: enter single-line input mode for the selected active pane.
- `r`: reconcile managed state with Zellij through the transport client.
- `x`: request cleanup of the selected pane's task.
- `R`: manually refresh runtime status and recent events.
- `q`: quit.

Input mode displays the selected pane and an editable single-line buffer. Printable runes append, backspace removes the last rune, `Esc` cancels, and `Enter` sends the buffer followed by `\n` through `SendInput`. Empty input is not sent. Input is available only for `starting` and `running` panes; otherwise the status line explains why it is disabled.

Cleanup is available only when the selected pane has a non-empty task ID. Pressing `x` enters confirmation mode and displays the task ID and current number of panes in that task. Only `y` submits `CleanupRequest{TaskID: selected.TaskID}`; `n` and `Esc` cancel. This prevents a missing task identifier from becoming an accidental broad cleanup request.

While an action is in flight, duplicate submissions of that action are ignored and the status line shows progress. Successful input, reconcile, and cleanup actions trigger an immediate refresh. Action responses are summarized in the status line, including reconciled active/lost counts and cleanup closed/failed/skipped counts.

## Refresh and Event Flow

Initialization starts three independent Bubble Tea commands:

1. Fetch runtime status and recent events.
2. Connect to the event stream.
3. Schedule the next two-second refresh tick.

An event-stream message is a refresh signal, not an authoritative replacement for the read model. The dashboard reads current state through `InspectRuntime`, `RecentEvents`, and `SnapshotOutput`, keeping display logic consistent with the existing transport boundary.

Only one status/events refresh may be in flight. If a timer, manual request, or stream event arrives during a refresh, the model sets a dirty flag. Completion launches exactly one follow-up refresh when dirty, coalescing bursts without blocking daemon event publishers or creating unbounded requests.

The recurring timer is rescheduled after every tick regardless of refresh state. Event-stream consumption waits for one event at a time in a Bubble Tea command and immediately installs the next wait command after delivery.

If the event stream fails or closes, the model marks the connection `degraded`, reports the reason, and continues periodic polling and all keyboard actions. Automatic stream reconnection and backoff remain part of the later subscription-recovery work explicitly listed in `docs/next-steps-todolist.md`.

## Error Handling

Startup can render before the daemon returns data. A failed initial read leaves the TUI running with an empty-state explanation and retries on the next timer or manual refresh.

Read failures preserve the most recent successful hierarchy, events, selection, expansion state, and output. The status line identifies the failed operation. A later success clears the corresponding degraded read error while preserving the latest action result long enough to be useful.

Action failures do not mutate the local hierarchy optimistically. The dashboard shows the error and waits for a successful refresh to change displayed runtime state. A cleanup response with failed panes is treated as a partial failure in the status summary while still refreshing to show successfully closed panes.

Context cancellation closes the event stream and allows outstanding commands to return. Quitting the dashboard never cleans up panes implicitly.

## Testing Strategy

Tests use Bubble Tea messages and a fake client; they do not require a running daemon or Zellij.

Model and projection tests cover:

- Stable `session -> task -> tab -> pane` ordering and `ungrouped` identifiers.
- Lifecycle badges, unknown statuses, and empty runtime rendering.
- Selection and expansion preservation across refreshes and safe fallback after deletion.
- Narrow, short, and zero-sized terminal rendering without panic.
- Selection-driven and manual snapshot requests.
- Input editing, newline submission, inactive-pane rejection, and action failure display.
- Reconcile execution and success/error summaries.
- Task cleanup confirmation, cancellation, exact task-scoped request, and missing-task rejection.
- Refresh coalescing when multiple event/timer messages arrive during an in-flight request.
- Stream close/error transition to `degraded` while timer refresh remains active.

CLI tests cover option defaults and validation, transport-client construction, Bubble Tea runner errors, and top-level `zellij-agent dashboard` dispatch/help output. The existing full unit suite remains the regression gate.

The manual smoke document adds a real-Zellij flow that starts a mixed workspace, launches the dashboard, verifies hierarchy and live output, sends harmless input, reconciles, and cleans up a test task after confirmation. Verification commands are:

```bash
go test ./internal/dashboard ./internal/cli/dashboard ./cmd/zellij-agent
go test ./...
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

The binary copy immediately follows a successful rebuild, as required by the repository guidelines.

## Scope Boundaries

This release intentionally excludes:

- Direct calls from the dashboard to the Zellij CLI or backend.
- Remote dashboard access or a network listener.
- Persistent dashboard preferences, event history, or daemon state.
- LLM-generated operations or autonomous remediation.
- Event cursors, replay, automatic subscription recovery, or exponential backoff.
- Pane creation, arbitrary role cleanup, or cleanup of ungrouped panes.

These boundaries keep P1 focused on supervising existing daemon-owned resources with the transport capabilities already present.
