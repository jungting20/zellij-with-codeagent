# Implementation Progress

## 2026-05-14

### Current Status

`U1` through `U5. Add Subscription Manager and Event Bus` are complete.

The daemon exposes an in-process `eventbus.Bus`, owns long-lived `zellij subscribe --format json` subprocesses per managed pane via `SubscriptionManager`, normalizes NDJSON frames through `ParseSubscribeNDJSONLine`, publishes typed events (`raw_output`, semantic matchers, `pane_closed`, `subscribe_error`, `health_changed`), and exposes them through `RuntimeService.SubscribeEvents`. Reconciliation, introspection, planner transports, and the supervisor remain deferred.

### Implemented

- Created `internal/zellij/types.go`
- Created `internal/zellij/commands.go`
- Created `internal/zellij/backend.go`
- Created `internal/zellij/backend_test.go`
- Created `internal/eventbus/types.go`
- Created `internal/eventbus/bus.go`
- Created `internal/eventbus/bus_test.go`
- Created `internal/runtime/types.go`
- Created `internal/runtime/service.go`
- Created `internal/runtime/service_test.go`
- Created `internal/runtime/parser.go`
- Created `internal/runtime/parser_test.go`
- Created `internal/runtime/subscriptions.go`
- Created `internal/runtime/subscriptions_test.go`
- Created `internal/runtime/integration_test.go` updates for subscriptions
- Updated `internal/runtime/doc.go`
- Updated `cmd/agentd/main.go`

### Zellij Backend Behavior

- `NewBackend` creates a CLI-backed Zellij controller with configurable binary path, session name, and command runner.
- `CreatePane` runs `zellij action new-pane`, supports pane name, cwd, and direct command arguments, and parses the returned pane ID.
- `ClosePane` runs `zellij action close-pane --pane-id`.
- `SendInput` serializes per-pane input operations and preserves paste-before-enter ordering for newline-terminated input.
- `ListPanes` runs `zellij action list-panes --json` and parses pane metadata into typed records.
- `DumpScreen` runs `zellij action dump-screen` with optional `--full` and `--ansi`.
- `SubscribeCommand` builds the long-running `zellij subscribe --format json` command spec consumed by `SubscriptionManager`.
- Failed subprocess calls return `CommandError` with the operation name and stderr detail.
- Malformed `list-panes` JSON is surfaced as an error instead of being hidden.

### Runtime Service Behavior

- `RuntimeService` exposes structured in-process methods for creating panes, sending input, listing panes, inspecting registry metadata, snapshotting output, closing panes, and subscribing to runtime events via `SubscribeEvents`.
- `CreatePane` calls the Zellij backend, registers a stable logical pane record, starts pane subscriptions when `Options.SubscriptionRunner` is configured (defaults to `ExecSubscriptionRunner` in `cmd/agentd`), then returns both logical and Zellij pane IDs.
- `SubscribeEvents` returns a buffered fan-out channel from `internal/eventbus` plus an unregister closure tied to the caller context.
- Pane records preserve Zellij tab ID and tab name metadata.
- `CreatePane` can create a new Zellij tab before registering the managed pane, or target an existing tab by ID.
- Pane-specific operations resolve logical pane IDs through the registry before touching backend-owned Zellij IDs.
- Unknown logical pane IDs return `ErrPaneNotFound` without backend calls.
- Backend create failures leave the registry unchanged.
- Registry failures after backend pane creation trigger best-effort backend cleanup.
- Snapshot output updates the registry's latest output summary (still usable alongside subscribe-derived summaries).
- Close failures mark the logical pane record as `error`; successful closes mark it `closed` and stop the pane subscribe loop.

### Event Bus Behavior

- `eventbus.Bus` publishes typed `eventbus.Event` values with pane/task/agent identifiers to subscribers registered via context-bound cancellation.
- Slow subscribers drop overflow events instead of blocking publishers.

### Subscription Manager Behavior

- `ExecSubscriptionRunner` wraps `exec.CommandContext` with stderr discarded so malformed stderr cannot deadlock pipes.
- `SubscriptionManager` maps NDJSON `pane_update` frames into normalized viewport text, dedupes identical payloads per pane for noisy renders, updates registry `LastOutput`, emits `raw_output`, and runs lightweight semantic matchers (`server_ready`, `test_failed`, `test_passed`).
- `pane_closed` frames flip registry status to `exited` and emit `pane_closed`.
- Malformed frames emit `subscribe_error` events without tearing down unrelated subscriptions.

### Verification

- `go test ./...` passed.
- `AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run TestIntegrationCreateSnapshotAndClosePane -v` created a real Zellij tab and pane, captured output, and closed it.
- `AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run TestIntegrationSubscribeEmitsRawOutput -v` observed streamed viewport updates via subscribe-derived events.

### Not Implemented Yet

- Reconciliation and cleanup automation (`U6`)
- Runtime introspection (`U7`)
- AI planner integration and semantic matchers tuned beyond MVP regex/heuristics
- External transports such as HTTP, Unix socket, stdio JSON-RPC, or gRPC

### Next Step

Start `U6. Implement Reconciliation and Cleanup`.

Align daemon-owned logical pane records with live Zellij metadata (`list-panes`), recover lost panes safely, and keep subscribe goroutines lifecycle-aligned during reconciliation passes.

## 2026-05-12

### Current Status

`U1. Bootstrap Go Module and Daemon Layout` and `U2. Define Runtime Domain Model and Registry` are complete.

The project now has a minimal Go module, an `agentd` daemon entrypoint, and an in-memory registry package for stable logical pane records. This is still pre-runtime-service; event bus, Zellij backend, external transport, and AI planner integration are not implemented yet.

### Implemented

- Created `go.mod`
- Created `cmd/agentd/main.go`
- Created `cmd/agentd/main_test.go`
- Created `internal/runtime/doc.go`
- Created `internal/registry/types.go`
- Created `internal/registry/registry.go`
- Created `internal/registry/registry_test.go`

### Agentd Behavior

- `go run ./cmd/agentd` prints a daemon skeleton message.
- `go run ./cmd/agentd --help` prints usage.
- `go run ./cmd/agentd --version` prints `agentd dev`.
- Unknown arguments return exit code `2` and print usage to stderr.

### Verification

- `go test ./...` passed.
- `go run ./cmd/agentd --help` passed.
- `go run ./cmd/agentd --version` passed.

### Registry Behavior

- Logical `PaneID`, `TaskID`, and `AgentID` are daemon-owned identifiers.
- `ZellijPaneID` is stored as a backend identifier, not the primary registry key.
- `RegisterPane` creates stable pane records with role, command, cwd, status, timestamps, and output summary fields.
- `GetPane`, `ListPanes`, `UpdatePaneStatus`, `UpdatePaneOutput`, `RemovePane`, and `GetLatestByZellijPaneID` are implemented.
- Returned records clone command slices so callers cannot mutate registry state by changing returned values.
- Reusing a Zellij pane ID for a new logical pane does not mutate the old logical record.

### Not Implemented Yet

- `RuntimeService` interface
- Event bus
- Zellij CLI backend
- Subscription manager
- Reconciliation and cleanup
- Runtime introspection
- AI planner
- External transports such as HTTP, Unix socket, stdio JSON-RPC, or gRPC

### Next Step

Start `U3. Implement Zellij CLI Backend`.

The next implementation should encapsulate all `zellij` subprocess calls behind `internal/zellij`, including create pane, close pane, send input, list panes, dump screen, and subscribe-oriented command construction.
