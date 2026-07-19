# Ticket Worker Per-Ticket Prompt Design

## Goal

Store the complete coding instruction for each ticket in the project-local
ticket database. The ticket manager uses this stored prompt instead of
rendering a project-wide prompt template from worker configuration.

This is a clean schema replacement. Existing version 1 databases are not
migrated; users must delete the old ticket-worker database and run
`zellij-agent ticket-worker init` again.

## Public Interface

Registering a ticket requires a prompt:

```text
zellij-agent ticket-worker add \
  --title TITLE \
  --summary SUMMARY \
  --spec PATH \
  --plan PATH \
  --prompt PROMPT \
  [--json]
```

`--prompt` accepts the prompt body directly, including multiline shell-quoted
text. It is required and must contain non-whitespace content. A prompt that
contains the reserved completion marker prefix
`ZELLIJ_AGENT_TICKET_DONE` is rejected so ticket content cannot accidentally
or intentionally complete its own worker run.

The `show` human-readable output includes the complete stored prompt. JSON
ticket output includes a `prompt` property. The compact human-readable `list`
output remains unchanged and does not print prompt bodies.

## Data Model and Schema

Add `Prompt string` to `Ticket` and `CreateInput`. The new tickets table has:

```sql
prompt TEXT NOT NULL CHECK (length(trim(prompt)) > 0)
```

The schema version becomes 2. Initialization of a new database creates the
version 2 schema directly. Opening a version 1 database returns a clear error
that tells the user to remove
`.zellij-agent/ticket-worker/tickets.db` and rerun `ticket-worker init`.
There is no `ALTER TABLE` path, row backfill, legacy prompt fallback, or
automatic database deletion.

Every ticket query and scan includes `prompt`. Adding a ticket trims leading
and trailing whitespace before validation and persistence while preserving
internal whitespace and line breaks.

## Configuration

Remove `prompt_template` from the generated worker configuration and from the
in-memory configuration model. The configuration retains only its version,
worker capacity, and polling interval:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
```

YAML decoding continues to tolerate unknown keys. An old `prompt_template`
entry is therefore ignored, but it has no runtime effect and is no longer
generated, validated, or documented. This avoids coupling config cleanup to
database recreation.

## Manager Data Flow

The manager claims a ticket through the existing store boundary. It takes the
ticket's stored `Prompt` as the complete coding instruction and appends the
existing completion instruction as a final paragraph:

```text
작업을 모두 완료한 뒤 마지막 줄에 따옴표 없이
"ZELLIJ_AGENT_TICKET_DONE <ID>"만 출력하세요.
```

The completion marker itself remains derived from the ticket ID and is never
stored in the database. Existing exact-line completion detection and ticket
lifecycle transitions remain unchanged. No planner or client calls Zellij
directly; pane creation and observation continue through the runtime client
used by the ticket manager.

This is persistence and background worker logic. The existing default
`ticket-manager` role remains the feature's runtime owner, so no new role is
introduced.

## Errors

- Missing `--prompt` is a CLI usage error, consistent with other required
  `add` flags.
- Empty or whitespace-only prompt content returns a dedicated
  `ErrInvalidPrompt` domain error, classified as a CLI validation error.
- Content containing `ZELLIJ_AGENT_TICKET_DONE` returns the same prompt
  validation error.
- A version 1 database is rejected before ticket queries run, with explicit
  delete-and-reinitialize guidance.
- Versions newer than 2 remain unsupported schema errors.

No command removes the old database automatically because deletion is a user
operation and may destroy queued ticket data.

## Testing

Store tests cover fresh version 2 initialization, prompt validation,
multiline prompt round trips, and prompt preservation through get, list,
claim, requeue, and lifecycle transitions. A fixture version 1 database must
be rejected with the reinitialization guidance and must not be modified.

CLI tests cover the required `--prompt` flag, empty and reserved-marker input,
multiline input, human `show` output, compact `list` output, and JSON output.

Configuration tests assert that generated YAML omits `prompt_template`, valid
capacity and polling settings still load, and an unknown legacy key does not
affect runtime behavior.

Prompt and manager tests assert that the stored prompt is passed without
template rendering, the completion instruction is appended exactly once, and
completion detection still requires the exact marker line.

Documentation examples show `ticket-worker add --prompt` and describe the
per-ticket prompt contract. Verification runs focused ticket-worker and CLI
tests, `go test ./...`, and the repository's required unified binary build and
atomic registration flow.

## Out of Scope

- Migrating or backing up version 1 databases
- Falling back to config-based prompt templates
- Reading prompts from a file or standard input
- Editing a ticket prompt after creation
- Changing manager capacity, polling, completion detection, or lifecycle
  semantics
