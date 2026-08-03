# Idle Agent Next Filter Design

## Goal

Change `zellij-agent agent next` so it cycles only through managed coding
agents whose current daemon-owned state is `idle`. If no idle agent exists,
the command succeeds without focusing a pane, advancing the cursor, or writing
output.

## Scope

- Reuse the existing `agent-next` default role and `/v1/agents/next` endpoint.
- Keep the current daemon-wide, in-memory focus cursor and creation-order
  wraparound behavior.
- Do not change direct agent focus: an explicitly selected agent may still be
  focused regardless of its state.
- Treat `working`, `blocked`, and `unknown` as ineligible for next navigation.

## Service Behavior

`codingagent.Service.FocusNextAgent` remains the authority for selection and
focus. While holding the existing focus mutex, it lists records in creation
order, filters them to `StateIdle`, and selects the next record relative to the
daemon-wide cursor.

- If the cursor identifies an eligible idle record, select the following idle
  record and wrap at the end.
- If the cursor is empty, deleted, or identifies a non-idle record, select the
  first idle record.
- If exactly one idle record exists, repeated calls select that record.
- Advance the cursor only after runtime focus succeeds and returns a
  non-terminal pane.
- If runtime focus fails, preserve the cursor and existing error behavior.
- If no idle record exists, return a successful no-op response without calling
  runtime focus or changing the cursor.

The stored state is the daemon's current source of truth. This feature does not
perform an additional screen detection pass during navigation.

## API Contract

Add a `Focused bool` field to the domain and transport
`FocusNextAgentResponse` types. The JSON response gains an additive `focused`
boolean:

```json
{
  "focused": false,
  "agent": {}
}
```

`focused: true` means `agent` contains the successfully focused agent and pane.
`focused: false` means no idle agent existed and the `agent` value must be
ignored. The endpoint continues to return HTTP 200 for the no-op case. Existing
error mapping remains unchanged for store, runtime, validation, and transport
failures.

## CLI Behavior

The CLI continues to validate its Zellij source context before calling the
daemon. After a successful response:

- `focused: true`: print the focused logical pane identifier using the existing
  output behavior.
- `focused: false`: print nothing to stdout or stderr and exit 0.

The `agent-next` role requires no command-line changes because it delegates to
the unified CLI and propagates its exit status.

## Concurrency

Filtering, selection, runtime focus, and cursor advancement remain serialized
by the service's existing daemon-wide focus mutex. This prevents concurrent
next requests from selecting from the same cursor position. Store state may be
updated independently by the monitor, but each request uses the ordered record
snapshot returned while executing its serialized selection path.

## Testing

Service tests cover:

- mixed idle/working/blocked/unknown records select only idle records;
- creation-order navigation and wraparound among idle records;
- a cursor pointing to a newly non-idle or deleted record restarts at the first
  idle record;
- zero idle records produce a successful no-op with no runtime focus and no
  cursor movement;
- one idle record remains selectable;
- focus failure preserves the cursor;
- concurrent requests remain serialized.

Transport tests cover `focused: true` and `focused: false` response conversion
and HTTP 200 behavior. CLI tests cover silent exit 0 for `focused: false`, the
existing output for `focused: true`, and exact RPC call counts for validation,
success, no-op, and error paths.

## Documentation and Rollout

Update the README and manual smoke procedure to state that global agent-next
navigation visits only idle agents and silently does nothing when none are
idle. Rebuild the unified binary and install it atomically on the custom CLI
path. The existing Zellij `Alt+o` binding requires no change because it already
invokes `zellij-agent agent next`.
