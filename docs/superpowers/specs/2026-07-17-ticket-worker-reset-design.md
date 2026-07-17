# Ticket Worker Reset Design

## Goal

Reset `ticket-worker` to an intentionally empty starting point. The public
`zellij-agent ticket-worker` command name remains available, but none of the
existing configuration, pane-pool, manager, monitoring, completion, or ticket
lifecycle behavior remains executable.

## Command Contract

`zellij-agent ticket-worker` remains visible in the top-level command list.
Its dedicated CLI package retains only a minimal `Run` entrypoint.

- `zellij-agent ticket-worker` prints a concise not-implemented message and
  returns a non-zero exit code.
- `zellij-agent ticket-worker --help` describes the command as an empty,
  not-yet-implemented placeholder and returns success.
- Former subcommands such as `init`, `start`, and the internal `manager` are no
  longer recognized and return a non-zero exit code.
- The placeholder does not read configuration, contact the daemon, submit a
  plan, create panes, or launch another process.

## Code Removal

Delete the feature implementation under `internal/ticketworker`, including its
configuration schema, plan construction, worker manager, completion runner,
and tests. Replace the existing `internal/cli/ticketworker` implementation and
tests with the minimal placeholder contract.

Keep the top-level dispatch in `cmd/zellij-agent`, but remove ticket-worker
client construction and other dependencies that existed only to support the
old implementation. General runtime, transport, dashboard, and Zellij behavior
remain unchanged.

## Documentation Removal

Remove the active README instructions, known-issues page, and ticket-worker-only
historical specifications and implementation plans for the discarded feature.
Remove stale ticket-worker assertions from command help tests. In shared design
documents, remove only claims that the ticket-worker implementation currently
exists; preserve unrelated historical context and other command behavior.

## Tests and Verification

Focused tests verify that:

- the top-level help still lists `ticket-worker`;
- `ticket-worker --help` succeeds and describes a placeholder;
- a bare invocation and former subcommands fail without side effects; and
- no old ticket-worker implementation package or feature documentation remains.

Run `gofmt` on edited Go files, then `go test ./...`. Build the unified binary
with `go build -o bin/zellij-agent ./cmd/zellij-agent`, verify its help and
placeholder behavior, and install it atomically on the custom CLI path according
to the repository instructions because the unified binary changed.

## Non-goals

This reset does not design the replacement ticket-worker, preserve backward
compatibility for its old configuration or subcommands, or alter general
runtime APIs that may also be used by other features.
