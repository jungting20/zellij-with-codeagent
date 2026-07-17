# Ticket Worker Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every existing ticket-worker capability while retaining a visible, side-effect-free `zellij-agent ticket-worker` placeholder entrypoint.

**Architecture:** Keep dispatch ownership in `cmd/zellij-agent` and a minimal command contract in `internal/cli/ticketworker`. Delete the implementation package and feature documentation so no configuration, daemon, plan, pane, worker, or completion path remains reachable.

**Tech Stack:** Go standard library, Go `testing`, Markdown, existing unified CLI build and install workflow.

## Global Constraints

- Keep `ticket-worker` visible in top-level help.
- `ticket-worker --help` returns success; bare invocation and every former subcommand return non-zero.
- The placeholder must not read files, contact the daemon, submit a plan, create panes, or start processes.
- Do not change general runtime, transport, dashboard, or Zellij behavior.
- Reinstall `bin/zellij-agent` atomically at `~/.config/custom-cli/zellij-agent` after verification.

---

### Task 1: Replace the command with a placeholder

**Files:**
- Modify: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `internal/cli/ticketworker/ticketworker.go`
- Modify: `cmd/zellij-agent/main.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Produces: `ticketworkercli.Run(args []string, stdout, stderr io.Writer) int`
- Consumes: top-level `run` dispatch and ordinary `io.Writer` output streams.

- [ ] **Step 1: Replace focused tests with the placeholder contract**

Test `Run([]string{"--help"}, ...) == 0`, help contains `Usage: zellij-agent ticket-worker` and `not implemented`, bare `Run(nil, ...) == 2`, and `Run([]string{"init"}, ...) == 2` with an unavailable message. Update the top-level dispatch test to require the same placeholder text and exclude `init` and `start` from help.

- [ ] **Step 2: Run tests to verify the old implementation fails the new contract**

Run: `go test ./internal/cli/ticketworker ./cmd/zellij-agent -run 'TestRun.*TicketWorker' -count=1`

Expected: FAIL because the old CLI advertises and executes `init`, `start`, and `manager`.

- [ ] **Step 3: Implement the minimal entrypoint**

Use this complete command shape:

```go
func Run(args []string, stdout, stderr io.Writer) int {
    if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
        printUsage(stdout)
        return 0
    }
    fmt.Fprintln(stderr, "ticket-worker is not implemented")
    return 2
}

func printUsage(w io.Writer) {
    fmt.Fprintln(w, "Usage: zellij-agent ticket-worker")
    fmt.Fprintln(w)
    fmt.Fprintln(w, "ticket-worker is not implemented")
}
```

Change top-level dispatch to `ticketworkercli.Run(args[1:], stdout, stderr)`, delete `newTicketWorkerClient`, and describe the command as `Reserved placeholder; not implemented` in top-level help.

- [ ] **Step 4: Format and run focused tests**

Run: `gofmt -w internal/cli/ticketworker cmd/zellij-agent && go test ./internal/cli/ticketworker ./cmd/zellij-agent -count=1`

Expected: PASS.

### Task 2: Delete the discarded implementation and documentation

**Files:**
- Delete: `internal/ticketworker/completion.go`
- Delete: `internal/ticketworker/completion_test.go`
- Delete: `internal/ticketworker/config.go`
- Delete: `internal/ticketworker/config_test.go`
- Delete: `internal/ticketworker/manager.go`
- Delete: `internal/ticketworker/manager_test.go`
- Delete: `internal/ticketworker/plan.go`
- Delete: `internal/ticketworker/plan_test.go`
- Modify: `README.md`
- Delete: `docs/ticket-worker-known-issues.md`
- Delete: `docs/superpowers/specs/2026-07-16-ticket-worker-pane-pool-design.md`
- Delete: `docs/superpowers/specs/2026-07-16-ticket-worker-race-recovery-design.md`
- Delete: `docs/superpowers/specs/2026-07-17-ticket-worker-ticket-completion-design.md`
- Delete: `docs/superpowers/plans/2026-07-16-ticket-worker-pane-pool.md`
- Delete: `docs/superpowers/plans/2026-07-16-ticket-worker-race-recovery.md`
- Delete: `docs/superpowers/plans/2026-07-17-ticket-worker-ticket-completion.md`
- Modify: `docs/superpowers/specs/2026-07-16-request-scoped-zellij-session-design.md`
- Modify: `docs/superpowers/plans/2026-07-16-request-scoped-zellij-session.md`

**Interfaces:**
- Consumes: the placeholder established in Task 1.
- Produces: no ticket-worker feature implementation or active feature instructions.

- [ ] **Step 1: Remove the feature implementation files**

Delete every file under `internal/ticketworker`; do not replace the package.

- [ ] **Step 2: Remove feature-specific documentation**

Delete the README `Ticket Worker Pool` section, the known-issues page, and the three discarded feature spec/plan pairs. Remove ticket-worker-only claims and task sections from the shared request-scoped-session documents while preserving other commands' session behavior.

- [ ] **Step 3: Scan for stale implementation references**

Run: `rg -n --hidden --glob '!.git' 'ticket-worker|ticketworker|TicketWorker' .`

Expected: matches only the placeholder source/tests, the reset spec/plan, harmless generic test fixture names, and top-level help/dispatch.

### Task 3: Verify, build, and atomically install

**Files:**
- Build output: `bin/zellij-agent`
- Install output: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: the placeholder command and cleaned source tree.
- Produces: a tested and installed unified binary.

- [ ] **Step 1: Run repository verification**

Run: `gofmt -w internal/cli/ticketworker cmd/zellij-agent && git diff --check && go test ./...`

Expected: all checks and tests PASS.

- [ ] **Step 2: Build and smoke-test the placeholder**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent`

Run: `./bin/zellij-agent --help`

Expected: output lists `ticket-worker` as a reserved placeholder.

Run: `./bin/zellij-agent ticket-worker --help`

Expected: exit 0 and output says `ticket-worker is not implemented`.

Run: `./bin/zellij-agent ticket-worker start`

Expected: exit 2 and no daemon or pane activity.

- [ ] **Step 3: Install atomically on the custom CLI path**

Run: `cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new && chmod 755 ~/.config/custom-cli/.zellij-agent.new && mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent`

Expected: the final executable exists, is executable, and its `ticket-worker --help` reports the placeholder contract.

- [ ] **Step 4: Review the final diff and status**

Run: `git diff --check && git status --short && git diff --stat HEAD`

Expected: only the planned reset implementation and documentation changes are present after the already committed design specification.
