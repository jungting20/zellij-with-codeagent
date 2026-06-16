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

From the repository root:

```bash
go test ./...
```

Build the local binaries when you want to use the checked-in scripts:

```bash
go build -o bin/agentd ./cmd/agentd
go build -o bin/agentctl ./cmd/agentctl
go build -o bin/agent-planner ./cmd/agent-planner
go build -o bin/agent-role ./cmd/agent-role
```

Start the current daemon entrypoint:

```bash
go run ./cmd/agentd
```

Without subcommands, the entrypoint still prints `agentd daemon skeleton` for the original smoke path.

Start the local transport daemon:

```bash
go run ./cmd/agentd serve --socket /tmp/agentd.sock
```

The `serve` command exposes JSON HTTP over the Unix socket. It does not bind a TCP port.

Use `agentctl` as the thin command-line client for the local socket:

```bash
go run ./cmd/agentctl health --socket /tmp/agentd.sock
go run ./cmd/agentctl status --socket /tmp/agentd.sock
go run ./cmd/agentctl plan --socket /tmp/agentd.sock --file examples/plans/agent-role-demo.json
go run ./cmd/agent-planner page --socket /tmp/agentd.sock --url http://localhost:8000/example/aa --cwd "$PWD" --mock-source "$PWD/README.md" --ui
go run ./cmd/agent-planner validate --file plan.json
go run ./cmd/agent-planner submit --socket /tmp/agentd.sock --file plan.json --ui
go run ./cmd/agentctl events --socket /tmp/agentd.sock --limit 20
go run ./cmd/agentctl events --socket /tmp/agentd.sock --follow --type raw_output
go run ./cmd/agentctl input --socket /tmp/agentd.sock coder --text $'go test ./...\n'
go run ./cmd/agentctl snapshot --socket /tmp/agentd.sock coder --full
go run ./cmd/agentctl cleanup --socket /tmp/agentd.sock --task feature-auth
```

`agentctl plan` accepts either a raw execution plan payload or a full `/v1/requests` envelope.
`agent-planner page` is a mock planner path for URL-based page inspection. It resolves the page source from `--mock-source`, generates a canonical `/v1/requests` `execution_plan`, and submits panes for editor, LSP, network, and console inspection. Add `--dry-run` to print the envelope without contacting `agentd`.
`agent-planner validate` and `agent-planner submit` accept AI-generated JSON files, require the canonical `/v1/requests` envelope, and reject legacy or unknown payload fields before submission.

## Runtime Service Shape

Future planners and developer harnesses should call `RuntimeService`, not Zellij directly:

```go
service := runtime.NewService(runtime.Options{
    Registry:           registry.New(),
    Backend:            zellij.NewBackend(zellij.Options{}),
    SubscriptionRunner: runtime.ExecSubscriptionRunner{},
})

created, err := service.CreatePane(ctx, runtime.CreatePaneRequest{
    ID:      "pane-1",
    TaskID:  "task-1",
    Role:    runtime.PaneRoleTest,
    Command: []string{"go", "test", "./..."},
})
```

The core operations are:

- `CreatePane`, `SendInput`, `SnapshotOutput`, and `ClosePane` for managed pane control.
- `SubscribeEvents` and `RecentEvents` for raw output, semantic matcher events, pane close events, subscribe errors, and health changes.
- `InspectPane`, `ListPanes`, and `InspectRuntime` for current daemon-owned state.
- `Reconcile` to align registry state with live Zellij pane metadata.
- `Cleanup` to close daemon-managed panes while preserving unmanaged panes in the same session.

## Transport API

`agentd serve --socket <path>` exposes these local endpoints:

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

In v1, `session` is used as `task_id`; a tab name defaults to the session when omitted. `layout` is validated metadata (`triple-horizontal` today); physical layout forcing is deferred.

## Zellij Session Selection

`zellij.NewBackend(zellij.Options{Session: "name"})` adds `--session name` to Zellij CLI calls. Tests also honor `ZELLIJ_SESSION_NAME` for real-Zellij integration and E2E runs:

```bash
ZELLIJ_SESSION_NAME=my-session AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1
```

When no session is configured, the backend lets Zellij use its default session behavior.

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
