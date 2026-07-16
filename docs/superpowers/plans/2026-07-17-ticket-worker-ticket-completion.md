# Ticket Worker Ticket Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run a project-configured ticket completion command with the ticket ID reported by a coding-agent worker, then close the worker pane only after command success.

**Architecture:** Extend the generic marker watch with opt-in prefix matching that returns the actual line while preserving exact matching. Add backward-compatible version-1 config fields and a shell-free runner, then extend the manager event loop with completing/failed states and manual-close reconciliation.

**Tech Stack:** Go, standard `context`, `os/exec`, `regexp`, `testing`, YAML v3, existing runtime and JSON-over-Unix-socket transport.

## Global Constraints

- Route all pane operations through `RuntimeService` or local transport; never invoke Zellij from ticket-worker code.
- Execute `complete_command` as argv without a shell, from the project root, with the ticket ID appended.
- Accept ticket IDs matching `[A-Za-z0-9][A-Za-z0-9._:-]*` only.
- Never retry a failed completion command automatically and never close its pane.
- Preserve exact-marker behavior when `complete_command` is absent.
- Run `gofmt`, `go test ./...`, rebuild `bin/zellij-agent`, and immediately copy it to `~/.config/custom-cli`.

---

### Task 1: Prefix Marker Watch

**Files:**
- Modify: `internal/runtime/types.go`
- Modify: `internal/runtime/marker_watch.go`
- Modify: `internal/runtime/marker_watch_test.go`
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/handlers_panes.go`
- Modify: `internal/transport/client_test.go`
- Modify: `internal/transport/server_test.go`

**Interfaces:**
- Produces: `WaitForOutputMarkerRequest.MatchPrefix bool`.
- Produces: `WaitForOutputMarkerResponse.MatchedLine string`.
- Preserves: exact standalone-line matching, `Marker`, and `MatchedAt`.

- [ ] **Step 1: Write failing runtime tests**

Add a prefix test using:

```go
WaitForOutputMarkerRequest{
    PaneID: "worker-1", Marker: "ZELLIJ_AGENT_WORKER_DONE ", MatchPrefix: true,
}
```

Publish unrelated lines, then `ZELLIJ_AGENT_WORKER_DONE ticket_id=TICKET-123`. Assert `MatchedLine` contains that complete trimmed line. Extend exact tests to assert `MatchedLine == "DONE"`.

- [ ] **Step 2: Verify runtime tests fail**

Run: `go test ./internal/runtime -run '^TestWaitForOutputMarker' -count=1`

Expected: compile failure for missing `MatchPrefix` and `MatchedLine`.

- [ ] **Step 3: Implement runtime prefix matching**

Extend types:

```go
type WaitForOutputMarkerRequest struct {
    PaneID PaneID
    Marker string
    MatchPrefix bool
}
type WaitForOutputMarkerResponse struct {
    PaneID PaneID
    Marker string
    MatchedLine string
    MatchedAt time.Time
}
```

Replace boolean line detection with:

```go
func findMarkerLine(text, marker string, prefix bool) (string, bool) {
    scanner := bufio.NewScanner(strings.NewReader(text))
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if (!prefix && line == marker) || (prefix && strings.HasPrefix(line, marker)) {
            return line, true
        }
    }
    return "", false
}
```

Use the returned line for initial output and raw events.

- [ ] **Step 4: Write failing transport mapping tests**

Assert JSON `match_prefix: true` reaches runtime and `matched_line` returns through server and client fixtures.

- [ ] **Step 5: Implement transport mapping**

Add JSON fields:

```go
MatchPrefix bool `json:"match_prefix,omitempty"`
MatchedLine string `json:"matched_line,omitempty"`
```

Map them in `handlers_panes.go`.

- [ ] **Step 6: Format, test, and commit**

Run: `gofmt -w internal/runtime internal/transport`

Run: `go test ./internal/runtime ./internal/transport -run 'Test.*WaitForOutputMarker' -count=1`

Expected: PASS.

Commit: `git add internal/runtime internal/transport && git commit -m "feat: return structured marker lines"`

---

### Task 2: Completion Config and Init Template

**Files:**
- Modify: `internal/ticketworker/config.go`
- Modify: `internal/ticketworker/config_test.go`

**Interfaces:**
- Produces: `WorkerConfig.CompleteCommand []string`.
- Produces: `WorkerConfig.CompleteTimeout time.Duration`, default 30 seconds.

- [ ] **Step 1: Write failing config and init tests**

Test explicit `complete_command: [ticket, complete]` and `complete_timeout: 45s`, omitted timeout defaulting to 30 seconds, empty/blank command rejection, and malformed/non-positive timeout rejection. Expect the init template:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
worker:
  command: ["go", "run", "./cmd/ticket-worker"]
  completion_marker: "ZELLIJ_AGENT_WORKER_DONE"
  complete_command: ["ticket", "complete"]
  complete_timeout: 30s
```

Reload the initialized file and assert the values; repeat after `--force` replacement.

- [ ] **Step 2: Verify config tests fail**

Run: `go test ./internal/ticketworker -run 'Test(LoadConfig|InitConfig)' -count=1`

Expected: missing fields/template assertion failure.

- [ ] **Step 3: Implement config fields and validation**

Use separate disk/runtime worker types:

```go
type WorkerConfig struct {
    Command []string
    CompletionMarker string
    CompleteCommand []string
    CompleteTimeout time.Duration
}
type diskWorkerConfig struct {
    Command []string `yaml:"command"`
    CompletionMarker string `yaml:"completion_marker"`
    CompleteCommand *[]string `yaml:"complete_command"`
    CompleteTimeout string `yaml:"complete_timeout"`
}
```

The pointer distinguishes omitted command from explicit `[]`. Parse durations, apply `30*time.Second`, validate non-empty trimmed argv elements, and update the active init template while keeping version 1.

- [ ] **Step 4: Format, test, and commit**

Run: `gofmt -w internal/ticketworker/config.go internal/ticketworker/config_test.go`

Run: `go test ./internal/ticketworker -run 'Test(LoadConfig|InitConfig)' -count=1`

Expected: PASS.

Commit: `git add internal/ticketworker/config.go internal/ticketworker/config_test.go && git commit -m "feat: configure ticket completion commands"`

---

### Task 3: Completion Parser and Runner

**Files:**
- Create: `internal/ticketworker/completion.go`
- Create: `internal/ticketworker/completion_test.go`

**Interfaces:**
- Produces: `parseCompletionLine(marker, line string) (string, error)`.
- Produces: `CompletionRunner.Run(context.Context, CompletionRequest) CompletionResult`.
- Produces: default `ExecCompletionRunner`.

- [ ] **Step 1: Write failing parser and runner tests**

Accept `DONE ticket_id=TICKET-123`, dotted, underscored, and colon IDs. Reject missing/empty IDs, whitespace, extra tokens, controls, and wrong marker. Use a temporary fake executable to assert argv, CWD, non-zero exit reporting, bounded output, and deadline cancellation.

- [ ] **Step 2: Verify tests fail**

Run: `go test ./internal/ticketworker -run 'Test(ParseCompletionLine|ExecCompletionRunner)' -count=1`

Expected: compile failure for missing interfaces.

- [ ] **Step 3: Implement parser and runner**

Define:

```go
type CompletionRequest struct { Command []string; TicketID, CWD string }
type CompletionResult struct { Output string; Err error }
type CompletionRunner interface {
    Run(context.Context, CompletionRequest) CompletionResult
}
type ExecCompletionRunner struct{}
```

Validate the exact `<marker> ticket_id=<id>` line with compiled ID regexp. Copy argv, append `TicketID`, use `exec.CommandContext` with `cmd.Dir = CWD`, and capture sanitized stdout/stderr in an 8 KiB capped writer. Never invoke a shell.

- [ ] **Step 4: Format, test, and commit**

Run: `gofmt -w internal/ticketworker/completion.go internal/ticketworker/completion_test.go`

Run: `go test ./internal/ticketworker -run 'Test(ParseCompletionLine|ExecCompletionRunner)' -count=1`

Expected: PASS.

Commit: `git add internal/ticketworker/completion.go internal/ticketworker/completion_test.go && git commit -m "feat: add ticket completion runner"`

---

### Task 4: Manager Lifecycle and Manual Recovery

**Files:**
- Modify: `internal/ticketworker/manager.go`
- Modify: `internal/ticketworker/manager_test.go`

**Interfaces:**
- Consumes: prefix marker response, config fields, parser, and `CompletionRunner`.
- Produces: `occupied -> completing -> empty|completion_failed`.

- [ ] **Step 1: Write failing success/order tests**

Inject a fake runner through `ManagerOptions.CompletionRunner`. For completion-enabled config, assert the watch uses marker `DONE ` with prefix mode, parses `DONE ticket_id=TICKET-123`, calls the runner with `[]string{"ticket", "complete"}`, `/repo`, and the ID, and does not close before runner success.

- [ ] **Step 2: Write failing failure/recovery tests**

Prove malformed lines skip runner/close; runner errors and timeout retain pane/capacity; later ticks do not retry; running failed panes remain occupied; absent/closed/exited failed panes release and refill; wrong task/session does not count as active; legacy configs still exact-match and close immediately.

- [ ] **Step 3: Verify manager tests fail**

Run: `go test ./internal/ticketworker -run '^TestManager' -count=1`

Expected: compile/behavior failures for missing lifecycle support.

- [ ] **Step 4: Implement async completion states**

Add:

```go
const (
    slotEmpty slotState = iota
    slotOccupied
    slotCompleting
    slotCompletionFailed
)
```

Store ticket ID, runner, and a buffered completion-result channel. Default to `ExecCompletionRunner{}`. On valid structured marker, set completing and start a goroutine using `context.WithTimeout(ctx, CompleteTimeout)`. Send its typed result to the manager loop; only that loop mutates slots. Success enters the existing close/reconciliation flow. Failure records bounded diagnostics, sets completion-failed, and never closes.

- [ ] **Step 5: Implement manual-close reconciliation**

Before fill on each tick, inspect runtime once only when failed slots exist. An exact pane ID/task/session is active only in `starting` or `running`. Preserve slots on inspection error. Release absent or terminal failed slots, then let the same tick refill them. Never reconcile ordinary occupied/completing slots or rerun completion commands.

- [ ] **Step 6: Preserve legacy and race behavior**

Legacy config keeps exact response validation and immediate close. Extract the current close logic into a helper shared by legacy success and completion-command success, including the existing close-error plus confirmed-absence rule.

- [ ] **Step 7: Format, test, and commit**

Run: `gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go`

Run: `go test ./internal/ticketworker -run '^TestManager' -count=1`

Expected: PASS.

Commit: `git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go && git commit -m "feat: complete tickets before closing workers"`

---

### Task 5: Documentation and Verification

**Files:**
- Modify: `README.md`

**Interfaces:**
- Documents: generated config, prompt marker contract, command invocation, failure, and manual recovery.

- [ ] **Step 1: Update README**

Show the new init template. Require a successful coding-agent response to end with `ZELLIJ_AGENT_WORKER_DONE ticket_id=<ticket-id>`. Explain appended argv execution from project root, close-on-success, no automatic retry, and manual ticket completion plus pane closure.

- [ ] **Step 2: Run focused and full tests**

Run: `gofmt -w internal/runtime internal/transport internal/ticketworker`

Run: `go test ./internal/runtime ./internal/transport ./internal/ticketworker -count=1`

Run: `go test ./...`

Expected: all commands PASS.

- [ ] **Step 3: Build and register**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent`

Run: `cp bin/zellij-agent ~/.config/custom-cli`

Run: `cmp -s bin/zellij-agent ~/.config/custom-cli`

Expected: all commands exit 0.

- [ ] **Step 4: Review and commit docs**

Run: `git diff --check && git status --short`

Expected: no whitespace errors and only intended files.

Commit: `git add README.md && git commit -m "docs: explain ticket completion workflow"`
