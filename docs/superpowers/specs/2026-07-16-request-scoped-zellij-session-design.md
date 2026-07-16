# Request-Scoped Zellij Session Design

## Goal

Make the physical Zellij session an explicit part of every pane or tab creation request. A single daemon must be able to create and manage panes in multiple Zellij sessions without depending on the daemon process's own environment.

The existing logical execution-plan `session` remains the task identifier. The new `zellij_session` value identifies the physical Zellij session and must not change the meaning of `session` or `task_id`.

## Session Resolution

Every CLI command that creates panes or tabs resolves the physical session before contacting the daemon:

1. Use a non-empty `--zellij-session` value when supplied.
2. Otherwise, use the CLI process's `ZELLIJ_SESSION_NAME` environment variable.
3. If neither provides a non-empty value, fail before submitting the request.

Values are trimmed before validation. Resolution happens in the calling CLI process, not in the daemon. The daemon's `ZELLIJ_SESSION_NAME` is never an implicit fallback for a client request.

Raw transport clients must provide `zellij_session` themselves because the daemon cannot observe the client's environment.

## Transport and Runtime Model

Add `zellij_session` to the transport `CreatePaneRequest` and `ExecutionPlanPayload`. Add the corresponding `ZellijSession` field to runtime pane-creation and execution-plan requests.

For an execution plan, `zellij_session` applies to its tab and every pane in that plan. The value is included in dry-run output so users can verify the physical target before submission.

The runtime stores the physical Zellij session in the pane record's existing `SessionID`. Logical task grouping continues to use `TaskID`. This preserves the existing registry hierarchy while making its session level represent the actual Zellij session rather than an empty backend default.

## Backend Routing

Zellij operations become request-scoped instead of relying only on `zellij.Options.Session`. Each backend request carries the target session, and the CLI backend emits:

```text
zellij --session <zellij_session> ...
```

Pane and tab creation use the session supplied by the runtime request. Once a pane is registered, input, snapshot, close, subscribe, cleanup, and reconciliation obtain the session from that pane's registry record.

The backend's configured session may remain available for direct low-level callers and integration tests, but runtime-managed operations always pass an explicit session. It is not used to fill a missing transport request.

## Creation Flows

Unified CLI commands that submit execution plans, including work, ticket-worker, Chrome, planner, debate, and control-plane submission paths, expose or resolve `--zellij-session` consistently. Direct pane-creation callers use the same resolver at their CLI boundary.

The flow is:

```text
CLI flag or CLI environment
  -> transport zellij_session
  -> runtime ZellijSession
  -> session-scoped backend request
  -> zellij --session <name>
  -> registry PaneRecord.SessionID
```

Long-lived components that create panes after the initial execution plan must preserve the resolved session explicitly. In particular, the ticket-worker manager receives the initial physical session and includes it in every worker `CreatePaneRequest`. Watcher and role processes that submit later plans follow the same rule rather than depending on the daemon environment.

## Target Validation

`same_tab_as_pane_id` resolves the anchor record before creation. If the request's physical session differs from the anchor pane's stored `SessionID`, the runtime rejects the request as an invalid pane target. This prevents a tab ID from one session from being reused accidentally in another session.

An explicit Zellij tab ID is interpreted only within the request's `zellij_session`. Tab IDs are not treated as globally unique across Zellij sessions.

## Errors

- A CLI command fails locally when neither `--zellij-session` nor the CLI process's `ZELLIJ_SESSION_NAME` resolves to a value.
- A transport request without `zellij_session` is rejected as invalid.
- A session mismatch between a request and its same-tab anchor is rejected before invoking Zellij.
- A nonexistent or unavailable Zellij session returns the underlying Zellij command failure through the existing runtime and transport error path.
- Dry-run performs the same session resolution and validation as a real submission.

## Reconciliation and Cleanup

Reconciliation groups active managed pane records by physical `SessionID` and lists live panes separately for each Zellij session. Results from one session are never used to mark panes in another session. A failure to inspect one session is reported without silently treating that session's panes as missing.

Cleanup, close, snapshot, input, and subscription operations route through the session stored on each pane record. Multi-pane operations group or iterate records by session as needed, so one daemon can manage multiple physical sessions safely.

## Compatibility

The logical execution-plan `session` and pane `task_id` retain their existing behavior. `zellij_session` is a separate field with a distinct name in JSON and CLI flags.

This is an intentional validation change for raw API callers: requests that previously omitted the physical session must now provide `zellij_session`. Normal CLI callers remain convenient because they inherit the invoking pane's `ZELLIJ_SESSION_NAME` automatically.

## Testing

Automated tests cover:

- `--zellij-session` taking precedence over `ZELLIJ_SESSION_NAME`.
- CLI environment fallback when the flag is absent.
- local failure when both sources are empty.
- transport and runtime validation of missing sessions.
- pane and tab commands containing the correct `--session` argument.
- two physical sessions managed concurrently by one runtime.
- follow-up input, snapshot, close, subscription, cleanup, and reconciliation routing to the pane record's session.
- same-tab anchor session mismatch rejection.
- ticket-worker manager, monitor, and worker panes retaining one physical session.
- dry-run output containing the resolved `zellij_session`.
- unchanged logical `session` and `task_id` behavior.

The normal verification command is `go test ./...`. Real-Zellij integration coverage remains opt-in and should exercise an explicitly named session through the existing integration-test environment flags.
