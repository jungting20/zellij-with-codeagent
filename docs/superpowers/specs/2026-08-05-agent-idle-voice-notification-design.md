# Agent Idle Voice Notification Design

## Goal

Add an opt-in voice notification to `zellij-agent agent start` so a managed
coding agent is announced whenever its daemon-owned semantic state transitions
from a non-idle state to `idle`.

The command surface is:

```bash
zellij-agent agent start codex --notify-idle -- "Implement the requested change."
```

`--notify-idle` is a wrapper option and must appear before the `--` passthrough
separator. The default is disabled, preserving current behavior.

## Architecture

The notification is a daemon-owned, best-effort side effect of the existing
`agent_state_changed` event. State detection remains responsible only for
updating the coding-agent store and publishing the event. A separate daemon
subscriber consumes state-change events and enqueues speech through the
existing daemon-owned voice service.

This follows the coding-agent dashboard design, which reserves
`agent_state_changed` subscribers for later voice, desktop, and hook
notifications. The dashboard does not participate, so notifications work even
when no dashboard is running.

The feature is background behavior and therefore does not require a new agent
role.

## Configuration and Data Model

The agent start CLI parser adds a boolean `--notify-idle` option. The value is
sent in `transport.StartAgentRequest` as `notify_on_idle` and converted into
`codingagent.StartAgentRequest.NotifyOnIdle`.

`codingagent.Record` stores `NotifyOnIdle`. The in-memory store already owns
the record for the lifetime of the claimed pane, so no separate configuration
registry or persistent storage is added. Daemon restart behavior remains
unchanged: managed coding-agent records and notification preferences are not
recovered.

The option is not forwarded to Codex, Claude, Gemini, or Cursor. Existing
commands without the flag remain wire-compatible because the JSON boolean
defaults to false.

## Event Subscriber

Daemon construction retains the event bus and coding-agent store alongside the
runtime service so the serve lifecycle can start one idle-voice subscriber.
The subscriber begins before the transport server accepts requests and stops
when the daemon serve context is canceled.

For each `agent_state_changed` event, the subscriber enqueues a notification
only when all of these conditions hold:

- `previous_state` is not `idle`;
- `state` is `idle`;
- `agent_id` resolves to a current coding-agent record;
- that record has `NotifyOnIdle` enabled.

This includes `unknown -> idle`, `working -> idle`, and `blocked -> idle`.
Changes to reason or matched rule while the state remains `idle` do not produce
another announcement. Events for deleted records, disabled records, malformed
agent IDs, and unrelated event types are ignored.

The subscriber processes events synchronously only through the queue's
non-blocking `Enqueue` operation. Native speech playback remains serialized by
the existing voice worker and never blocks state detection or the event bus
publisher.

## Speech Queue and Message

The existing `voice.Notification` type gains an optional `Message` field.
When `Message` is non-empty, the voice service normalizes and speaks it
directly. Otherwise it retains the current ticket formatting based on
`Prefix`, `TicketID`, and `Summary`, preserving ticket-worker behavior and its
HTTP contract.

An agent idle notification uses:

```text
{DisplayName} {agent ID} 작업이 완료되었습니다
```

For example:

```text
Codex agent-3 작업이 완료되었습니다
```

The display name comes from the existing coding-agent profile. The request ID
is deterministic for one state transition:

```text
agent-idle:{agent ID}:{state_changed_at Unix nanoseconds}
```

The voice queue's existing active/recent deduplication therefore suppresses a
duplicate delivery of the same event while allowing a later
`working -> idle` transition for the same agent to be announced.

Direct messages use the existing control-character removal, whitespace
collapse, and 120-Unicode-code-point speech limit. Speech arguments continue
to be passed directly to native executables without a shell.

## Failure and Shutdown Behavior

Voice notification remains best effort. Queue-full, closed-service, missing
speech backend, and playback errors do not modify agent state and do not stop
the daemon. Enqueue errors are written to the daemon error log with the agent
ID; backend playback errors continue to use the voice service log.

Daemon shutdown cancels the subscriber before closing the existing voice
service. Cancellation stops new enqueue attempts, while the voice service
retains its current behavior of canceling active speech and discarding pending
items.

## Testing

CLI and transport tests cover parsing `--notify-idle`, leaving it disabled by
default, rejecting no existing valid syntax, keeping the flag out of agent
passthrough arguments, and preserving the value through request conversion.

Coding-agent service/store tests cover retaining `NotifyOnIdle` in the created
record without changing initial `unknown` state or pane claiming.

Idle-voice subscriber tests cover:

- enabled `unknown -> idle`, `working -> idle`, and `blocked -> idle` events;
- disabled records;
- `idle -> idle` events;
- non-state-change events;
- missing or deleted agent records;
- deterministic request IDs and profile display names;
- enqueue failure logging without subscriber termination;
- context cancellation and channel closure.

Voice service tests cover direct-message formatting, normalization, Unicode
truncation, and unchanged ticket formatting. Daemon lifecycle tests verify one
subscriber is started with the runtime's event bus/store and is stopped before
the voice service closes.

Relevant package tests run first under TDD, followed by `go test ./...`, a race
test for the affected daemon/coding-agent/voice packages, and a unified binary
build.

## Scope

This change does not add global daemon configuration, desktop notifications,
custom message templates, volume/voice/language selection, persistent
preferences, notification replay after daemon restart, or dashboard-owned
notification logic. It does not alter state-detection manifests or the
stabilization rules that decide when an agent has become idle.
