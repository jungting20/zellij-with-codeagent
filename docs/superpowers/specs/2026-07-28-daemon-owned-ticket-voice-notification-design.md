# Daemon-Owned Ticket Voice Notification Design

## Goal

Move the in-memory speech queue and native speech backend from each ticket-manager process into the daemon. A coding-agent completion also supplies a one-line summary of the actual changes, producing announcements such as:

```text
ticket-manager 42 완료. 로그인 재시도 로직과 오류 메시지를 개선했습니다
```

The daemon serializes announcements from every manager so concurrent completions cannot start overlapping speech processes. The queue remains intentionally memory-only: daemon shutdown cancels current speech and discards queued announcements.

This design supersedes the queue ownership, message construction, and manager lifecycle portions of `2026-07-28-ticket-manager-voice-notification-design.md`. The existing project configuration defaults and native OS backend choices remain in effect.

## Responsibilities

### Coding agent

The coding agent describes the work it actually completed. Its prompt requires these final two lines, in this order:

```text
ZELLIJ_AGENT_TICKET_SUMMARY 로그인 재시도 로직과 오류 메시지를 개선했습니다
ZELLIJ_AGENT_TICKET_DONE 42
```

The summary must be a single concise line. The existing completion marker is unchanged so older agents that emit only the marker remain compatible.

### Ticket manager

The ticket manager detects completion, extracts the optional summary, persists the `done` transition, closes the worker pane, and submits a structured voice-notification request to the daemon. It continues to read the repository-local `voice_notifications` and `voice_notification_prefix` settings. It no longer creates, owns, or closes a native speech backend or speech queue.

Voice notification is a best-effort side effect. Missing summaries and notification failures never prevent ticket completion or pane cleanup.

### Daemon

The daemon owns one bounded FIFO queue, one speech worker, recent request-ID deduplication, final message formatting, and native backend selection. All managers using the daemon share this one serialization point.

The cross-platform backend code currently coupled to ticket-worker moves to a package with no ticket-manager dependency. Backend selection remains:

- macOS: `say`
- Linux: `spd-say --wait`, falling back to `espeak`
- Windows: Windows PowerShell or PowerShell Core with `System.Speech.Synthesis.SpeechSynthesizer`

## Completion Summary Protocol

The summary prefix is exactly `ZELLIJ_AGENT_TICKET_SUMMARY ` and the completion marker remains exactly `ZELLIJ_AGENT_TICKET_DONE <positive ticket ID>`.

When an output block contains the exact completion marker, the manager selects the closest preceding summary line associated with that marker. Leading and trailing whitespace and the display bullet prefix already tolerated by completion-marker parsing are ignored. If the event block contains the marker but no usable summary, the manager requests a pane snapshot and parses it once before falling back to no summary. Snapshot recovery uses the same parser.

The parser treats a missing prefix, an empty value, an invalid marker, or a snapshot failure as an absent summary. It does not reject completion. If multiple summary lines precede the marker, only the closest one is used.

The extracted text is transient and is not added to the ticket database. The daemon removes control characters, trims the value, collapses whitespace, and limits the spoken summary to 120 Unicode code points. Truncation applies only to speech formatting.

## HTTP Contract

The local Unix-socket transport adds this endpoint:

```http
POST /v1/voice-notifications
Content-Type: application/json
```

Request body:

```json
{
  "request_id": "ticket-worker-a40161df:42:1785231234567890000",
  "prefix": "ticket-manager",
  "ticket_id": 42,
  "summary": "로그인 재시도 로직과 오류 메시지를 개선했습니다"
}
```

`request_id` is `<task ID>:<ticket ID>:<completed_at Unix nanoseconds>`. The manager obtains `completed_at` from the successful database transition, or from the current `done` record when recovering an already-applied transition. This makes retries for one completion idempotent while allowing a reopened ticket to produce a new announcement. A `done` record without `completed_at` is an invariant violation: the manager logs it, skips notification, and still clears the completed slot.

The transport client exposes a dedicated queue method rather than reusing pane messaging. The handler reads at most 8 KiB. It accepts a request ID of at most 256 bytes, a trimmed prefix of at most 128 Unicode code points, a positive ticket ID, and a single-line summary of at most 4 KiB before normalization. Empty request IDs and prefixes are invalid. It does not accept a caller-provided shell command.

Responses are:

- `202 Accepted` with `{"status":"queued"}` after a new item is enqueued.
- `200 OK` with `{"status":"duplicate"}` when the request ID was already accepted during this daemon lifetime.
- `400 Bad Request` for invalid input.
- `503 Service Unavailable` with a stable `queue_full` error code when the queue cannot accept another item.

The endpoint returns after enqueueing and never waits for speech playback.

## Queue and Formatting

The daemon queue holds at most 256 pending items. A mutex defines acceptance order for concurrent requests; the single worker removes and speaks items in that FIFO order. A request ID enters the deduplication set only after its item is accepted, so a queue-full response may be retried.

Queued, in-flight, and recently completed request IDs count as duplicates. The daemon retains at most 1,024 accepted IDs in insertion order. Eviction only bounds memory; it does not provide persistence or deduplication across daemon restarts.

The daemon constructs the base message as:

```text
{trimmed prefix} {ticket ID} 완료
```

When the normalized summary is non-empty, it appends a sentence pause and the summary:

```text
{trimmed prefix} {ticket ID} 완료. {normalized summary}
```

Speech arguments continue to be passed directly to native executables without a shell.

## Completion and Retry Flow

The manager performs operations in this order:

1. Detect the exact completion marker and extract or recover the optional summary.
2. Transition the ticket to `done` and retain the returned `completed_at`.
3. Close the worker pane, retrying the existing close flow when necessary.
4. If voice notifications are enabled, submit the structured request.
5. Clear the manager slot regardless of the final notification outcome.

The manager treats `queued` and `duplicate` as success. Each submission has a one-second timeout. It retries an ambiguous transport failure, `queue_full`, or a server-side failure with the same request ID, for at most three total attempts, waiting 100 milliseconds before the second attempt and 200 milliseconds before the third. It does not retry a validation error. Exhausted retries are logged with ticket context but do not alter `done` state or reopen the pane.

Keeping notification after successful pane closure preserves the existing behavior: close retries never enqueue early, and a completed worker is not announced while its pane remains active.

## Shutdown and Failure Behavior

Daemon shutdown stops accepting new notification requests, cancels the active native speech process, discards queued items, closes the speech worker, and then completes shutdown. Close is idempotent. Expected process cancellation is not logged as a backend failure.

A missing native executable or a native speech failure is logged by the daemon. It does not stop the queue worker; subsequent items continue. Because delivery is memory-only and best effort, a daemon crash can lose accepted items, and a manager crash after pane closure but before request submission can lose an announcement. Durable or exactly-once delivery is outside this design.

Disabling `voice_notifications` prevents the manager from making an HTTP request. An empty configured prefix remains invalid when notifications are enabled, following the current configuration behavior.

## Testing

### Completion protocol

- Prompt rendering includes the exact summary instruction followed by the unchanged completion marker instruction.
- Parsing covers a normal summary, a missing summary, an empty summary, multiple summaries, display bullet prefixes, event chunks without the summary, snapshot fallback, and snapshot recovery.
- A malformed or absent summary never blocks completion.

### Manager integration

- No request occurs before both the `done` transition and successful pane closure.
- The request contains prefix, ticket ID, parsed summary, and a stable ID derived from `completed_at`.
- Disabled notifications make no request.
- `queued` and `duplicate` clear the slot.
- Transient failures reuse the same request ID and stop after the bounded retry count.
- Validation and exhausted transport failures are logged without changing the completed ticket or closed pane outcome.
- An already-completed transition recovery uses the stored completion timestamp.

### Transport and daemon queue

- Handler and client tests cover valid requests, every validation boundary, queued, duplicate, queue-full, and shutdown behavior.
- Concurrent submissions verify FIFO acceptance and that at most one native speech call runs at a time.
- Deduplication covers queued, in-flight, and recently completed IDs, including bounded eviction.
- Formatting covers no-summary fallback, control-character removal, whitespace collapse, punctuation, and the 120-code-point Unicode limit.
- Shutdown tests verify active-process cancellation, pending-queue discard, worker exit, and idempotent close.

### Native backends and verification

Existing macOS, Linux, and Windows backend-selection and argument-safety tests move with the backend package. Relevant packages run under the race detector, followed by `go test ./...`.

## Scope

This change does not persist notification items or summaries, recover a queue after daemon restart, announce manual `ticket-worker done` transitions, add volume/voice/language settings, or allow arbitrary speech commands. It does not solve discovery of orphaned ticket-manager panes across daemon restarts; that is a separate lifecycle concern.
