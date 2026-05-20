# Implementation Progress

## 2026-05-19

### Current Status

External transport is implemented on top of the existing runtime MVP.

`agentd serve --socket <path>` now exposes the daemon runtime through local JSON HTTP over a Unix domain socket without importing `internal/zellij` or calling Zellij directly.

### Implemented

- Created `internal/transport/types.go`
- Created `internal/transport/errors.go`
- Created `internal/transport/server.go`
- Created `internal/transport/client.go`
- Added transport, client, server, command, and env-gated E2E tests
- Updated `cmd/agentd/main.go` with `serve --socket <path>`
- Updated README, runtime E2E docs, and architecture invariants

### Transport Behavior

- `agentd serve --socket <path>` starts a long-running local server on a Unix domain socket.
- The transport exposes health, pane creation, input, snapshot, runtime inspection, recent events, live event streaming, reconcile, and cleanup endpoints.
- Requests use logical daemon IDs (`pane_id`, `task_id`, `agent_id`) as the contract identifiers.
- Runtime errors are normalized into structured JSON errors with stable codes such as `bad_request`, `not_found`, `runtime_error`, `cleanup_partial`, `stream_closed`, and `timeout`.
- Live events are streamed as newline-delimited JSON and late clients can recover with `RecentEvents`.

### Verification

- `go test ./...` passed.

### Not Implemented Yet

- Real LLM planner integration
- Durable restart recovery or persisted event/task history
- Rich TUI supervisor/dashboard

### Next Step

Future work can integrate a real LLM planner that uses the transport contract.

## 2026-05-15

### Current Status

`U1` through `U8. Document MVP Operation and Invariants` are complete.

The runtime can now reconcile daemon-owned logical pane records against live `zellij list-panes --json` state, clean up daemon-managed panes while preserving unmanaged panes in the same Zellij session, expose in-process introspection for managed panes, grouped runtime status, latest output summaries, and recent event history, and document MVP operation plus invariants for future planner and transport work. Reconciliation and cleanup keep subscription lifecycles aligned by stopping subscriptions for lost, exited, or cleanup-closed panes. AI planner integration and external transports remain deferred.

### Implemented

- Created `internal/runtime/reconcile.go`
- Created `internal/runtime/reconcile_test.go`
- Created `internal/runtime/cleanup.go`
- Created `internal/runtime/cleanup_test.go`
- Created `internal/runtime/introspection.go`
- Created `internal/runtime/introspection_test.go`
- Created `internal/supervisor/view.go`
- Created `internal/supervisor/view_test.go`
- Created `README.md`
- Updated `internal/eventbus/bus.go`
- Updated `internal/eventbus/bus_test.go`
- Updated `internal/runtime/types.go`
- Updated `internal/runtime/service_test.go`
- Updated `internal/runtime/integration_test.go` with real-Zellij reconciliation and cleanup coverage
- Updated `docs/ai-agent-zellij-runtime-architecture.md`
- Updated `docs/runtime-e2e-test.md`

### Reconciliation Behavior

- `RuntimeService.Reconcile` compares registry records with non-plugin panes returned by the Zellij backend.
- Live managed panes are marked `running`.
- Managed panes reported by Zellij as exited are marked `exited` and have subscriptions stopped.
- Managed panes missing from the live Zellij pane list are marked `lost` and have subscriptions stopped.
- Unmanaged live panes are reported in the response but are not adopted or closed.
- `list-panes` failures leave registry state intact and publish a `health_changed` event.

### Cleanup Behavior

- `RuntimeService.Cleanup` closes daemon-managed panes, with optional logical pane ID, task ID, and role filters.
- Cleanup skips terminal records that are already `closed`, `exited`, or `lost`.
- Successful cleanup marks records `closed` and stops subscriptions.
- Close failures mark only the failed pane `error`, continue cleanup attempts for later panes, and return `ErrCleanupPartial` with per-pane failure details.
- Unknown explicitly requested pane IDs are reported as cleanup failures without backend calls.

### Introspection Behavior

- `eventbus.Bus` retains a bounded recent event history and returns cloned event slices in publication order.
- `RuntimeService.InspectRuntime` returns a developer-facing status view with managed panes, counts by lifecycle state, panes grouped by task and role, and latest output summaries.
- Empty registries return a useful `no managed panes` status without special-case caller logic.
- `RuntimeService.RecentEvents` returns recent events with optional type filtering and limit handling after filtering.
- `supervisor.BuildView` combines runtime status and recent events into a read-only dashboard-ready view without adding an external transport.
- Unknown pane output snapshots still return `ErrPaneNotFound` before any backend `dump-screen` call.

### MVP Operation and Invariants

- `README.md` documents setup, `go run ./cmd/agentd`, `go test ./...`, real-Zellij integration tests, manual E2E tests, and current MVP limitations.
- The README describes the internal `RuntimeService` boundary, core operations, Zellij session selection through backend options or `ZELLIJ_SESSION_NAME`, and cleanup expectations.
- `docs/ai-agent-zellij-runtime-architecture.md` now records implementation invariants: planners and clients must not call Zellij directly, the registry is the managed state source, Zellij pane IDs are backend IDs, unmanaged panes are not adopted or closed by default, and subscription lifecycles follow pane lifecycles.

### Verification

- `go test ./...` passed.
- `go test ./internal/eventbus ./internal/runtime ./internal/supervisor` passed.
- `AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration(Reconcile|Cleanup)' -v -count=1` created real Zellij panes, reconciled an externally closed pane to a terminal runtime state, cleaned up managed panes, and preserved an unmanaged pane until test cleanup.

### Not Implemented Yet

- AI planner integration and semantic matchers tuned beyond MVP regex/heuristics
- External transports such as HTTP, Unix socket, stdio JSON-RPC, or gRPC

### Next Step

No further MVP implementation unit is listed in the current plan. Future work can start from the deferred planner integration, semantic matcher tuning, or external transport design.

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
