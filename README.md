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

### Coding Agent Dashboard

Run coding agents in a new pane of the current Zellij tab through the unified
CLI. These commands include each tool's permission-bypass option by default:

```bash
./bin/zellij-agent agent start codex
./bin/zellij-agent agent start claude --cwd /path/to/project
./bin/zellij-agent agent start gemini -- --model gemini-3
./bin/zellij-agent agent start cursor
```

The configured executables are `codex`, `claude`, `agy` for Gemini, and
`agent` for Cursor. Only agents started through this runtime appear in the
dedicated dashboard:

```bash
./bin/zellij-agent agent dashboard
```

Use `j`/`k` or the arrow keys to select an agent, `Enter` to switch to its
session and focus its pane, `R` to refresh, and `q` to quit. The dashboard
shows the detected agent state (`idle`, `working`, `blocked`, or `unknown`),
agent kind, project, and time in the current state.

Coding-agent records are in-memory. A pane close notification removes its
record immediately. In addition, the daemon reconciles Zellij every two
seconds; if a managed pane no longer exists, runtime reconciliation triggers
the same close observer cleanup. Listing agents also removes any remaining
orphan whose runtime pane is absent.

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

### Ticket Worker SQLite Queue

`zellij-agent ticket-worker` manages a ticket queue stored inside each Git
project. Initialize it from the project root or any nested directory:

```bash
./bin/zellij-agent ticket-worker init
```

Initialization creates `.zellij-agent/ticket-worker/tickets.db` and
`.zellij-agent/worker/config.yaml` at the Git root, then adds
`.zellij-agent/ticket-worker/` to the root `.gitignore`. It is idempotent:
running it again preserves existing tickets, does not duplicate the ignore
entry, and never overwrites an existing worker config.

The generated worker config contains the coding-agent capacity and polling
cadence:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
```

To regenerate the defaults, delete only `.zellij-agent/worker/config.yaml` and
run `ticket-worker init` again. Other ticket commands never create a database
implicitly and report an initialization error until `init` succeeds.

Register a ticket from an approved Superpowers design and implementation plan:

```bash
./bin/zellij-agent ticket-worker add \
  --title "Add search" \
  --summary "Implement indexed search" \
  --spec docs/superpowers/specs/2026-07-17-search-design.md \
  --plan docs/superpowers/plans/2026-07-17-search.md \
  --worktree-branch feat/search \
  --prompt $'Implement the approved search plan.\nRun the complete test suite.'
```

The worktree branch name is required and stored with the ticket. The spec and plan must be existing Markdown files under
`docs/superpowers/specs/` and `docs/superpowers/plans/`. A plan can be
registered only once. The required prompt is stored with the ticket and used
as the coding-agent instruction. The manager appends its completion-marker
instruction automatically. Queue and lifecycle commands are:

```bash
./bin/zellij-agent ticket-worker list [--status ready] [--no-prompt]
./bin/zellij-agent ticket-worker next
./bin/zellij-agent ticket-worker show ID
./bin/zellij-agent ticket-worker start [--zellij-session NAME]
./bin/zellij-agent ticket-worker done ID
./bin/zellij-agent ticket-worker cancel ID
./bin/zellij-agent ticket-worker reopen ID
```

`start` creates one runtime-managed `ticket-manager` pane in a new
`ticket-worker` tab. With no active workers, the manager fills the tab. While
workers are active, the manager occupies the top 50% and all coding-agent
workers share the bottom 50% side by side; Zellij reflows that row whenever a
worker opens or closes. The manager claims the oldest `ready` tickets, starts
up to `max_workers` coding-agent panes, and continues polling for new tickets.
Every coding-agent created by the manager runs in YOLO mode, bypassing Codex
approvals and sandboxing. The manager uses `--zellij-session` when supplied or
`ZELLIJ_SESSION_NAME` when run inside Zellij. The unified CLI automatically
starts the local daemon when needed.

`next` remains the explicit manual claim operation: it atomically moves the
oldest `ready` ticket to `in_progress`. Add `--json` to ticket data commands
other than `init` and `start` for machine-readable output and structured
errors.

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
    InitialInput:          "Run the assigned task.\n",
    InitialInputReadyText: "›",
})
```

When `InitialInput` is set, `CreatePane` returns success only after the pane
shows `InitialInputReadyText` (when provided) and the runtime delivers the
input. Initialization failure rolls back the new pane; a `cleanup_partial`
error means callers must inspect and finish cleanup.

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
- Dashboard state is not restored after a daemon restart.
# zellij-with-codeagent
