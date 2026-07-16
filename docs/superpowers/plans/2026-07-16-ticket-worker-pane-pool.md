# Ticket Worker Pane Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `zellij-agent ticket-worker init|start`, a deterministic bounded worker-pane manager, exact per-pane completion-marker watching, and a task-filtered read-only monitoring pane.

**Architecture:** A project-local YAML file supplies one worker command, capacity, polling interval, and a fixed marker. `ticket-worker start` submits a two-pane execution plan for a long-running manager and a filtered dashboard; the manager uses the local transport to create workers in its own managed tab, wait for exact marker lines, close completed workers, and refill slots only on polling ticks. Ticket claiming and implementation remain entirely inside the project command.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, Unix-socket JSON HTTP, Bubble Tea, existing `RuntimeService`/registry/Zellij backend.

## Global Constraints

- `max_workers` defaults to `3` and must be positive.
- `poll_interval` defaults to `30s` and must be positive.
- `worker.command` is a non-empty argv vector executed directly from the project root, without a shell.
- `worker.completion_marker` is a non-empty single line; only trimmed exact-line equality completes a worker.
- Worker pane IDs are never reused; use slot number plus monotonically increasing launch sequence.
- Canceling the manager must not close existing workers.
- A marker-less exit, unchanged output, or silence is not completion.
- Restart recovery, stalled-worker policy, and failed/waiting policy remain out of scope.
- Planners, clients, the manager, and the dashboard must not invoke Zellij directly; all pane mutations go through `RuntimeService` or local transport wrappers.
- After the final unified-binary rebuild, immediately run `cp bin/zellij-agent ~/.config/custom-cli`.

---

## File and Interface Map

- Create `internal/ticketworker/config.go`: strict version-1 YAML loading, defaults, validation, and safe initialization.
- Create `internal/ticketworker/config_test.go`: configuration and scaffolding behavior.
- Create `internal/ticketworker/manager.go`: single-owner slot event loop and transport-facing client interface.
- Create `internal/ticketworker/manager_test.go`: capacity, marker, close, retry, and cancellation tests.
- Create `internal/ticketworker/plan.go`: initial manager/monitor execution-plan construction and stable IDs.
- Create `internal/ticketworker/plan_test.go`: plan command, task, cwd, and pane assertions.
- Create `internal/runtime/marker_watch.go` and `marker_watch_test.go`: pane-scoped exact marker wait.
- Modify `internal/runtime/types.go`, `service.go`, and `service_test.go`: logical same-tab anchors and marker service interface.
- Modify `internal/transport/types.go`, `client.go`, `handlers_panes.go`, `server.go`, `client_test.go`, and `server_test.go`: same-tab JSON, blocking marker wait, and logical single-pane close.
- Modify `internal/dashboard/model.go`, `actions.go`, `view.go`, and dashboard tests: task filter, optional capacity, and read-only action gate.
- Create `internal/cli/ticketworker/ticketworker.go` and `_test.go`: `init`, `start`, and implementation-private `manager` dispatch.
- Modify `internal/cli/dashboard/dashboard.go` and `_test.go`: `--task`, `--read-only`, and `--capacity` flags.
- Modify `cmd/zellij-agent/main.go` and `main_test.go`: unified command dispatch and help.
- Modify `README.md`: user-facing setup, marker contract, shutdown behavior, and limitations.

## Task 1: Project Configuration and Safe Initialization

**Files:**
- Create: `internal/ticketworker/config.go`
- Create: `internal/ticketworker/config_test.go`

**Interfaces:**
- Produces: `type Config`, `type WorkerConfig`, `func ConfigPath(string) string`, `func LoadConfig(string) (Config, error)`, and `func InitConfig(string, bool) (string, error)`.
- Consumes: `gopkg.in/yaml.v3`, already present in `go.mod`.

- [ ] **Step 1: Write failing strict-config and initialization tests**

```go
func TestLoadConfigAppliesDefaults(t *testing.T) {
    root := t.TempDir()
    writeConfig(t, root, "version: 1\nworker:\n  command: [go, run, ./cmd/ticket-worker]\n  completion_marker: ZELLIJ_AGENT_WORKER_DONE\n")
    got, err := LoadConfig(root)
    if err != nil { t.Fatal(err) }
    if got.MaxWorkers != 3 || got.PollInterval != 30*time.Second {
        t.Fatalf("defaults = %d %s", got.MaxWorkers, got.PollInterval)
    }
}

func TestLoadConfigRejectsUnknownFieldAndMultilineMarker(t *testing.T) {
    for name, body := range map[string]string{
        "unknown": "version: 1\nextra: true\nworker:\n  command: [worker]\n  completion_marker: DONE\n",
        "marker": "version: 1\nworker:\n  command: [worker]\n  completion_marker: 'DONE\\nAGAIN'\n",
    } {
        t.Run(name, func(t *testing.T) {
            root := t.TempDir(); writeConfig(t, root, body)
            if _, err := LoadConfig(root); err == nil { t.Fatal("expected validation error") }
        })
    }
}

func TestInitConfigRefusesOverwriteWithoutForce(t *testing.T) {
    root := t.TempDir()
    path, err := InitConfig(root, false)
    if err != nil { t.Fatal(err) }
    if _, err := os.Stat(path); err != nil { t.Fatal(err) }
    if _, err := InitConfig(root, false); !errors.Is(err, fs.ErrExist) {
        t.Fatalf("second init error = %v, want fs.ErrExist", err)
    }
}
```

- [ ] **Step 2: Run the tests and verify the package does not exist yet**

Run: `go test ./internal/ticketworker -run 'Test(LoadConfig|InitConfig)' -v`

Expected: FAIL because `LoadConfig` and `InitConfig` are undefined.

- [ ] **Step 3: Implement strict loading, defaults, and atomic scaffolding**

```go
const (
    configVersion = 1
    defaultMaxWorkers = 3
    defaultPollInterval = 30 * time.Second
)

type WorkerConfig struct {
    Command []string `yaml:"command"`
    CompletionMarker string `yaml:"completion_marker"`
}

type Config struct {
    Version int
    MaxWorkers int
    PollInterval time.Duration
    Worker WorkerConfig
}

type diskConfig struct {
    Version int `yaml:"version"`
    MaxWorkers int `yaml:"max_workers"`
    PollInterval string `yaml:"poll_interval"`
    Worker WorkerConfig `yaml:"worker"`
}

func ConfigPath(root string) string {
    return filepath.Join(root, ".zellij-agent", "worker", "config.yaml")
}

func LoadConfig(root string) (Config, error) {
    file, err := os.Open(ConfigPath(root)); if err != nil { return Config{}, err }
    defer file.Close()
    var disk diskConfig
    dec := yaml.NewDecoder(file); dec.KnownFields(true)
    if err := dec.Decode(&disk); err != nil { return Config{}, fmt.Errorf("decode ticket-worker config: %w", err) }
    cfg := Config{Version: disk.Version, MaxWorkers: disk.MaxWorkers, Worker: disk.Worker}
    if cfg.MaxWorkers == 0 { cfg.MaxWorkers = defaultMaxWorkers }
    if disk.PollInterval == "" { cfg.PollInterval = defaultPollInterval } else {
        cfg.PollInterval, err = time.ParseDuration(disk.PollInterval)
        if err != nil { return Config{}, fmt.Errorf("poll_interval: %w", err) }
    }
    return cfg, validateConfig(cfg)
}
```

Implement `validateConfig` with exact checks for version `1`, positive capacity/duration, non-empty trimmed argv elements, a marker equal to its trimmed form, and `\r`/`\n` rejection. Implement `InitConfig` with `os.MkdirAll(..., 0o755)` and `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)`; for `force`, write a temporary sibling and `os.Rename` it over the target. Use this exact template:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
worker:
  command: ["go", "run", "./cmd/ticket-worker"]
  completion_marker: "ZELLIJ_AGENT_WORKER_DONE"
```

- [ ] **Step 4: Run configuration tests**

Run: `go test ./internal/ticketworker -run 'Test(LoadConfig|InitConfig)' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ticketworker/config.go internal/ticketworker/config_test.go
git commit -m "feat: add ticket worker project config"
```

## Task 2: Logical Same-tab Pane Creation

**Files:**
- Modify: `internal/runtime/types.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/server_test.go`

**Interfaces:**
- Produces: `CreatePaneRequest.SameTabAsPaneID PaneID` in runtime and `SameTabAsPaneID string` with JSON key `same_tab_as_pane_id` in transport.
- Consumes: the existing registry record's logical pane status and `ZellijTabID`.

- [ ] **Step 1: Write failing runtime tests for logical anchor resolution**

Add tests that register an active anchor with Zellij tab ID `7`, create a worker using only `SameTabAsPaneID: "manager"`, and assert the fake backend receives tab `7`. Add table cases for a missing anchor, a closed anchor, an anchor without `ZellijTabID`, and `NewTab` combined with `SameTabAsPaneID`; assert no backend create call.

```go
response, err := service.CreatePane(context.Background(), CreatePaneRequest{
    ID: "worker-1", TaskID: "ticket-session", Role: "ticket-worker",
    SameTabAsPaneID: "manager", Command: []string{"worker"}, CWD: "/repo",
})
if err != nil { t.Fatal(err) }
if response.Pane.ZellijTabID == nil || *response.Pane.ZellijTabID != "7" { t.Fatalf("pane = %#v", response.Pane) }
```

- [ ] **Step 2: Run the focused runtime test**

Run: `go test ./internal/runtime -run 'TestCreatePaneSameTabAs' -v`

Expected: FAIL because the request field and resolution do not exist.

- [ ] **Step 3: Add the logical target field and resolve it inside `RuntimeService`**

Add `ErrInvalidPaneTarget = errors.New("runtime: invalid pane target")`. Before `createBackendPane`, enforce mutual exclusion with `NewTab` and explicit `ZellijTabID`. Resolve with `s.lookupPane`, require status `starting` or `running`, require non-nil `ZellijTabID`, clone that value into the request's internal `ZellijTabID`, and never fall back to the daemon's current tab.

```go
func (s *Service) resolveCreatePaneTarget(req CreatePaneRequest) (CreatePaneRequest, error) {
    if req.SameTabAsPaneID == "" { return req, nil }
    if req.NewTab || req.ZellijTabID != nil { return req, ErrInvalidPaneTarget }
    anchor, err := s.lookupPane(req.SameTabAsPaneID); if err != nil { return req, err }
    if anchor.Status != registry.PaneStatusStarting && anchor.Status != registry.PaneStatusRunning {
        return req, fmt.Errorf("%w: anchor %s is %s", ErrInvalidPaneTarget, anchor.ID, anchor.Status)
    }
    if anchor.ZellijTabID == nil { return req, fmt.Errorf("%w: anchor %s has no tab", ErrInvalidPaneTarget, anchor.ID) }
    tabID := ZellijTabID(*anchor.ZellijTabID); req.ZellijTabID = &tabID
    return req, nil
}
```

- [ ] **Step 4: Add transport JSON mapping and server coverage**

Extend `transport.CreatePaneRequest`, `ToRuntime`, and `RuntimeCreatePaneRequest`. Update `TestServerCreatePane` to submit `"same_tab_as_pane_id":"manager"` and assert `service.createReq.SameTabAsPaneID == "manager"`.

- [ ] **Step 5: Run runtime and transport tests**

Run: `go test ./internal/runtime ./internal/transport`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/types.go internal/runtime/service.go internal/runtime/service_test.go internal/transport/types.go internal/transport/server_test.go
git commit -m "feat: create panes beside logical anchors"
```

## Task 3: Exact Marker Wait and Logical Single-pane Close Transport

**Files:**
- Create: `internal/runtime/marker_watch.go`
- Create: `internal/runtime/marker_watch_test.go`
- Modify: `internal/runtime/types.go`
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/handlers_panes.go`
- Modify: `internal/transport/client.go`
- Modify: `internal/transport/client_test.go`
- Modify: `internal/transport/server_test.go`

**Interfaces:**
- Produces runtime `WaitForOutputMarker(context.Context, WaitForOutputMarkerRequest) (WaitForOutputMarkerResponse, error)`.
- Produces transport client methods `WaitForOutputMarker(context.Context, string, WaitForOutputMarkerRequest) (WaitForOutputMarkerResponse, error)` and `ClosePane(context.Context, string) (ClosePaneResponse, error)`.

- [ ] **Step 1: Write failing exact-marker runtime tests**

Test exact standalone line matching, trimmed whitespace, substring rejection, wrong-pane rejection, cancellation, empty/multiline marker rejection, and an already-visible marker in `InspectPane(...).Pane.LastOutput`.

```go
result := make(chan error, 1)
go func() {
    _, err := service.WaitForOutputMarker(ctx, WaitForOutputMarkerRequest{PaneID: "worker-1", Marker: "DONE"})
    result <- err
}()
bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-2", Message: "DONE"})
bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-1", Message: "NOT_DONE"})
bus.Publish(eventbus.Event{Type: eventbus.TypeRawOutput, PaneID: "worker-1", Message: "log\n  DONE  \n"})
if err := <-result; err != nil { t.Fatal(err) }
```

- [ ] **Step 2: Run marker tests to verify failure**

Run: `go test ./internal/runtime -run 'TestWaitForOutputMarker' -v`

Expected: FAIL because the marker service is undefined.

- [ ] **Step 3: Implement the bounded event-based marker waiter**

Subscribe before inspecting the pane so events emitted during the initial check are queued. Validate the marker as one non-empty line, scan `LastOutput`, then accept only `raw_output` events for the requested logical pane. `containsExactLine` uses `bufio.Scanner` and never stores prior frames.

Add these runtime types and include the method in `PaneService`:

```go
type WaitForOutputMarkerRequest struct { PaneID PaneID; Marker string }
type WaitForOutputMarkerResponse struct { PaneID PaneID; Marker string; MatchedAt time.Time }
```

```go
func containsExactLine(text, marker string) bool {
    scanner := bufio.NewScanner(strings.NewReader(text))
    for scanner.Scan() {
        if strings.TrimSpace(scanner.Text()) == marker { return true }
    }
    return false
}
```

Return `MatchedAt` from the event timestamp, or `time.Now()` for an already-visible marker. Cancellation returns `ctx.Err()` and always unsubscribes.

- [ ] **Step 4: Add blocking transport routes and client methods**

Add pane actions:

```text
POST /v1/panes/{pane_id}/wait-marker  {"marker":"ZELLIJ_AGENT_WORKER_DONE"}
POST /v1/panes/{pane_id}/close        {}
```

The wait handler must use `r.Context()` rather than the server's 30-second request timeout. The client wait method must clone its HTTP client with `Timeout = 0`, matching `StreamEvents`. Close calls the existing runtime `ClosePane` and returns the mapped pane.

Define the wire types explicitly:

```go
type WaitForOutputMarkerRequest struct { Marker string `json:"marker"` }
type WaitForOutputMarkerResponse struct {
    PaneID string `json:"pane_id"`
    Marker string `json:"marker"`
    MatchedAt time.Time `json:"matched_at"`
}
type ClosePaneResponse struct { Pane Pane `json:"pane"` }
```

- [ ] **Step 5: Add server/client tests for match, cancellation, and close**

Extend the fake runtime service with captured marker/close requests. Assert JSON mapping, 200 responses, request-context cancellation, and structured 404 errors for missing panes.

- [ ] **Step 6: Run focused packages**

Run: `go test ./internal/runtime ./internal/transport`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/runtime/marker_watch.go internal/runtime/marker_watch_test.go internal/runtime/types.go internal/transport/types.go internal/transport/handlers_panes.go internal/transport/client.go internal/transport/client_test.go internal/transport/server_test.go
git commit -m "feat: wait for exact pane output markers"
```

## Task 4: Deterministic Worker Pool Manager

**Files:**
- Create: `internal/ticketworker/manager.go`
- Create: `internal/ticketworker/manager_test.go`

**Interfaces:**
- Consumes: `transport.CreatePaneRequest`, `WaitForOutputMarker`, and `ClosePane` from Tasks 2-3.
- Produces: `func NewManager(ManagerOptions) (*Manager, error)` and `func (*Manager) Run(context.Context) error`.

- [ ] **Step 1: Write a fake-client manager test suite**

Define a fake that records creates/closes, provides a controllable marker channel per pane, and never calls Zellij. Cover initial fill, unique IDs, no refill before a tick, refill after successful close, create retry, close failure capacity preservation, watch failure retention, and cancellation without close.

```go
func TestManagerCompletesAndRefillsOnNextTick(t *testing.T) {
    client := newFakeManagerClient()
    ticks := make(chan time.Time, 1)
    manager, err := NewManager(ManagerOptions{
        Client: client, Config: Config{MaxWorkers: 2, PollInterval: time.Second,
            Worker: WorkerConfig{Command: []string{"worker"}, CompletionMarker: "DONE"}},
        TaskID: "tickets", AnchorPaneID: "ticket-worker-manager", CWD: "/repo",
        Tick: ticks, Now: func() time.Time { return time.Unix(100, 0) },
    })
    if err != nil { t.Fatal(err) }
    ctx, cancel := context.WithCancel(context.Background()); defer cancel()
    go manager.Run(ctx)
    client.waitForCreates(t, 2)
    client.match("ticket-worker-slot-1-0001")
    client.waitForCloses(t, 1)
    if client.createCount() != 2 { t.Fatal("refilled before tick") }
    ticks <- time.Unix(101, 0)
    client.waitForCreates(t, 3)
}
```

- [ ] **Step 2: Run manager tests and verify failure**

Run: `go test ./internal/ticketworker -run 'TestManager' -v`

Expected: FAIL because `Manager` is undefined.

- [ ] **Step 3: Implement one event-loop owner and per-pane wait goroutines**

Use these core types:

```go
type ManagerClient interface {
    CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
    WaitForOutputMarker(context.Context, string, transport.WaitForOutputMarkerRequest) (transport.WaitForOutputMarkerResponse, error)
    ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
}

type ManagerOptions struct {
    Client ManagerClient
    Config Config
    TaskID string
    AnchorPaneID string
    CWD string
    Tick <-chan time.Time
    Now func() time.Time
    Log io.Writer
}
```

`Run` immediately calls `fillEmptySlots`, then selects over ticks, typed watch results, and `ctx.Done()`. Only this loop changes slot state. `launchSlot` increments the slot sequence before calling `CreatePane`, uses ID `ticket-worker-slot-%d-%04d`, role `ticket-worker`, the configured argv/cwd, and `SameTabAsPaneID`. On a match, synchronously close that logical pane; only a successful close empties the slot. Write concise create/watch/close transitions and errors to `Log` (default `io.Discard` in tests and `os.Stdout` in the manager pane) so the monitoring dashboard can show manager diagnostics through the existing pane output. On cancellation, return `ctx.Err()` without any close calls.

- [ ] **Step 4: Run manager tests plus race detector**

Run: `go test -race ./internal/ticketworker -run 'TestManager' -v`

Expected: PASS with no races.

- [ ] **Step 5: Commit**

```bash
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git commit -m "feat: manage bounded ticket worker panes"
```

## Task 5: Task-filtered Read-only Dashboard

**Files:**
- Modify: `internal/dashboard/model.go`
- Modify: `internal/dashboard/actions.go`
- Modify: `internal/dashboard/view.go`
- Modify: `internal/dashboard/model_test.go`
- Modify: `internal/dashboard/view_test.go`
- Modify: `internal/cli/dashboard/dashboard.go`
- Modify: `internal/cli/dashboard/dashboard_test.go`

**Interfaces:**
- Produces: `dashboard.Options{TaskID string, ReadOnly bool, Capacity int}`.
- Produces CLI flags `--task`, `--read-only`, and `--capacity`.

- [ ] **Step 1: Write failing model tests for filtering and read-only keys**

Construct refresh results with ticket task `tickets-1` and unrelated task `work-2`. Assert only matching panes/events enter the model. Send `i`, `r`, and `x` key messages in read-only mode and assert the fake client records no input, reconcile, or cleanup calls. Keep navigation, snapshot, refresh, help, and quit available.

- [ ] **Step 2: Run focused dashboard tests**

Run: `go test ./internal/dashboard ./internal/cli/dashboard -run 'Test.*(TaskFilter|ReadOnly|Capacity)' -v`

Expected: FAIL because the options and flags do not exist.

- [ ] **Step 3: Filter refresh data and gate mutating actions**

Add helpers that return copied slices:

```go
func filterPanesByTask(panes []transport.Pane, taskID string) []transport.Pane
func filterEventsByTask(events []transport.Event, taskID string) []transport.Event
```

Apply them in `handleRefreshResult` before rebuilding rows. In `updateActionOrNormalKey`, when `ReadOnly` is true, consume `i`, `r`, and `x` with status text `read-only dashboard`; do not call action commands. Update header/footer/help to show `READ ONLY`, the task filter, and `active/capacity` when capacity is positive.

- [ ] **Step 4: Parse and forward dashboard flags**

Validate `--capacity >= 0`; pass all three values through `dashboard.Options`. Update help so read-only mode lists no mutation keys.

- [ ] **Step 5: Run dashboard tests**

Run: `go test ./internal/dashboard ./internal/cli/dashboard`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard internal/cli/dashboard
git commit -m "feat: add read-only task dashboard"
```

## Task 6: Workspace Plan and `ticket-worker` CLI

**Files:**
- Create: `internal/ticketworker/plan.go`
- Create: `internal/ticketworker/plan_test.go`
- Create: `internal/cli/ticketworker/ticketworker.go`
- Create: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `cmd/zellij-agent/main.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Produces: `ticketworker.BuildPlan(PlanRequest) (transport.ExecutionPlanPayload, error)`.
- Produces unified commands `zellij-agent ticket-worker init`, `start`, and implementation-private `manager`.

- [ ] **Step 1: Write failing plan tests**

Assert a two-pane plan with one task/session, absolute cwd/config path, manager ID `ticket-worker-manager`, monitor ID `ticket-worker-monitor`, and commands that call the same unified executable:

```text
zellij-agent ticket-worker manager --cwd /repo --config /repo/.zellij-agent/worker/config.yaml --task <session> --anchor ticket-worker-manager
zellij-agent dashboard --task <session> --read-only --capacity 3
```

The manager pane must be the first plan pane so it owns the stable tab anchor; manager startup must retry worker creation on polling ticks until its registry record is visible.

- [ ] **Step 2: Implement plan construction and collision-resistant session IDs**

Define `PlanRequest{CWD, ConfigPath, Session string, Executable []string, SocketPath string, Config Config}`. If `Session` is empty, derive `ticket-worker-<UTC timestamp>-<8 hex chars of SHA-256(cwd)>`. Set tab name `ticket-worker`, layout `triple-horizontal`, and both panes' task to the payload session through existing execution-plan mapping.

- [ ] **Step 3: Write failing CLI tests**

Cover `init`, `init --force`, `start --dry-run`, `start --max-workers`, missing/invalid config, successful submit, and internal manager cancellation. Inject filesystem cwd, clock, client factory, and manager factory through a `Config` dependency struct rather than spawning processes in unit tests.

- [ ] **Step 4: Implement CLI dispatch**

Use:

```go
type Config struct {
    Executable []string
    NewClient func(string, time.Duration) Client
    NewManager func(ticketworker.ManagerOptions) (Manager, error)
    Getwd func() (string, error)
    Now func() time.Time
}
```

`start` loads config, applies `--max-workers` only when explicitly set, validates everything before submission, and supports `--dry-run` for exact plan inspection. The internal `manager` command loads the same config, creates a transport client with auto-start disabled (the workspace daemon already exists), constructs the manager, and runs until SIGINT/SIGTERM. Return success for expected context cancellation; never cleanup workers.

- [ ] **Step 5: Wire unified dispatch and usage**

Add `case "ticket-worker"` in `cmd/zellij-agent/main.go`, pass `[]string{executablePath()}` as the executable prefix, reuse the auto-start client for `start`, and extend `printUsage`. Add dispatch/help tests in `cmd/zellij-agent/main_test.go`.

- [ ] **Step 6: Run CLI and unified-command tests**

Run: `go test ./internal/ticketworker ./internal/cli/ticketworker ./cmd/zellij-agent`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ticketworker/plan.go internal/ticketworker/plan_test.go internal/cli/ticketworker cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
git commit -m "feat: launch ticket worker workspaces"
```

## Task 7: Documentation, Full Verification, and Local Binary Registration

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: documented project setup and operational contract.

- [ ] **Step 1: Document the exact workflow and boundaries**

Add a `Ticket Worker Pool` section with the version-1 YAML, `init`, `start`, override example, exact marker contract, project-command responsibilities, manager cancellation behavior, and deferred restart recovery. Explicitly state that the project worker command must atomically claim its own ticket.

- [ ] **Step 2: Format and run the complete test suite**

Run: `gofmt -w` on every edited Go file listed by `git diff --name-only -- '*.go'`.

Run: `go test ./...`

Expected: PASS.

Run: `go test -race ./internal/ticketworker ./internal/runtime ./internal/transport ./internal/dashboard`

Expected: PASS with no races.

- [ ] **Step 3: Build and immediately register the unified binary**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent`

Expected: exit code 0 and `bin/zellij-agent` exists.

Run immediately: `cp bin/zellij-agent ~/.config/custom-cli`

Expected: exit code 0.

- [ ] **Step 4: Run a dry-run smoke check**

In a temporary fixture project, run `zellij-agent ticket-worker init`, replace the example command with `command: ["sh", "-lc", "printf '%s\\n' ZELLIJ_AGENT_WORKER_DONE"]` only for this manual smoke fixture, then run `zellij-agent ticket-worker start --dry-run`. Confirm the envelope contains exactly the manager and monitor bootstrap panes and no worker pane before manager startup.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: explain ticket worker pane pool"
```

## Final Review Checklist

- [ ] Compare every goal and non-goal in `docs/superpowers/specs/2026-07-16-ticket-worker-pane-pool-design.md` with the completed tasks.
- [ ] Confirm no project or client package calls Zellij directly.
- [ ] Confirm manager cancellation performs zero close/cleanup calls.
- [ ] Confirm exact marker matching is scoped to logical pane ID.
- [ ] Confirm the manager cannot exceed configured capacity after create or close failures.
- [ ] Confirm `git status --short` contains no unintended files.
