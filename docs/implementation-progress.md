# Implementation Progress

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
