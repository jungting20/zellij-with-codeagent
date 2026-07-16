# Ticket Worker Race Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ticket-worker startup wait for its registered anchor and make completed-worker close handling recover when the worker record has already disappeared.

**Architecture:** The manager uses the existing runtime inspection API as its source of truth. It waits for an anchor matching logical ID, task, physical session, and active status before filling slots; after an exact completion marker, a failed close is considered complete only when runtime inspection confirms the worker record is absent.

**Tech Stack:** Go, standard `context`/`time`/`testing`, existing Unix-socket transport client and runtime inspection types

## Global Constraints

- Keep the fix inside ticket-worker manager behavior; do not make general runtime close or `ctl` close idempotent.
- Worker commands may exit immediately after printing the exact configured completion marker.
- Anchor identity requires matching pane ID, task ID, physical Zellij session, and `starting` or `running` status.
- A close error releases capacity only after a valid marker and confirmed runtime-record absence.
- A present worker or failed post-close inspection preserves the occupied slot.
- Keep logical task/session and physical Zellij session semantics unchanged.
- After implementation, run `go test ./...`, build `bin/zellij-agent`, immediately copy it to `~/.config/custom-cli`, and compare both artifacts.

## File Structure

- `internal/ticketworker/manager.go`: readiness polling, runtime identity matching, close reconciliation, and slot-completion helper.
- `internal/ticketworker/manager_test.go`: deterministic inspection responses and race regression tests.
- `internal/cli/ticketworker/ticketworker.go`: pass manager `--timeout` into startup readiness.
- `internal/cli/ticketworker/ticketworker_test.go`: verify timeout wiring.
- `README.md`: document anchor readiness and immediate-exit worker support.

---

### Task 1: Manager Readiness and Completed-Worker Reconciliation

**Files:**
- Modify: `internal/ticketworker/manager.go`
- Modify: `internal/ticketworker/manager_test.go`

**Interfaces:**
- Consumes: `transport.Client.InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)`.
- Produces: `ManagerClient.InspectRuntime`, `ManagerOptions.StartupTimeout`, startup anchor wait, and completed-slot reconciliation.

- [ ] **Step 1: Extend the fake client and write failing startup tests**

Add queued inspection responses/errors, inspection call recording, and a default valid anchor to `fakeManagerClient`. Use this exact pane:

```go
transport.Pane{
	ID:        "ticket-worker-manager",
	TaskID:    "tickets",
	SessionID: "physical-a",
	Status:    "running",
}
```

Add these tests:

```go
func TestManagerWaitsForRegisteredAnchorBeforeInitialFill(t *testing.T)
func TestManagerRejectsWrongTaskOrSessionAsAnchor(t *testing.T)
func TestManagerAnchorReadinessTimeoutCreatesNoWorkers(t *testing.T)
func TestManagerAnchorReadinessCancellationCreatesNoWorkers(t *testing.T)
```

The first confirms zero creates while inspection has no anchor, releases a matching response, then observes initial capacity. The identity test returns same-ID panes with a wrong task and wrong session before returning the valid pane.

- [ ] **Step 2: Run startup tests and verify they fail**

Run: `go test ./internal/ticketworker -run 'TestManager.*Anchor' -count=1`

Expected: FAIL because the manager cannot inspect runtime and fills slots immediately.

- [ ] **Step 3: Implement bounded anchor readiness**

Extend interfaces and options:

```go
type ManagerClient interface {
	CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
	WaitForOutputMarker(context.Context, string, transport.WaitForOutputMarkerRequest) (transport.WaitForOutputMarkerResponse, error)
	ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
}

type ManagerOptions struct {
	Client         ManagerClient
	Config         Config
	TaskID         string
	AnchorPaneID   string
	CWD            string
	ZellijSession  string
	StartupTimeout time.Duration
	Tick           <-chan time.Time
	Now            func() time.Time
	Log            io.Writer
}
```

Default zero timeout to 15 seconds and reject negative values. Poll every 50 milliseconds. Implement `waitForAnchor(ctx)` with a timeout context, retry inspection errors, match all identity/status requirements, and return an anchor-not-ready error joined with the last inspection error when present. Call it before the first `fillEmptySlots`.

- [ ] **Step 4: Run startup tests to verify they pass**

Run: `gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go && go test ./internal/ticketworker -run 'TestManager.*Anchor' -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing close reconciliation tests**

Add:

```go
func TestManagerCloseNotFoundWithAbsentWorkerReleasesSlot(t *testing.T)
func TestManagerCloseRuntimeErrorWithAbsentWorkerReleasesSlot(t *testing.T)
func TestManagerCloseFailureWithPresentWorkerPreservesCapacity(t *testing.T)
func TestManagerCloseFailureWithInspectionFailurePreservesCapacity(t *testing.T)
```

For absent cases, return `transport.ClientError` values using `CodeNotFound` and `CodeRuntimeError`, omit the worker from inspection, send the matching marker, tick, and assert replacement creation. For present, include exact worker ID/task/session and assert no refill. For inspection failure, return an error and assert no refill.

- [ ] **Step 6: Run close reconciliation tests and verify they fail**

Run: `go test ./internal/ticketworker -run 'TestManagerClose.*(Absent|Present|Inspection)' -count=1`

Expected: FAIL because close errors preserve occupied slots without runtime inspection.

- [ ] **Step 7: Implement completed-worker reconciliation**

Add:

```go
func (m *Manager) runtimeHasPane(response transport.InspectRuntimeResponse, paneID string, statuses ...string) bool
func (m *Manager) completeSlot(slot *workerSlot, matchedAt time.Time)
```

`runtimeHasPane` requires pane ID, task ID, and physical session; optional statuses further constrain it. After a valid marker, close normally. On close error, inspect runtime. If inspection succeeds and the worker is absent, log `already closed` and call `completeSlot`. If present or inspection fails, retain the occupied slot and original close error. Normal close success uses the same helper.

- [ ] **Step 8: Run all manager tests**

Run: `gofmt -w internal/ticketworker && go test ./internal/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 9: Commit manager behavior**

```bash
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git commit -m "fix: recover ticket worker lifecycle races"
```

### Task 2: CLI Wiring, Documentation, Verification, and Registration

**Files:**
- Modify: `internal/cli/ticketworker/ticketworker.go`
- Modify: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `README.md`
- Test: all Go packages
- Build: `bin/zellij-agent`
- Register: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: `ticketworker.ManagerOptions.StartupTimeout` from Task 1.
- Produces: manager `--timeout` controls transport calls and startup readiness.

- [ ] **Step 1: Write a failing timeout-wiring test**

Add:

```go
func TestRunManagerPassesTimeoutToStartupReadiness(t *testing.T)
```

Invoke manager with `--timeout 3s`, capture options, and assert:

```go
if gotOptions.StartupTimeout != 3*time.Second {
	t.Fatalf("startup timeout = %s, want 3s", gotOptions.StartupTimeout)
}
```

- [ ] **Step 2: Run the CLI test and verify it fails**

Run: `go test ./internal/cli/ticketworker -run '^TestRunManagerPassesTimeoutToStartupReadiness$' -count=1`

Expected: FAIL because `runManager` does not populate `StartupTimeout`.

- [ ] **Step 3: Wire timeout into manager options**

Set `StartupTimeout: *timeout` in the `ticketworker.ManagerOptions` created by `runManager`. Keep request timeout and validation unchanged.

- [ ] **Step 4: Document lifecycle recovery**

Update the ticket-worker README section: manager waits for its registered anchor before workers; a worker may print the exact marker and exit immediately; an already-absent runtime record releases the slot.

- [ ] **Step 5: Run focused and full verification**

Run these commands separately:

```bash
gofmt -w internal/cli/ticketworker internal/ticketworker
git diff --check
go test ./internal/cli/ticketworker ./internal/ticketworker -count=1
go test ./...
```

Expected: every command exits 0 and all Go packages pass.

- [ ] **Step 6: Build and immediately register the binary**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent && cp bin/zellij-agent ~/.config/custom-cli
cmp bin/zellij-agent ~/.config/custom-cli/zellij-agent
~/.config/custom-cli/zellij-agent --help >/dev/null
```

Expected: exit 0 for build, copy, comparison, and help.

- [ ] **Step 7: Commit CLI and documentation**

```bash
git add internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go README.md
git commit -m "docs: explain ticket worker race recovery"
```

- [ ] **Step 8: Confirm final state**

Run: `git status --short && git log -4 --oneline`

Expected: clean tracked worktree with both task commits above the design and plan commits.
