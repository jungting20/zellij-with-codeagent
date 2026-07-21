# Ticket Manager Ticket Titles Design

## Goal

Include the ticket title in every existing ticket-specific manager log and in
each worker's Zellij pane name so work is identifiable by both numeric ID and
human-readable title.

## Log Format

Ticket-specific log lines add `title=%q` immediately after `ticket=<id>`. Go's
quoted-string formatting keeps each event on one line and escapes quotes,
backslashes, tabs, carriage returns, and newlines in a title. Unicode text
remains readable.

Examples:

```text
started ticket=3 title="검색 기능 구현" pane=ticket-coding-manager-3
closed ticket=3 title="검색 기능 구현" pane=ticket-coding-manager-3
complete ticket=3 title="검색 기능 구현" failed: database unavailable
```

## Scope

Add the title to the existing logs for:

- prompt rendering failure
- worker pane creation failure
- worker start
- worker inspection failure
- ticket completion failure
- worker pane close failure
- worker pane close success
- snapshot recovery failure

Claim failures do not have a ticket value and therefore cannot include a
title. Event-stream logs are not associated with a ticket and remain
unchanged. This change adds no new log events; it only enriches existing
ticket-specific events.

## Worker Pane Names

Keep the existing logical pane ID, `ticket-coding-<manager-id>-<ticket-id>`, so
runtime identity, event matching, retries, and cleanup remain unchanged. Set
only the display-oriented `CreatePaneRequest.Name` to:

```text
[<ticket-id>] <normalized-title>
```

Normalize the title with Unicode-aware whitespace fields: newlines, carriage
returns, tabs, and repeated spaces collapse to one ASCII space. Limit the
normalized title portion to 32 Unicode code points. When it exceeds that
limit, retain the first 31 code points and append the single-character
ellipsis `…`. This avoids splitting a UTF-8 encoding while keeping pane names
compact without adding a display-width dependency.

The Zellij backend already passes `CreatePaneRequest.Name` as one `--name`
argument, so spaces and Unicode do not require shell escaping. The logical
pane ID remains the value used by the runtime registry and manager operations.

## Data Flow

`Manager.startSlot` already receives the complete `Ticket` returned by
`Store.Next`, including `Title`, and stores it in the worker slot. Startup logs
and the display name read `ticket.Title`; later lifecycle and recovery logs
read `slot.ticket.Title`. No additional database query, store interface
change, or runtime call is required.

## Testing

Manager tests will capture the configured log writer and exercise successful
start/close events plus representative startup and post-start failure paths.
Assertions will require `ticket=<id> title="<title>"` and verify that special
characters remain escaped on one line. Pane creation assertions will verify
the `[id] title` name, whitespace normalization, 32-code-point truncation, and
preservation of the logical pane ID. Existing manager and repository tests
must continue to pass.
