# Ticket Manager Role Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persistent `ticket-manager` role that fills a configured coding-agent pool from the SQLite ticket queue, detects safe ticket-specific completion markers through runtime output subscription, persists completion, closes panes, and refills capacity.

**Architecture:** Keep queue and orchestration logic in `internal/ticketworker`, with all external effects injected through store and runtime-client interfaces. Keep role flag parsing and dependency wiring in `cmd/agent-role/ticketmanager`, and keep shared role dispatch limited to catalog lookup and package selection.

**Tech Stack:** Go 1.26, SQLite through `modernc.org/sqlite`, existing local JSON/Unix-socket transport, Go `text/template`, standard `testing` package.

## Global Constraints

- Role name is exactly `ticket-manager`; Go package name is `ticketmanager`; constant name is `RoleTicketManager`.
- All pane creation, input, output events, snapshots, inspection, and close operations go through local transport/runtime boundaries, never direct Zellij calls.
- Completion marker is exactly `ZELLIJ_AGENT_TICKET_DONE <ID>` and only an exact trimmed output line may complete a ticket.
- The submitted prompt contains the marker only inside double quotes within an instruction sentence.
- `done` persistence happens before pane close; failed persistence keeps the pane open.
- Existing worker config schema and database schema version remain unchanged.
- Do not add a top-level `ticket-worker start` command or planner-generated role.
- Update the external role summary at `/Users/in05908_mac/.config/pi/docs/agent-roles.md`.
- After verification, rebuild and atomically register `bin/zellij-agent` at `~/.config/custom-cli/zellij-agent`.

---

### Task 1: Prompt Rendering and Manager-Only Requeue

**Files:**
- Create: `internal/ticketworker/prompt.go`
- Create: `internal/ticketworker/prompt_test.go`
- Modify: `internal/ticketworker/store.go`
- Modify: `internal/ticketworker/store_test.go`
- Modify: `internal/ticketworker/repository.go`
- Modify: `internal/ticketworker/repository_test.go`

**Interfaces:**
- Produces: `CompletionMarker(ticketID int64) (string, error)`
- Produces: `RenderTicketPrompt(cfg Config, ticket Ticket) (prompt string, marker string, err error)`
- Produces: `(*Store).Requeue(ctx context.Context, id int64) (Ticket, error)`
- Extends: `FindRoot(start string)` to accept either a file or directory path.

- [ ] **Step 1: Write failing prompt tests**

Test a ticket with ID `42` and all five template fields. Require marker
`ZELLIJ_AGENT_TICKET_DONE 42`, rendered ticket content, and this exact suffix:

```text
작업을 모두 완료한 뒤 마지막 줄에 따옴표 없이 "ZELLIJ_AGENT_TICKET_DONE 42"만 출력하세요.
```

Assert the raw standalone marker never appears as its own line in the submitted prompt. Reject zero/negative ticket IDs and template execution errors.

- [ ] **Step 2: Write failing requeue and file-root tests**

Add store tests requiring `Requeue` to change only `in_progress` to `ready`, clear `StartedAt`, update `UpdatedAt`, preserve other ticket fields, and reject missing or non-`in_progress` tickets with the existing domain errors. Add a repository test that passes a regular file nested under a Git root to `FindRoot` and receives the root.

- [ ] **Step 3: Run focused tests to verify failure**

Run: `go test ./internal/ticketworker -run 'Test(RenderTicketPrompt|CompletionMarker|Requeue|FindRootFromFile)' -count=1`

Expected: FAIL because the new functions and file-path behavior do not exist.

- [ ] **Step 4: Implement prompt rendering**

Use these concrete contracts:

```go
const completionMarkerPrefix = "ZELLIJ_AGENT_TICKET_DONE"

func CompletionMarker(ticketID int64) (string, error)
func RenderTicketPrompt(cfg Config, ticket Ticket) (string, string, error)
```

Parse `cfg.PromptTemplate` with `missingkey=error`, execute it with the `Ticket`, trim trailing whitespace, and append two newlines plus the quoted Korean completion instruction. Build the marker with `strconv.FormatInt` and reject nonpositive IDs.

- [ ] **Step 5: Implement transactional requeue and file-root discovery**

`Requeue` must use a transaction, load the target ticket, require `StatusInProgress`, update `status='ready'`, `updated_at`, and `started_at=NULL`, commit, and return the updated value. `FindRoot` must `os.Stat` the absolute starting path and begin at its parent when it is a regular file.

- [ ] **Step 6: Format and verify Task 1**

Run: `gofmt -w internal/ticketworker/prompt.go internal/ticketworker/prompt_test.go internal/ticketworker/store.go internal/ticketworker/store_test.go internal/ticketworker/repository.go internal/ticketworker/repository_test.go && go test ./internal/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

```bash
git add internal/ticketworker/prompt.go internal/ticketworker/prompt_test.go internal/ticketworker/store.go internal/ticketworker/store_test.go internal/ticketworker/repository.go internal/ticketworker/repository_test.go
git commit -m "feat: add ticket manager prompt lifecycle"
```

---

### Task 2: Ticket Manager Pool and Output Subscription

**Files:**
- Create: `internal/ticketworker/manager.go`
- Create: `internal/ticketworker/manager_test.go`

**Interfaces:**
- Consumes: `RenderTicketPrompt`, `Store.Next`, `Store.Transition(ActionDone)`, and `Store.Requeue`.
- Produces: exported `ManagerStore`, `ManagerClient`, `ManagerOptions`, `Manager`, and `NewManager(ManagerOptions) (*Manager, error)`.

- [ ] **Step 1: Define failing manager startup and capacity tests**

Use fake store/client/event stream implementations. Require zero claims before a matching anchor and established stream; reject same pane ID with wrong task/session; after readiness, claim FIFO tickets and create at most `Config.MaxWorkers` panes with IDs `ticket-coding-<ID>`, `Role: "coding-agent"`, `SameTabAsPaneID`, task/session/root, and command:

```go
[]string{roleBin, "role", "coding-agent", root}
```

Fake snapshots return `›` so startup remains deterministic.

- [ ] **Step 2: Define failing completion and false-positive tests**

Publish a `raw_output` event containing the full echoed prompt with the quoted marker and assert no transition or close occurs. Then publish an event with a standalone exact marker and assert the ordered calls are `done` then `close`; duplicate output must be ignored. A prefix, suffix, embedded marker, other pane, and other ticket ID must not complete the slot.

- [ ] **Step 3: Define failing retry, reconnect, and shutdown tests**

Cover render/create failure requeue, readiness/input failure close-before-requeue, completion database failure retaining the pane, close failure with a present pane retaining capacity, close failure with an absent pane releasing capacity, stream loss pausing claims, reconnect snapshot marker recovery, and cancellation close/requeue cleanup.

- [ ] **Step 4: Run manager tests to verify failure**

Run: `go test ./internal/ticketworker -run '^TestManager' -count=1`

Expected: FAIL because manager types do not exist.

- [ ] **Step 5: Implement manager interfaces and state**

Use these exact interface shapes:

```go
type ManagerStore interface {
    Next(context.Context) (Ticket, error)
    Transition(context.Context, int64, Action) (Ticket, error)
    Requeue(context.Context, int64) (Ticket, error)
}

type ManagerClient interface {
    CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
    SendInput(context.Context, string, transport.SendInputRequest) error
    SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
    ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
    InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
    StreamEvents(context.Context) (*transport.EventStream, error)
}

type ManagerOptions struct {
    Store ManagerStore
    Client ManagerClient
    Config Config
    Root string
    TaskID string
    AnchorPaneID string
    ZellijSession string
    RoleBin string
    StartupTimeout time.Duration
    PollInterval time.Duration
    ReadyPollInterval time.Duration
    Log io.Writer
}
```

`PollInterval` overrides config only for deterministic tests; zero uses `Config.PollInterval`. `ReadyPollInterval` defaults to `50ms`.

- [ ] **Step 6: Implement startup, stream ownership, fill, and readiness input**

`Run` waits for the matching anchor, calls `StreamEvents`, creates `max_workers` slots, fills synchronously, and enters one serialized select loop over context, poll ticks, stream events, and stream errors. A slot follows `empty → starting → working`; failure cleanup may move it to `cleanup_failed`. No claim occurs while the stream is disconnected.

- [ ] **Step 7: Implement exact completion, retries, reconnect recovery, and shutdown**

Scan trimmed output lines and compare equality with the slot marker. Successful completion follows `working → completing → closing → empty`, with `Transition(ActionDone)` before `ClosePane`. On ticks, retry completing/closing/cleanup states, reconnect a lost stream, snapshot all workers after reconnect, and refill only when connected. Shutdown stops fill, closes active panes, and requeues unfinished tickets only after confirmed close/absence; aggregate unsafe cleanup errors.

- [ ] **Step 8: Format and verify Task 2**

Run: `gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go && go test ./internal/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 9: Commit Task 2**

```bash
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git commit -m "feat: manage ticket coding agent pool"
```

---

### Task 3: Ticket Manager Role Package

**Files:**
- Create: `cmd/agent-role/ticketmanager/ticketmanager.go`
- Create: `cmd/agent-role/ticketmanager/ticketmanager_test.go`

**Interfaces:**
- Consumes: `ticketworker.FindRoot`, `OpenExisting`, `LoadConfig`, `NewManager`, `transport.NewClient`, and `cli.ResolveZellijSession`.
- Produces: `ticketmanager.Run(args []string) int`.

- [ ] **Step 1: Write failing option and wiring tests**

Test exact usage `ticket-manager [options] <path>`, required `--task`, required `--anchor-pane`, one required path, positive `--startup-timeout`, role-bin default, socket default, and Zellij session flag/environment resolution. Inject a fake manager runner to assert resolved root, loaded config, opened store, runtime client options, and every `ManagerOptions` field without starting Codex or a daemon.

- [ ] **Step 2: Run role package tests to verify failure**

Run: `go test ./cmd/agent-role/ticketmanager -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement role parsing and dependency wiring**

Expose `Run(args []string) int`, keep parsing in the package, and use a `runWithDependencies(ctx, args, stdout, stderr, deps)` helper for tests. `Run` creates a signal-aware context for interrupt/SIGTERM. Open the store only after root/config validation, defer close, construct the transport client with the socket and default request timeout, construct the manager, and return nonzero with concise `Error: ...` diagnostics for setup or runtime failure.

- [ ] **Step 4: Format and verify Task 3**

Run: `gofmt -w cmd/agent-role/ticketmanager/ticketmanager.go cmd/agent-role/ticketmanager/ticketmanager_test.go && go test ./cmd/agent-role/ticketmanager -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add cmd/agent-role/ticketmanager/ticketmanager.go cmd/agent-role/ticketmanager/ticketmanager_test.go
git commit -m "feat: add ticket manager role runner"
```

---

### Task 4: Role Catalog, Dispatch, and Documentation

**Files:**
- Modify: `internal/roles/roles.go`
- Modify: `internal/roles/roles_test.go`
- Modify: `internal/cli/role/role.go`
- Modify: `internal/cli/role/role_test.go`
- Modify or create: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

**Interfaces:**
- Consumes: `ticketmanager.Run(args []string) int`.
- Produces: catalog and CLI access to `ticket-manager`.

- [ ] **Step 1: Write failing catalog and dispatch tests**

Require `Lookup(RoleTicketManager)` with usage `ticket-manager [options] <path>`, a short manager description, required `path`, `--task`, and `--anchor-pane`, plus optional `--socket`, `--zellij-session`, `--role-bin`, and `--startup-timeout`. Add a dispatcher test that invokes `ticket-manager` against an initialized temporary repository with injected-invalid runtime identity and confirms role-specific validation output rather than unknown-role output.

- [ ] **Step 2: Run focused role tests to verify failure**

Run: `go test ./internal/roles ./internal/cli/role -run 'Test.*TicketManager' -count=1`

Expected: FAIL because catalog metadata and dispatch do not exist.

- [ ] **Step 3: Add catalog metadata and direct dispatch**

Add `RoleTicketManager = "ticket-manager"`, its `RoleSpec`, import `cmd/agent-role/ticketmanager`, and dispatch with:

```go
case roles.RoleTicketManager:
    return ticketmanager.Run(args[1:])
```

Do not add planner code or dispatcher wrapper helpers.

- [ ] **Step 4: Format and run all focused role tests**

Run: `gofmt -w internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go && go test ./internal/roles ./internal/cli/role ./cmd/agent-role/ticketmanager ./internal/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 5: Build the role binary and update external role documentation**

Run `go build -o bin/agent-role ./cmd/agent-role` and `./bin/agent-role roles`. Create or update `/Users/in05908_mac/.config/pi/docs/agent-roles.md` with the exact usage, purpose, required/optional arguments, project DB/config paths, agentd/Zellij/Codex requirements, pool behavior, and exact completion marker contract.

- [ ] **Step 6: Commit repository role wiring**

```bash
git add internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go
git commit -m "feat: register ticket manager role"
```

The external summary file is verified separately because it is outside this repository.

---

### Task 5: Full Verification and Unified Binary Registration

**Files:**
- Verify: all Go packages and role documentation
- Build: `bin/agent-role`, `bin/zellij-agent`
- Register: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: completed Tasks 1–4.
- Produces: verified role binaries and the atomically registered unified CLI.

- [ ] **Step 1: Run complete tests and static diff checks**

Run:

```bash
set -euo pipefail
go test ./...
git diff --check
```

Expected: every package passes and diff check emits no errors.

- [ ] **Step 2: Build and inspect the compatibility role binary**

Run:

```bash
set -euo pipefail
go build -o bin/agent-role ./cmd/agent-role
./bin/agent-role roles | rg '^ticket-manager'
```

Expected: build succeeds and the role listing contains its exact usage.

- [ ] **Step 3: Verify external role summary**

Run: `rg -n 'ticket-manager|--task|--anchor-pane|ZELLIJ_AGENT_TICKET_DONE' /Users/in05908_mac/.config/pi/docs/agent-roles.md`

Expected: every contract term appears.

- [ ] **Step 4: Build and atomically register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
cmp bin/zellij-agent ~/.config/custom-cli/zellij-agent
```

Expected: every command exits zero and `cmp` emits no output.

- [ ] **Step 5: Verify the unified role listing and clean worktree**

Run:

```bash
set -euo pipefail
~/.config/custom-cli/zellij-agent role roles | rg '^ticket-manager'
git status --short
```

Expected: installed listing contains the role and the repository worktree is clean.
