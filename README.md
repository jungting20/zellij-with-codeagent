# Zellij Agent Runtime

`zellij-with-codeagent` is an MVP Go runtime that lets an agent manage Zellij panes through a daemon-owned boundary. Zellij remains the terminal execution fabric, but `agentd` owns logical pane IDs, registry state, subscriptions, events, reconciliation, cleanup, and introspection.

## Current MVP

- `cmd/agentd` starts the daemon skeleton and wires the in-process runtime service.
- `internal/runtime.RuntimeService` is the primary boundary for callers and future planners.
- `internal/zellij` is the only package that shells out to the Zellij CLI.
- `internal/registry` is the system of record for daemon-managed panes.
- `internal/eventbus` publishes normalized runtime events and retains recent event history.
- `internal/supervisor` builds a read-only status view from runtime introspection.
- `internal/transport` exposes the runtime over local JSON HTTP on a Unix domain socket.

The transport is local-only and still intended for developer validation, but external clients no longer need to call the Go service in process.

## Requirements

- Go 1.22 or newer
- Zellij installed and available as `zellij`
- A running Zellij environment for integration or E2E tests

## Run

For a command-by-command smoke flow covering daemon startup, TUI submission, pane creation, and cleanup, see `docs/zellij-agent-quickstart.md`.

From the repository root:

```bash
go test ./...
```

Build the unified local binary:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
```

The legacy entrypoints are still available as compatibility wrappers when needed:

```bash
go build -o bin/agentd ./cmd/agentd
go build -o bin/agentctl ./cmd/agentctl
go build -o bin/agent-planner ./cmd/agent-planner
go build -o bin/agent-role ./cmd/agent-role
```

Start the current daemon entrypoint:

```bash
./bin/zellij-agent daemon
```

Without subcommands, the entrypoint still prints `agentd daemon skeleton` for the original smoke path.

Start the local transport daemon:

```bash
./bin/zellij-agent daemon serve
```

The `serve` command exposes JSON HTTP over the default Unix socket `/tmp/agentd.sock`. It does not bind a TCP port. Pass `--socket <path>` only when you need an override.
Use the built `zellij-agent` binary for planner flows so generated panes can call back into the same stable executable path.

Use `zellij-agent ctl` as the thin command-line client for the local socket:

```bash
./bin/zellij-agent ctl health
./bin/zellij-agent ctl status
./bin/zellij-agent ctl plan --file examples/plans/agent-role-demo.json
./bin/zellij-agent planner page --url http://localhost:8000/example/aa --cwd "$PWD" --ui
./bin/zellij-agent planner tui
./bin/zellij-agent planner validate --file plan.json
./bin/zellij-agent planner submit --file plan.json --ui
./bin/zellij-agent ctl events --limit 20
./bin/zellij-agent ctl events --follow --type raw_output
./bin/zellij-agent ctl input coder --text $'go test ./...\n'
./bin/zellij-agent ctl snapshot coder --full
./bin/zellij-agent ctl cleanup --task feature-auth
```

### Personal Work Launcher

`zellij-agent work` starts a daemon-managed mixed coding workspace for the current repository:

```bash
./bin/zellij-agent work "implement the mixed work command"
```

`--zellij-session` selects the physical Zellij session. When omitted, the CLI
uses its own `ZELLIJ_SESSION_NAME`. The logical `--session` flag remains the
execution task ID. Commands fail before submission when neither source names a
physical Zellij session.

The command creates one Zellij tab with five panes:

- `coder`: interactive Codex session through `zellij-agent role coding-agent <cwd>`, with the goal prefilled for review; press Enter to submit it.
- `test`: test shell prepared with a default command detected from root project markers (Go, npm/pnpm/Yarn, or Rust).
- `review`: non-interactive Codex review assistant seeded with the goal.
- `lazygit`: repository Git UI through `lazygit`.
- `notes`: session notes and useful `zellij-agent ctl` commands.

The runtime waits for the Codex input prompt, up to `--timeout` (15 seconds by default), before pasting the exact trimmed goal without an Enter key. Review or edit the text in Codex, then press Enter when you want the coding session to begin. `--dry-run` exposes the value and readiness marker as the coder pane's `initial_input` and `initial_input_ready_text` without creating a workspace.

Project detection reads only known marker files in the selected working-directory root. By default the test pane suggests the detected command without running it. `--auto-test` runs that detected command once when the pane starts. If markers conflict, `package.json` is malformed, or no Node `test` script exists, the workspace still opens with feedback disabled and an actionable reason in the test and notes panes.

Useful options:

```bash
./bin/zellij-agent work --dry-run --session work-command --zellij-session physical-a "implement the mixed work command"
./bin/zellij-agent work --session work-command --zellij-session physical-a "implement the mixed work command"
./bin/zellij-agent work --cwd /path/to/repo "implement the mixed work command"
./bin/zellij-agent work --socket /tmp/agentd.sock "implement the mixed work command"
./bin/zellij-agent work --timeout 30s "implement the mixed work command"
./bin/zellij-agent work --auto-test "implement the mixed work command"
```

The dry-run envelope makes the distinction visible in its payload:

```json
{
  "session": "work-command",
  "zellij_session": "physical-a"
}
```

The daemon must be running before non-dry-run submission:

```bash
./bin/zellij-agent daemon serve
```

### Chrome Tab Watcher

`zellij-agent chrome` submits a watcher pane that tracks newly opened Chrome
tabs. `--zellij-session` selects the physical Zellij session. When omitted, the
CLI uses its own `ZELLIJ_SESSION_NAME`. The logical `--session` flag remains the
execution task ID. Commands fail before submission when neither source names a
physical Zellij session.

Use a dry run to inspect both values without contacting the daemon:

```bash
./bin/zellij-agent chrome --dry-run --session chrome-debug --zellij-session physical-a
```

```json
{
  "session": "chrome-debug",
  "zellij_session": "physical-a"
}
```

Arguments after `--` configure the watcher, for example `--port 9333
--no-launch`. Use `--no-watch` before `--` to create one `tab-network` pane
instead of watching for new tabs.

### Ticket Worker Pool

`zellij-agent ticket-worker` launches a project-configured pool of identical worker commands. Initialize the project-local configuration from the project root:

```bash
./bin/zellij-agent ticket-worker init
```

For `ticket-worker start`, `--zellij-session` selects the physical Zellij
session. When omitted, the CLI uses its own `ZELLIJ_SESSION_NAME`. The logical
`--session` flag remains the execution task ID. Commands fail before submission
when neither source names a physical Zellij session.

This creates `.zellij-agent/worker/config.yaml` and refuses to overwrite an existing file. Use `ticket-worker init --force` to replace it, or `--cwd /path/to/project` to select another project root. The version-1 configuration is:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
worker:
  command: ["go", "run", "./cmd/ticket-worker"]
  completion_marker: "ZELLIJ_AGENT_WORKER_DONE"
```

`version` must be `1`. `max_workers` and `poll_interval` must be positive; when omitted, they default to `3` and `30s`. `worker.command` is a non-empty argument vector executed directly from the project root, not through a shell. `worker.completion_marker` must be a non-empty single line with no surrounding whitespace. Unknown fields are rejected.

After replacing the example command with the project's worker entrypoint, start the daemon and workspace:

```bash
./bin/zellij-agent daemon serve
./bin/zellij-agent ticket-worker start
```

`start` validates the complete configuration before submitting a new `ticket-worker` tab. The initial plan contains exactly two bootstrap panes: the deterministic worker manager and a read-only dashboard monitor. The manager waits until its registered anchor pane is visible in the runtime before creating workers, then creates up to `max_workers` worker panes in that same tab. The `--timeout` value bounds both transport requests and this startup-readiness wait. A one-run capacity override does not change the file:

```bash
./bin/zellij-agent ticket-worker start --max-workers 5 --session tickets --zellij-session physical-a
```

Use `--dry-run` to print the `/v1/requests` execution-plan envelope without
contacting the daemon or creating panes:

```bash
./bin/zellij-agent ticket-worker start --dry-run --session tickets --zellij-session physical-a
```

```json
{
  "session": "tickets",
  "zellij_session": "physical-a"
}
```

`--cwd`, `--config`, `--session`, `--zellij-session`, `--socket`, and
`--timeout` are also available; run `ticket-worker start --help` for their exact
forms.

The project worker command owns the ticket workflow. It must atomically claim its own next ticket, invoke the coding agent or project-specific ticket skill, implement and verify the change, update the ticket system, and only then print the configured completion marker. If no ticket is available, the project command also decides whether and when to print the marker. The manager has no ticket-system knowledge and does not claim tickets, interpret ticket IDs, or infer success from process exit or idle output.

Completion requires an output line from the watched logical pane whose surrounding whitespace is trimmed and then exactly equals `completion_marker`. Substrings do not match, and an identical marker from another pane cannot complete the watched worker. Process exit, pane-close events, unchanged output, and silence are not completion signals.

When a worker matches the marker, the manager closes that logical pane through the runtime and makes its slot eligible for refill on the next polling tick. A worker may print the exact marker and exit immediately; if closing then reports that the runtime record is already absent, the manager reconciles runtime state and releases the slot. Create failures leave a slot empty for a later retry. Watch failures and marker-less exits leave the worker unresolved. Other close failures keep the slot occupied, so replacements cannot exceed configured capacity.

Canceling or closing the manager stops marker watches and future creation but performs zero worker close or cleanup calls; existing worker panes are deliberately preserved. Version 1 does not recover or adopt those panes after a manager or daemon restart, persist pool state, automatically retry stalled workers, or handle failed/waiting worker policy. Starting another manager while prior workers remain is an operator error. The monitor is read-only and cannot create, close, retry, or send input to workers.

The initial release also has known limitations around repeated starts, dashboard capacity display, malformed marker error mapping, multi-document YAML, and real-Zellij end-to-end coverage. See [`docs/ticket-worker-known-issues.md`](docs/ticket-worker-known-issues.md) for reproduction conditions and follow-up directions.

### Planner Commands

`zellij-agent ctl plan` accepts either a raw execution plan payload or a full `/v1/requests` envelope.
`zellij-agent planner page` is a mock planner path for URL-based page inspection. It uses a built-in mock source by default, generates a canonical `/v1/requests` `execution_plan`, and submits panes for editor, LSP, network, and console inspection. Add `--dry-run` to print the envelope without contacting `agentd`.
`zellij-agent planner tui` provides the same mock planner path through a single chat-style prompt. Include the URL in the natural-language request, for example `localhost:8000/example/aa 페이지 소스 열고 네트워크/콘솔 확인해줘`; the mock source and cwd default from the current repo, and generated panes call back into `zellij-agent role`.
`zellij-agent planner validate` and `zellij-agent planner submit` accept AI-generated JSON files, require the canonical `/v1/requests` envelope, and reject legacy or unknown payload fields before submission.

For planner commands that submit or generate plans, `--zellij-session` selects
the physical Zellij session. When omitted, the CLI uses its own
`ZELLIJ_SESSION_NAME`. The logical `--session` flag remains the execution task
ID. Commands fail before submission when neither source names a physical Zellij
session. A page dry run shows the generated logical session alongside the
explicit physical target:

```bash
./bin/zellij-agent planner page --url http://localhost:8000/example/aa --dry-run --zellij-session physical-a
```

```json
{
  "session": "page-example-aa",
  "zellij_session": "physical-a"
}
```

## Runtime Service Shape

Future planners and developer harnesses should call `RuntimeService`, not Zellij directly:

```go
service := runtime.NewService(runtime.Options{
    Registry:           registry.New(),
    Backend:            zellij.NewBackend(zellij.Options{}),
    SubscriptionRunner: runtime.ExecSubscriptionRunner{},
})

created, err := service.CreatePane(ctx, runtime.CreatePaneRequest{
    ID:            "pane-1",
    TaskID:        "task-1",
    ZellijSession: "physical-a",
    Role:          runtime.PaneRoleTest,
    Command:       []string{"go", "test", "./..."},
})
```

The core operations are:

- `CreatePane`, `SendInput`, `SnapshotOutput`, and `ClosePane` for managed pane control.
- `SubscribeEvents` and `RecentEvents` for raw output, semantic matcher events, pane close events, subscribe errors, and health changes.
- `InspectPane`, `ListPanes`, and `InspectRuntime` for current daemon-owned state.
- `Reconcile` to align registry state with live Zellij pane metadata.
- `Cleanup` to close daemon-managed panes while preserving unmanaged panes in the same session.

## Transport API

`zellij-agent daemon serve` exposes these local endpoints on `/tmp/agentd.sock` by default:

- `GET /v1/health`
- `POST /v1/requests`
- `POST /v1/panes`
- `GET /v1/panes`
- `POST /v1/panes/{pane_id}/input`
- `POST /v1/panes/{pane_id}/snapshot`
- `GET /v1/runtime`
- `GET /v1/events/recent`
- `GET /v1/events/stream`
- `POST /v1/reconcile`
- `POST /v1/cleanup`

Requests and responses use logical daemon IDs (`pane_id`, `task_id`, `agent_id`) as the contract identifiers. Zellij pane IDs are returned only as backend metadata for debugging.

`POST /v1/requests` accepts typed envelopes. The `execution_plan` type creates all panes for one logical session across one or more Zellij tabs:

```json
{
  "type": "execution_plan",
  "request_id": "req_123",
  "payload": {
    "session": "feature-auth",
    "zellij_session": "physical-a",
    "layout": "triple-horizontal",
    "tabs": [
      {
        "name": "feature-auth",
        "panes": [
          { "id": "planner", "role": "planner" },
          { "id": "frontend", "role": "react-dev" }
        ]
      }
    ]
  }
}
```

In v1, `session` is used as `task_id`; `zellij_session` selects the physical
Zellij session; and a tab name defaults to the logical session when omitted.
`layout` is validated metadata (`triple-horizontal` today); physical layout
forcing is deferred.

## Zellij Session Selection

Each execution plan carries its physical Zellij target as `zellij_session`.
At the CLI boundary, explicit `--zellij-session` wins; otherwise the CLI reads
its own `ZELLIJ_SESSION_NAME`. A missing physical session is rejected before
submission. The plan's logical `session` remains the daemon task ID and does
not select a Zellij instance.

`zellij.NewBackend(zellij.Options{Session: "name"})` adds `--session name` to Zellij CLI calls. Tests also honor `ZELLIJ_SESSION_NAME` for real-Zellij integration and E2E runs:

```bash
ZELLIJ_SESSION_NAME=my-session AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1
```

Runtime pane creation rejects requests that omit the physical Zellij session.

## Manual Verification

Automatic real-Zellij integration tests create panes and clean them up:

```bash
AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1
```

Manual E2E tests intentionally leave panes open for observation:

```bash
AGENTD_ZELLIJ_E2E=1 go test ./internal/runtime -run '^TestE2ECreateTabAndFourPanesPrintRegistry$' -v -count=1
```

See `docs/runtime-e2e-test.md` for the close-on-input E2E flow and cleanup notes.

For a current manual CLI flow, see `docs/manual-smoke-test.md`.

## Invariants

- Planners and clients must not invoke Zellij directly. They request outcomes through `RuntimeService`.
- External clients should use the local transport or compatible client wrapper, which still delegates to `RuntimeService`.
- `agentd` is the only owner of Zellij mutations for managed panes: create, input, subscribe, snapshot, reconcile, close, and cleanup.
- Logical `PaneID` values are daemon-owned and stable. Zellij pane IDs are backend identifiers and may disappear or be reused.
- The registry is the system of record for managed runtime state. Zellij is the execution runtime, not the durable state source.
- Unmanaged live Zellij panes may be reported by reconciliation, but they are not adopted or closed by default.
- Subscription lifecycles must follow pane lifecycles. Lost, exited, closed, and cleanup-closed panes should not keep subscribe processes alive.
- Debug views and future transports should expose the same runtime state that planner integrations use.

## Current Limitations

- Local-only, in-memory runtime state.
- No restart persistence beyond what can be rediscovered through reconciliation.
- Rule-based semantic event matchers only.
- No rich TUI dashboard yet.
# zellij-with-codeagent
