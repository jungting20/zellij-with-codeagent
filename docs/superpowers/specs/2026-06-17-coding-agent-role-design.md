# Coding Agent Role Design

## Goal

Add an `agent-role coding-agent <path>` role that starts the Codex coding agent inside the repository containing `<path>`.

## Scope

The role is a manual role command. It is not added to the page planner's automatic page-inspection pane set in this change.

## Behavior

- `agent-role coding-agent <path>` requires exactly one path argument.
- The path is normalized to an absolute path.
- The path must exist and may be either a file or directory.
- If the path is a file, repository detection starts from its parent directory.
- If the path is a directory, repository detection starts from that directory.
- The role walks upward until it finds a `.git` entry and uses that directory as the repository root.
- The `codex` executable must be available on `PATH`.
- The role starts `codex` with stdin, stdout, and stderr attached to the current terminal and with the working directory set to the discovered repository root.
- The role returns Codex's exit code when Codex exits.
- If validation fails, the role prints a direct error message to stderr and exits non-zero.

## Architecture

The implementation follows the existing role structure:

- `internal/roles/roles.go` owns role metadata and exposes it through `roles.All()` and `roles.Lookup()`.
- `internal/cli/role/role.go` dispatches `coding-agent` to a focused role package.
- `cmd/agent-role/codingagent` owns argument validation and process execution for the role.

The role package separates validation and command creation from terminal execution so tests can cover behavior without launching an interactive Codex process.

## Testing

Tests cover:

- The role catalog includes `coding-agent` with usage, description, and required `path` argument metadata.
- Validation rejects missing paths and paths outside a repository.
- Command construction resolves `codex` from `PATH`, sets `cmd.Dir` to the containing repository root, and prepares an interactive command.

Integration with an actual `codex` binary remains a manual smoke test because it depends on the developer's local Codex installation and interactive terminal.
