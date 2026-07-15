# Debate Background Role Pipeline Design

**Date:** 2026-07-15

## Goal

Rewrite `debate-background` around the independently executable `debate-proposer`, `debate-critic`, and `debate-judge` roles. The rewrite must not reuse the existing background orchestration implementation. It must preserve the existing public CLI option names where practical, add structured output, and keep `code-review` working.

## Scope

This change replaces the background-only implementation and tests. It does not modify the interactive/Zellij-backed `debate` command or the implementations and system prompts of the three roles.

The following existing background implementation is removed rather than adapted:

- `internal/debate/background.go` and its background-only tests
- the implementation of `internal/cli/debatebackground/debatebackground.go`
- tests that assert the old agent fan-out and coordinator model

The `internal/cli/debatebackground.Run` entrypoint remains at the same package path because `cmd/zellij-agent` and `internal/cli/codereview` call it. Its implementation and dependencies are written anew.

## Decisions

### Fixed role pipeline

Every round executes these roles sequentially:

1. `debate-proposer`, backed by `agy`
2. `debate-critic`, backed by Cursor `agent`
3. `debate-judge`, backed by `codex`

The set and order are not configurable. The deprecated `--agents` and `--config` flags remain parseable for CLI compatibility, emit warnings to stderr, and are ignored.

### Role CLI boundary

The orchestrator invokes the current `zellij-agent` executable as a child process:

```text
zellij-agent role debate-proposer --output-format json <cwd>
zellij-agent role debate-critic --output-format json <cwd>
zellij-agent role debate-judge --output-format json <cwd>
```

The role-specific prompt is delivered through stdin. The orchestrator does not import provider implementations, reconstruct role system prompts, invoke `agy`, `agent`, or `codex` directly, or parse provider-specific output.

Each child must return exactly one `debate-role/v1` JSON document. The runner validates:

- valid JSON with no trailing non-whitespace data
- `schema_version` equals `debate-role/v1`
- `role` matches the requested role
- `engine` matches the role catalog (`agy`, `agent`, or `codex`)
- `status` equals `success`
- `content` is not blank

### New package boundary

A new `internal/backgrounddebate` package owns the workflow. It has no dependency on `internal/debate` or on provider-specific role packages.

Its main units are:

- **Orchestrator:** validates workflow options, runs the fixed role sequence, constructs prompts, and accumulates results.
- **RoleRunner interface:** accepts a role, repository, prompt, and execution context and returns a validated `debate-role/v1` result.
- **Process role runner:** invokes the current executable's `role` subcommand and validates its structured response.
- **Result model:** represents completed rounds, final content, and an optional structured failure.

The CLI package owns flag parsing, timeouts, output rendering, file persistence, and optional post-debate Codex startup. Workflow code remains independent of terminal streams and filesystem output.

## Data Flow

### First round

The proposer receives the original topic. The critic receives the original topic and current proposal. The judge receives the original topic, current proposal, and current critique.

### Later rounds

The proposer receives the original topic and the previous round judge's final recommendation, with an instruction to produce an improved proposal. The critic and judge receive only the current round inputs described above.

The full earlier transcript is not copied into later prompts. This prevents prompt growth while preserving the judge-approved direction between rounds. All responses remain available in the final result.

### Prompt framing

The orchestrator uses explicit labelled sections for untrusted role output, for example `TOPIC`, `CURRENT_PROPOSAL`, `CURRENT_CRITIQUE`, and `PREVIOUS_JUDGMENT`. It tells each role to treat embedded responses as debate material rather than instructions. Role system prompts remain exclusively owned by the role commands.

## Rounds and Failure Semantics

`--rounds` remains limited to 1 through 3. Each round contains the complete proposer-critic-judge sequence.

The pipeline is strict. If a role exits unsuccessfully, times out, emits invalid JSON, or violates the role schema contract, the orchestrator stops immediately. It does not retry and does not invoke later roles.

The result records:

- requested and completed round counts
- all successfully completed role responses
- the failed round and role
- a stable failure kind
- a human-readable diagnostic
- the child exit code when available

A round counts as completed only after its judge succeeds.

The overall `--timeout` covers the complete command. `--agent-timeout` applies separately to each role child. The earlier deadline wins, and cancellation terminates the active child process.

## CLI Contract

### Preserved options

- `--topic`
- `--rounds`
- `--cwd`
- `--timeout`
- `--agent-timeout`
- `--output`
- `--start-codex`
- `--codex-bin`

### Added option

- `--output-format text|json`, defaulting to `text`

### Deprecated compatibility options

- `--agents`
- `--config`

These options are accepted and ignored. Supplying either produces a clear warning on stderr. Their values never change the fixed role pipeline.

Unknown positional arguments remain usage errors. Invalid options, an empty topic, an invalid round count, non-positive timeouts, an inaccessible repository, and the combination of `--output-format json` with `--start-codex` return exit code 2 and do not create a result file.

Execution and persistence failures return exit code 1. Successful runs return 0.

## Structured Output

JSON mode emits exactly one JSON document to stdout. Progress, warnings, and diagnostics go to stderr.

The top-level success shape is:

```json
{
  "schema_version": "debate-background/v1",
  "status": "success",
  "topic": "topic text",
  "rounds_requested": 2,
  "rounds_completed": 2,
  "rounds": [
    {
      "round": 1,
      "proposer": {
        "schema_version": "debate-role/v1",
        "role": "debate-proposer",
        "engine": "agy",
        "status": "success",
        "content": "..."
      },
      "critic": {
        "schema_version": "debate-role/v1",
        "role": "debate-critic",
        "engine": "agent",
        "status": "success",
        "content": "..."
      },
      "judge": {
        "schema_version": "debate-role/v1",
        "role": "debate-judge",
        "engine": "codex",
        "status": "success",
        "content": "..."
      }
    }
  ],
  "final_content": "last successful judge content",
  "output_path": "/tmp/zellij-agent-debate-...json"
}
```

Failure JSON uses the same top-level schema and accumulated `rounds`, sets `status` to `failed`, omits `final_content` when no judge completed, and includes:

```json
{
  "failure": {
    "round": 1,
    "role": "debate-critic",
    "kind": "role_execution_failed",
    "message": "...",
    "exit_code": 1
  }
}
```

`exit_code` is omitted when no child exit code exists. Stable failure kinds distinguish timeout, child execution, malformed output, contract mismatch, and empty content.

Text mode renders the same result model as readable Markdown. It includes the topic, status, every completed role response grouped by round, any partial current round, failure details, and the last successful judge recommendation. Existing scripts still see a save notice before the rendered result.

## Persistence

The default `--output /tmp` behavior remains. A directory target generates a timestamped filename with `.md` for text mode and `.json` for JSON mode. A file target is used exactly as supplied.

The CLI resolves the output path before rendering so `output_path` can be included consistently. It writes to a temporary file in the destination directory, sets restrictive permissions, and renames it atomically. Both successful and execution-failure results are saved. CLI validation failures are not saved.

If persistence fails, the command reports the error on stderr and exits 1. JSON mode still emits one failure result to stdout, with a persistence failure replacing the prior status if necessary; it never prints a separate save notice to stdout.

## Progress and Diagnostics

Progress always goes to stderr, including:

- round started and completed
- role started and completed
- deprecated option warnings
- saved output path in JSON mode

Child stderr is captured for diagnostics and is not streamed into stdout. Diagnostics are bounded before inclusion in the result to avoid oversized or sensitive accidental output.

## `--start-codex` and `code-review`

`--start-codex` remains available only in text mode and only after a successful debate result has been saved and printed. It launches the selected `--codex-bin` with the saved result path, preserving the current `code-review` handoff behavior without coupling it to the new judge role process.

`internal/cli/codereview` continues to call `debatebackground.Run` with its existing topic, rounds, and `--start-codex` arguments. Its tests are rewritten against the new CLI dependencies and output rather than the deleted background runner.

## Testing Strategy

All background-specific tests are written against the new contracts rather than copied from the old implementation.

### Orchestrator tests

- executes proposer, critic, and judge in exact order
- runs the complete sequence for each requested round
- passes the previous judge result only to the next proposer
- frames current proposal and critique correctly
- stops immediately on each possible role failure
- reports partial rounds and completed-round counts accurately

### Process runner tests

- invokes `role <name> --output-format json <cwd>` on the configured executable
- sends the prompt through stdin
- accepts a valid `debate-role/v1` response
- rejects malformed or trailing JSON
- rejects schema, role, engine, status, and content mismatches
- maps child exit codes and enforces cancellation/timeouts

### CLI tests

- preserves documented flags and help
- warns and ignores `--agents` and `--config`
- keeps stdout JSON-only in JSON mode
- renders text and JSON from the same result model
- saves success and execution-failure results atomically
- rejects JSON plus `--start-codex`
- launches post-debate Codex only after successful text output
- uses exit codes 0, 1, and 2 consistently

### Integration and regression tests

- `zellij-agent debate-background` dispatches correctly
- `code-review` continues through the new background pipeline
- the normal role catalog and the three standalone roles remain unchanged

Final verification runs `go test ./...`, builds `bin/zellij-agent`, and immediately copies it to `~/.config/custom-cli` as required by the repository instructions.

## Non-goals

- configurable role sets or ordering
- parallel role execution
- automatic retry
- resuming a partially completed debate
- changing role system prompts or provider commands
- routing background debate through Zellij or the daemon
- changing the interactive `debate` command
