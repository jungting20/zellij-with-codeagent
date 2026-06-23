# Work Command Design

## Goal

Add a personal `zellij-agent work` command that starts a mixed automation coding workspace for the current repository.

The command should turn one natural-language goal into a daemon-managed Zellij tab with useful panes already prepared. It should reuse the existing execution-plan transport and runtime boundary instead of calling Zellij directly.

## Scope

The first version is intentionally personal and conservative:

- Add `zellij-agent work "<goal>"`.
- Generate a fixed mixed-mode execution plan.
- Submit the plan through the existing local transport.
- Keep the main coding agent interactive.
- Prepare helper panes for tests, review, and notes.
- Support a dry-run mode that prints the generated execution-plan envelope.

This version does not implement autonomous multi-agent orchestration, cross-pane decision loops, persistent work history, or a rich TUI dashboard.

## User Interface

Primary usage:

```bash
zellij-agent work "implement the mixed work command"
```

Supported options:

```bash
zellij-agent work --dry-run "implement the mixed work command"
zellij-agent work --session work-command "implement the mixed work command"
zellij-agent work --cwd /path/to/repo "implement the mixed work command"
zellij-agent work --auto-test "implement the mixed work command"
zellij-agent work --socket /tmp/agentd.sock --timeout 10s "implement the mixed work command"
```

The goal can be passed as positional arguments. The command joins all remaining positional arguments with spaces.

## Pane Set

The generated plan creates one tab with four panes:

- `coder`: interactive Codex pane for hands-on implementation.
- `test`: shell pane dedicated to test commands.
- `review`: non-interactive Codex review assistant seeded with the work goal.
- `notes`: shell pane that prints the session goal and useful follow-up commands.

The default session ID is derived from the goal with a stable slug and a `work-` prefix. `--session` overrides it.

## Pane Behavior

`coder` runs the existing `zellij-agent role coding-agent <cwd>` command. It opens an interactive Codex session in the repository so the user remains in control of implementation.

`test` runs a shell script in `<cwd>`. Without `--auto-test`, it prints the recommended `go test ./...` command and leaves an interactive shell open. With `--auto-test`, it runs `go test ./...` once, prints a marker, then leaves the shell open.

`review` runs `codex exec --cd <cwd> -` with a focused prompt. The prompt asks for risks, missing tests, and implementation advice for the supplied goal. It must not edit files.

`notes` runs a shell script in `<cwd>` that prints the session, goal, cwd, and common control commands such as status, events, snapshot, and cleanup, then leaves an interactive shell open.

## Architecture

The implementation should add a focused CLI package and a small plan builder:

- `cmd/zellij-agent/main.go` dispatches the top-level `work` command.
- `internal/cli/work` owns flag parsing, goal parsing, client setup, dry-run output, and user-facing summaries.
- `internal/work` owns goal normalization, session slugging, command construction, and `transport.ExecutionPlanPayload` generation.

The new command uses `transport.Client.SubmitExecutionPlan`. It must not call the Zellij backend or shell out to `zellij` directly.

## Data Flow

1. Parse flags and positional goal text.
2. Resolve `cwd`, defaulting to the current working directory.
3. Build a `transport.ExecutionPlanPayload` with the fixed mixed pane set.
4. Wrap it in a `/v1/requests` execution-plan envelope for dry-run output.
5. If not dry-run, submit the payload to the daemon over the configured Unix socket.
6. Print the created session, tab, and pane IDs.

## Error Handling

The command should fail before submission when:

- The goal is empty.
- The working directory cannot be resolved.
- The generated plan is invalid.

Submission errors should include the socket path and a short hint to start `zellij-agent daemon serve`.

Pane commands may fail after creation if `codex` or `go` is not available. Those failures should remain visible inside the relevant Zellij pane rather than blocking plan submission.

## Testing

Unit tests should cover:

- Goal parsing from positional arguments.
- Session slug generation and explicit session overrides.
- Plan generation for default and `--auto-test` modes.
- Command construction for all four panes.
- Dry-run envelope shape and validation.
- CLI validation for empty goals and unexpected arguments.

Existing runtime and transport tests cover plan submission behavior. Real Zellij behavior remains a manual smoke test because it depends on a running Zellij session, the local daemon, and local Codex availability.
