# Ticket Manager Coding-Agent YOLO Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ticket-manager-created coding agents always run Codex in YOLO mode without changing the default behavior of other coding-agent launches.

**Architecture:** Add an optional `--yolo` flag at the existing coding-agent role boundary and translate it into Codex's `--dangerously-bypass-approvals-and-sandbox` option. Change only the ticket manager's runtime pane command to supply `--yolo`, then update role metadata and documentation.

**Tech Stack:** Go standard library (`flag`, `os/exec`, `testing`), existing runtime transport types, Markdown documentation.

## Global Constraints

- `coding-agent <path>` must continue to launch Codex without approval or sandbox overrides.
- `coding-agent --yolo <path>` must launch Codex with `--dangerously-bypass-approvals-and-sandbox`.
- Every coding-agent pane created by ticket-manager must use `--yolo` unconditionally.
- Pane creation must continue through `ManagerClient`; neither the manager nor clients may call Zellij directly.
- Rebuild and atomically register `bin/zellij-agent` after verification.

---

### Task 1: Add the coding-agent YOLO option

**Files:**
- Modify: `cmd/agent-role/codingagent/codingagent_test.go`
- Modify: `cmd/agent-role/codingagent/codingagent.go`

**Interfaces:**
- Consumes: `prepare(args []string) (*exec.Cmd, error)` used by `Run` and package tests.
- Produces: support for `prepare([]string{"--yolo", path})`, whose command args contain `--dangerously-bypass-approvals-and-sandbox`.

- [ ] **Step 1: Write failing command-construction and invalid-argument tests**

Add assertions that plain invocation has no Codex arguments, YOLO invocation has exactly the dangerous Codex flag, and unknown flags or missing paths fail.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./cmd/agent-role/codingagent -run 'TestPrepare' -count=1`

Expected: FAIL because `prepare` currently accepts exactly one positional path and rejects `--yolo`.

- [ ] **Step 3: Implement role-local flag parsing**

Use a `flag.FlagSet` with `ContinueOnError`, register `--yolo`, require exactly one positional path, and construct `exec.Command(codexPath, "--dangerously-bypass-approvals-and-sandbox")` only when enabled. Keep repository discovery and safe default behavior unchanged.

- [ ] **Step 4: Run the focused tests**

Run: `go test ./cmd/agent-role/codingagent -count=1`

Expected: PASS.

### Task 2: Make ticket-manager workers always request YOLO

**Files:**
- Modify: `internal/ticketworker/manager_test.go`
- Modify: `internal/ticketworker/manager.go`

**Interfaces:**
- Consumes: coding-agent's new `--yolo` CLI option.
- Produces: worker `transport.CreatePaneRequest.Command` values shaped as `[]string{roleBin, "role", "coding-agent", "--yolo", root}`.

- [ ] **Step 1: Change the manager command expectation first**

Update `TestManagerWaitsForAnchorThenFillsConfiguredCapacity` to expect `--yolo` between `coding-agent` and `/repo`.

- [ ] **Step 2: Run the focused manager test and verify failure**

Run: `go test ./internal/ticketworker -run '^TestManagerWaitsForAnchorThenFillsConfiguredCapacity$' -count=1`

Expected: FAIL because the generated command does not include `--yolo`.

- [ ] **Step 3: Add `--yolo` to the worker role command**

Construct the pane command as `[]string{m.roleBin, "role", "coding-agent", "--yolo", m.root}` without adding manager configuration or bypassing `ManagerClient`.

- [ ] **Step 4: Run the focused manager tests**

Run: `go test ./internal/ticketworker -count=1`

Expected: PASS.

### Task 3: Update role metadata and documentation

**Files:**
- Modify: `internal/roles/roles_test.go`
- Modify: `internal/roles/roles.go`
- Modify: `README.md`
- Modify or create: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

**Interfaces:**
- Consumes: the finalized coding-agent CLI contract.
- Produces: `coding-agent [--yolo] <path>` in role discovery and accurate ticket-manager behavior documentation.

- [ ] **Step 1: Add a failing catalog assertion**

Assert that the coding-agent spec usage is `coding-agent [--yolo] <path>` and includes an optional `--yolo` argument.

- [ ] **Step 2: Run the catalog test and verify failure**

Run: `go test ./internal/roles -run '^TestCodingAgentRoleMetadata$' -count=1`

Expected: FAIL on the old usage or missing option metadata.

- [ ] **Step 3: Update catalog and user documentation**

Change the role usage, add optional `--yolo` metadata describing the Codex approval/sandbox bypass, state in `README.md` that ticket-manager workers always use YOLO mode, and synchronize the external role summary.

- [ ] **Step 4: Run role tests and inspect role output**

Run: `go test ./internal/roles -count=1 && go run ./cmd/agent-role roles`

Expected: PASS and output containing `coding-agent [--yolo] <path>`.

### Task 4: Verify and register the unified binary

**Files:**
- Build output: `bin/zellij-agent`
- Registered output: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: all preceding code and metadata changes.
- Produces: a tested and atomically registered unified CLI binary.

- [ ] **Step 1: Format and run the full test suite**

Run: `gofmt -w cmd/agent-role/codingagent/codingagent.go cmd/agent-role/codingagent/codingagent_test.go internal/ticketworker/manager.go internal/ticketworker/manager_test.go internal/roles/roles.go internal/roles/roles_test.go && go test ./...`

Expected: PASS.

- [ ] **Step 2: Build the role and unified binaries**

Run: `go build -o bin/agent-role ./cmd/agent-role && go build -o bin/zellij-agent ./cmd/zellij-agent`

Expected: both commands exit zero.

- [ ] **Step 3: Inspect the built role catalog**

Run: `./bin/agent-role roles`

Expected: output includes `coding-agent [--yolo] <path>` and the optional YOLO argument.

- [ ] **Step 4: Register the unified binary atomically**

Run: `cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new && chmod 755 ~/.config/custom-cli/.zellij-agent.new && mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent`

Expected: the registered executable exists and is executable.

- [ ] **Step 5: Review the final diff**

Run: `git diff --check && git status --short && git diff --stat`

Expected: no whitespace errors and only intended project changes remain; the pre-existing `AGENTS.md` edit is preserved.
