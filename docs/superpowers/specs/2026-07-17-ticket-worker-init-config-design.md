# Ticket Worker Init Config Design

## Goal

Extend `zellij-agent ticket-worker init` so it creates a project-local worker
configuration alongside the existing SQLite ticket queue. The configuration
keeps only worker capacity, queue polling cadence, and the prompt template that
will be sent to a coding agent for a claimed ticket.

## Scope

- Keep the existing config path: `.zellij-agent/worker/config.yaml` at the Git
  project root.
- Keep `max_workers` and `poll_interval` from the former ticket-worker config.
- Add a coding-agent `prompt_template`.
- Remove the former worker command, completion marker, completion command, and
  completion timeout fields from the schema.
- Do not restore worker panes, a manager, ticket completion execution, or any
  other pre-reset runtime behavior.

## Configuration Schema

The generated YAML is:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
prompt_template: |
  티켓 #{{ .ID }}을 구현해줘.

  제목: {{ .Title }}
  요약: {{ .Summary }}
  설계: {{ .SpecPath }}
  구현 계획: {{ .PlanPath }}
```

`version` remains explicit so later schema changes can be rejected or migrated
deterministically. `max_workers` defaults to `3` and must be positive.
`poll_interval` defaults to `30s` and must parse as a positive Go duration.
`prompt_template` is required, must contain non-whitespace content, and must
parse as a Go `text/template` template.

Template rendering will expose one ticket view with these fields:

- `ID`
- `Title`
- `Summary`
- `SpecPath`
- `PlanPath`

This change defines and validates the template contract but does not yet launch
a coding agent or render the template as part of a worker flow.

## Initialization Behavior

`ticket-worker init` continues to discover the enclosing Git root. It then:

1. initializes or opens `.zellij-agent/ticket-worker/tickets.db` without
   removing existing ticket data;
2. ensures `.zellij-agent/ticket-worker/` appears exactly once in the root
   `.gitignore`;
3. creates `.zellij-agent/worker/config.yaml` with the default template only
   when that file does not already exist.

Repeated initialization is idempotent. In particular, it never overwrites an
existing config because that file may contain a user-edited prompt. No `--force`
option is added. A user who wants the generated defaults again can remove the
config file and rerun `init`.

Config creation uses exclusive file creation so concurrent or repeated
initialization cannot silently replace an existing file. Its parent directory
is created as needed. The config file uses mode `0644`, matching the former
project config behavior.

If config creation fails, `init` reports an initialization error and exits
nonzero. Database or `.gitignore` work completed earlier is not rolled back;
those artifacts are independently safe and a later `init` can finish the
missing config initialization.

On success, human-readable output identifies both the initialized database and
the config path, so the caller can immediately find and edit the prompt.

## Code Boundaries

The `internal/ticketworker` package owns:

- the config path and default YAML template;
- the typed config model and strict YAML decoding;
- value and `text/template` syntax validation;
- create-if-missing config initialization.

The `internal/cli/ticketworker` package remains responsible for command
dispatch, exit-code mapping, and success/error output. It calls the package
initialization API rather than manipulating project files itself.

The CLI and config code do not call Zellij. Any later worker implementation
must continue to route pane operations through `RuntimeService` or the local
transport wrappers.

## Validation and Error Handling

The loader rejects:

- unsupported `version` values;
- unknown YAML fields;
- zero or negative `max_workers`;
- invalid, zero, or negative `poll_interval` values;
- empty or whitespace-only `prompt_template` values;
- templates that fail Go `text/template` parsing.

The schema accepts omitted `max_workers` and `poll_interval` and applies their
documented defaults. `version` and `prompt_template` remain required. Loading
does not rewrite the file.

## Tests and Documentation

Package tests cover:

- the exact config path and generated defaults;
- loading the generated file;
- default application for omitted optional settings;
- every validation failure class;
- preserving an edited config across repeated initialization;
- recreating defaults after the config is removed;
- initialization failure reporting without deleting existing queue data.

CLI and unified-binary tests assert that `ticket-worker init` creates the
database, `.gitignore` entry, and config, and that a second invocation preserves
the config. README documentation lists the config path, generated fields,
available template variables, and edit/delete-and-reinitialize behavior.

Final verification runs `gofmt` on edited Go files, `go test ./...`, builds
`bin/zellij-agent`, and registers the rebuilt binary atomically at
`~/.config/custom-cli/zellij-agent` according to the repository instructions.
