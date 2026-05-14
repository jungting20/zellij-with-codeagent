# Implementation Progress

## 2026-05-14

### Current Status

`U1. Bootstrap Go Module and Daemon Layout`, `U2. Define Runtime Domain Model and Registry`, `U3. Implement Zellij CLI Backend`, and `U4. Create Internal RuntimeService` are complete.

The project now has a testable `internal/runtime` service boundary that wires the registry and Zellij backend together for daemon-owned pane operations. This is still pre-event-streaming; the runtime service exists, but it is not yet wired into an event bus, subscription manager, reconciliation loop, introspection surface, or planner-facing transport.

### Implemented

- Created `internal/zellij/types.go`
- Created `internal/zellij/commands.go`
- Created `internal/zellij/backend.go`
- Created `internal/zellij/backend_test.go`
- Created `internal/runtime/types.go`
- Created `internal/runtime/service.go`
- Created `internal/runtime/service_test.go`
- Updated `cmd/agentd/main.go`

### Zellij Backend Behavior

- `NewBackend` creates a CLI-backed Zellij controller with configurable binary path, session name, and command runner.
- `CreatePane` runs `zellij action new-pane`, supports pane name, cwd, and direct command arguments, and parses the returned pane ID.
- `ClosePane` runs `zellij action close-pane --pane-id`.
- `SendInput` serializes per-pane input operations and preserves paste-before-enter ordering for newline-terminated input.
- `ListPanes` runs `zellij action list-panes --json` and parses pane metadata into typed records.
- `DumpScreen` runs `zellij action dump-screen` with optional `--full` and `--ansi`.
- `SubscribeCommand` builds the long-running `zellij subscribe` command spec for future subscription manager ownership.
- Failed subprocess calls return `CommandError` with the operation name and stderr detail.
- Malformed `list-panes` JSON is surfaced as an error instead of being hidden.

### Runtime Service Behavior

- `RuntimeService` exposes structured in-process methods for creating panes, sending input, listing panes, inspecting registry metadata, snapshotting output, and closing panes.
- `CreatePane` calls the Zellij backend, registers a stable logical pane record, and returns both logical and Zellij pane IDs.
- Pane records now preserve Zellij tab ID and tab name metadata.
- `CreatePane` can create a new Zellij tab before registering the managed pane, or target an existing tab by ID.
- Pane-specific operations resolve logical pane IDs through the registry before touching backend-owned Zellij IDs.
- Unknown logical pane IDs return `ErrPaneNotFound` without backend calls.
- Backend create failures leave the registry unchanged.
- Registry failures after backend pane creation trigger best-effort backend cleanup.
- Snapshot output updates the registry's latest output summary.
- Close failures mark the logical pane record as `error`; successful closes mark it `closed`.
- `agentd` now has an internal runtime service construction point while external transports remain deferred.

### Verification

- `go test ./...` passed.
- `AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run TestIntegrationCreateSnapshotAndClosePane -v` created a real Zellij tab and pane, captured output, and closed it.

### Not Implemented Yet

- Event bus
- Subscription manager that owns long-running subscribe processes
- Reconciliation and cleanup
- Runtime introspection
- AI planner
- External transports such as HTTP, Unix socket, stdio JSON-RPC, or gRPC

### Next Step

Start `U5. Add Subscription Manager and Event Bus`.

The next implementation should stream pane output from Zellij, normalize it into runtime events, and publish those events through an internal event bus.

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
