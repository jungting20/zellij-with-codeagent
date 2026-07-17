# Ticket Worker SQLite Queue Design

## Goal

Restore `zellij-agent ticket-worker` as a project-local ticket queue by porting
the existing `ra-ticket` implementation from
`/Users/hwangjungho/myprojuct/romance-agent/tools/ra-ticket`. Preserve its
ticket model, SQLite state machine, FIFO claiming, CLI output, structured JSON,
and exit-code contracts while integrating them into the unified binary.

This phase provides ticket persistence and ticket-management commands. It does
not restore the previous Zellij pane pool, worker manager, completion-marker
watching, or automatic coding-agent launcher.

## Public Commands

The unified binary exposes:

```text
zellij-agent ticket-worker init
zellij-agent ticket-worker add --title TITLE --summary SUMMARY --spec PATH --plan PATH [--json]
zellij-agent ticket-worker list [--status STATUS] [--json]
zellij-agent ticket-worker next [--json]
zellij-agent ticket-worker show ID [--json]
zellij-agent ticket-worker start ID [--json]
zellij-agent ticket-worker done ID [--json]
zellij-agent ticket-worker cancel ID [--json]
zellij-agent ticket-worker reopen ID [--json]
```

`ticket-worker --help` lists these commands and returns success. A missing or
unknown command returns the usage exit code. The imported human-readable output,
JSON field names, structured JSON errors, and exit codes `0` through `7` remain
compatible with `ra-ticket`.

## Repository Discovery and Initialization

Every command searches upward from its starting directory for the nearest
`.git` entry and treats that directory as the project root. Commands fail with
a validation error when no Git project root exists.

The database path is:

```text
<project-root>/.zellij-agent/ticket-worker/tickets.db
```

Only `ticket-worker init` may create the directory, database, and schema.
Initialization is idempotent: rerunning it validates the existing schema and
preserves all data. A schema version newer than the supported version is an
error.

Initialization also updates `<project-root>/.gitignore` idempotently. It appends
the exact entry `.zellij-agent/ticket-worker/` only when an equivalent exact
line is absent, preserves existing content, and ensures the appended entry is
on its own line. Failure to update `.gitignore` is an initialization failure.

All other commands require the database to exist. They must not create a file
implicitly and instead report:

```text
ticket-worker is not initialized; run zellij-agent ticket-worker init
```

## Ticket Model and Persistence

Port the `ra-ticket` schema version 1 without semantic changes. Each ticket has
an integer ID, title, summary, approved design path, approved implementation
plan path, status, and lifecycle timestamps. Status values remain `ready`,
`in_progress`, `done`, and `cancelled`.

`add` requires non-empty title and summary plus existing regular Markdown files
under `docs/superpowers/specs/` and `docs/superpowers/plans/` inside the project.
Resolved symlinks may not escape the project. A plan path uniquely identifies a
ticket and duplicate plans are rejected.

`next` uses a SQLite `BEGIN IMMEDIATE` transaction to select the oldest ready
ticket by creation time and ID, then atomically changes it to `in_progress`.
The imported transitions remain:

- `start`: `ready` to `in_progress`
- `done`: `in_progress` to `done`
- `cancel`: `ready` or `in_progress` to `cancelled`
- `reopen`: `done` or `cancelled` to `ready`, clearing lifecycle timestamps

SQLite uses the pure-Go `modernc.org/sqlite` driver, a five-second busy timeout,
and one open connection per Store, matching the source implementation.

## Components

- `internal/ticketworker/model.go` owns ticket types, statuses, actions, and
  domain errors.
- `internal/ticketworker/store.go` owns schema initialization, existing-database
  opening, persistence, FIFO claiming, and transitions.
- `internal/ticketworker/repository.go` owns Git-root discovery, database-path
  construction, and idempotent `.gitignore` updates.
- `internal/cli/ticketworker/ticketworker.go` owns public argument parsing,
  initialization dispatch, store lifetime, output formatting, and exit-code
  mapping.
- `cmd/zellij-agent/main.go` passes the current directory and clock dependencies
  into the ticket-worker CLI without contacting the daemon.

The port adapts Go package paths and the initialization boundary only. It does
not invoke the external `ra-ticket` binary or retain a second Go module.

## Error Handling

The command returns concise stderr diagnostics and never panics. `--json`
requests preserve the imported `{ "error": ..., "code": ... }` contract.
Usage, not-found, invalid-transition, duplicate, empty-queue, validation, and
database errors retain their source exit-code classifications. Initialization
errors use the existing database or validation categories as appropriate.

Opening an existing database is distinct from initializing it: the former
checks existence before `sql.Open` so SQLite cannot silently create a missing
file.

## Tests and Verification

Port the source repository, Store, and CLI tests. Preserve coverage of schema
versioning, FIFO order, concurrent claiming, valid and invalid transitions,
artifact containment, duplicate plans, output formats, and error mapping.

Add focused coverage for:

- idempotent `init` creating schema version 1;
- repeated `init` preserving tickets;
- exact, non-duplicated `.gitignore` insertion;
- commands before `init` failing without creating the database;
- nested-directory Git-root discovery; and
- top-level unified CLI help and an end-to-end lifecycle in a temporary project.

Run focused package tests, `go test ./...`, and `git diff --check`. Build
`bin/zellij-agent`, exercise `init/add/list/next/done` against a temporary Git
project, and atomically install the verified binary at
`~/.config/custom-cli/zellij-agent` according to the repository instructions.
