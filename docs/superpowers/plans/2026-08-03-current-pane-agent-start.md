# Current Pane Agent Start Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `zellij-agent agent start <kind>` register and use the current Zellij pane, run the interactive coding agent there, and close that pane when the agent exits without changing ticket-worker pane creation.

**Architecture:** Add a runtime `ClaimPane` use case that resolves and registers an existing physical pane without creating one. `codingagent.Service.StartAgent` uses that claim path, while the CLI runs the returned command as an interactive child and closes the claimed logical pane after child termination. Existing execution-plan, `CreatePane`, and `zellij-agent role coding-agent` paths remain unchanged.

**Tech Stack:** Go, standard library `os/exec` and `os/signal`, in-memory runtime registry, Unix-socket HTTP transport, Zellij CLI backend.

## Global Constraints

- Only `zellij-agent agent start` claims the current pane; keep `RuntimeService.CreatePane` unchanged.
- Keep pane lookup, registration, observation, focus, and close behind `RuntimeService`; the CLI never invokes Zellij directly.
- Reuse the existing `coding-agent` default role and profiles; do not add a role or make the role self-register.
- Close a successfully claimed pane after normal exit, non-zero exit, or child startup failure.
- Atomically reject duplicate active `(ZellijSession, ZellijPaneID)` registrations; allow terminal-record reuse and the same pane ID in another session.
- Preserve `ticket-worker start`, its execution plan, and manager-created worker `CreatePane` requests.
- Keep daemon restart recovery out of scope.
- Run `gofmt`, relevant focused tests, and `go test ./...`; commit messages must be Korean.
- Rebuild and install `bin/zellij-agent` with the repository's `.zellij-agent.new` atomic rename sequence.

---

### Task 1: Enforce active physical pane uniqueness

**Files:**
- Modify: `internal/registry/types.go`
- Modify: `internal/registry/registry.go:48-123,512-535`
- Modify: `internal/registry/registry_test.go`

**Interfaces:**
- Consumes: `RegisterPaneRequest.SessionID`, `ZellijPaneID`, and `Status`.
- Produces: `registry.ErrZellijPaneAlreadyRegistered` and atomic physical-pane validation in `RegisterPane`.

- [ ] **Step 1: Write failing registry tests**

Add `TestRegisterPaneRejectsActivePhysicalPaneDuplicate` for both active statuses:

```go
for _, status := range []PaneStatus{PaneStatusStarting, PaneStatusRunning} {
	reg := newTestRegistry()
	_, _ = reg.RegisterPane(RegisterPaneRequest{
		ID: "pane-1", SessionID: "session-a",
		ZellijPaneID: "terminal_2", Status: status,
	})
	_, err := reg.RegisterPane(RegisterPaneRequest{
		ID: "pane-2", SessionID: "session-a", ZellijPaneID: "terminal_2",
	})
	if !errors.Is(err, ErrZellijPaneAlreadyRegistered) {
		t.Fatalf("RegisterPane() error = %v", err)
	}
}
```

Add `TestRegisterPaneAllowsPhysicalPaneReuseOutsideActiveScope`. Verify `closed`, `exited`, `lost`, and `error` records do not block reuse. Verify an active `session-a/terminal_2` does not block `session-b/terminal_2`.

Add `TestRegisterPaneAllowsOnlyOneConcurrentPhysicalPaneRegistration`. Release two goroutines against the same registry at once with different logical IDs but the same `session-a/terminal_2`; assert exactly one result is nil and exactly one wraps `ErrZellijPaneAlreadyRegistered`.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/registry -run 'RegisterPane.*PhysicalPane' -count=1
```

Expected: FAIL because physical uniqueness is absent.

- [ ] **Step 3: Implement lock-protected validation**

Define:

```go
var ErrZellijPaneAlreadyRegistered = errors.New("zellij pane already registered")
```

Apply register defaults before uniqueness checks. Under `Registry.mu`, scan only the requested session and reject a record with the same physical ID only when status is `starting` or `running`:

```go
func (r *Registry) validateActiveZellijPaneUniqueLocked(req RegisterPaneRequest) error {
	if req.ZellijPaneID == "" {
		return nil
	}
	for _, tab := range r.sessions[req.SessionID].Tabs {
		for _, pane := range tab.Panes {
			if pane.ZellijPaneID == req.ZellijPaneID &&
				(pane.Status == PaneStatusStarting || pane.Status == PaneStatusRunning) {
				return fmt.Errorf("%w: session %q pane %q", ErrZellijPaneAlreadyRegistered, req.SessionID, req.ZellijPaneID)
			}
		}
	}
	return nil
}
```

Guard the missing-session map lookup. Keep logical ID validation and `latestByZellij` behavior intact.

- [ ] **Step 4: Verify and commit**

```bash
gofmt -w internal/registry/types.go internal/registry/registry.go internal/registry/registry_test.go
go test ./internal/registry -count=1
git add internal/registry
git commit -m "feat: 활성 물리 pane 중복 등록 방지"
```

---

### Task 2: Add RuntimeService.ClaimPane

**Files:**
- Modify: `internal/runtime/types.go:10-133`
- Create: `internal/runtime/claim.go`
- Create: `internal/runtime/claim_test.go`

**Interfaces:**
- Consumes: `Backend.ListPanes`, `Registry.RegisterPane`, `PaneObserver`, and `SubscriptionManager.StartPane`.
- Produces: `PaneClaimService`, `ClaimPaneRequest`, `ClaimPaneResponse`, and `(*Service).ClaimPane`.

- [ ] **Step 1: Write failing claim tests**

Create a success test using `fakeBackend` and `recordingPaneObserver`:

```go
backend := &fakeBackend{listPanes: []zellij.Pane{{
	ID: "terminal_2", TabID: 7, TabName: "work", Command: "zsh",
}}}
response, err := service.ClaimPane(context.Background(), ClaimPaneRequest{
	ID: "agent-1", AgentID: "agent-1", Role: "coding-agent",
	ZellijSession: "session-a", ZellijPaneID: "terminal_2",
	Command: []string{"codex", "--dangerously-bypass-approvals-and-sandbox"},
	CWD: "/workspace",
})
```

Assert logical/physical IDs, tab ID 7, tab name, command, and CWD. Assert zero backend `CreatePane`, `CreateTab`, and `SendInput` calls and one `PaneOpened` observation. Add cases for blank session, blank physical ID, missing pane, duplicate matches, plugin pane, already-managed physical pane, and subscription startup.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/runtime -run '^TestClaimPane' -count=1
```

Expected: FAIL because the claim API is absent.

- [ ] **Step 3: Add the claim types and service boundary**

```go
type PaneClaimService interface {
	ClaimPane(context.Context, ClaimPaneRequest) (ClaimPaneResponse, error)
}

type ClaimPaneRequest struct {
	ID PaneID
	TaskID TaskID
	AgentID AgentID
	Role string
	ZellijSession string
	ZellijPaneID ZellijPaneID
	Command []string
	CWD string
}

type ClaimPaneResponse struct { Pane Pane }
```

Embed `PaneClaimService` in `RuntimeService` without adding it to the public pane transport.

- [ ] **Step 4: Implement ClaimPane without mutating Zellij**

Trim and validate identifiers, list panes in the specified session, and require exactly one non-plugin match. Convert the tab ID and register the match with its discovered metadata:

```go
tabID := registry.ZellijTabID(match.TabID)
record, err := s.registry.RegisterPane(registry.RegisterPaneRequest{
	ID: registry.PaneID(req.ID), SessionID: registry.SessionID(req.ZellijSession),
	TabID: registry.TabID(strconv.Itoa(match.TabID)), TaskID: registry.TaskID(req.TaskID),
	AgentID: registry.AgentID(req.AgentID), ZellijPaneID: registry.ZellijPaneID(req.ZellijPaneID),
	ZellijTabID: &tabID, TabName: match.TabName,
	Role: req.Role, Command: cloneStrings(req.Command), CWD: req.CWD,
})
```

Convert `registry.ErrZellijPaneAlreadyRegistered` to an error wrapping `ErrInvalidPaneTarget`. After registration, call `PaneOpened` and `StartPane` as `createPaneOnce` does. Never call create, input, or cleanup operations.

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w internal/runtime/types.go internal/runtime/claim.go internal/runtime/claim_test.go
go test ./internal/runtime -run 'ClaimPane|CreatePane|ClosePane' -count=1
git add internal/runtime
git commit -m "feat: 현재 Zellij pane 등록 경로 추가"
```

---

### Task 3: Switch codingagent.StartAgent to claim

**Files:**
- Modify: `internal/codingagent/service.go:125-213,487-565`
- Modify: `internal/codingagent/service_test.go`

**Interfaces:**
- Consumes: `RuntimeService.ClaimPane` from Task 2.
- Produces: an existing-pane `StartAgent` response with the profile command stored on the runtime pane.

- [ ] **Step 1: Convert StartAgent tests to ClaimPane**

Extend `serviceFakeRuntime`:

```go
claimFn func(context.Context, runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error)
claimErr error
claimed []runtime.ClaimPaneRequest
```

Implement its `ClaimPane` method:

```go
func (f *serviceFakeRuntime) ClaimPane(ctx context.Context, request runtime.ClaimPaneRequest) (runtime.ClaimPaneResponse, error) {
	f.claimed = append(f.claimed, request)
	if f.claimFn != nil {
		return f.claimFn(ctx, request)
	}
	if f.claimErr != nil {
		return runtime.ClaimPaneResponse{}, f.claimErr
	}
	return runtime.ClaimPaneResponse{Pane: runtime.Pane{
		ID: request.ID, AgentID: request.AgentID, Role: request.Role,
		SessionID: runtime.SessionID(request.ZellijSession),
		ZellijPaneID: request.ZellijPaneID,
		Command: append([]string(nil), request.Command...), CWD: request.CWD,
	}}, nil
}
```

Change the main expectation to:

```go
runtime.ClaimPaneRequest{
	ID: "agent-1", AgentID: "agent-1", Role: "coding-agent",
	ZellijSession: "physical-a", ZellijPaneID: "terminal_2",
	Command: []string{"agy", "--dangerously-skip-permissions", "--model", "gemini-3"},
	CWD: cwd,
}
```

Invalid input and monitor failure must produce zero claims. Claim failure must stop the monitor, delete the agent record, make zero cleanup calls, and leave the source pane untouched.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/codingagent -run '^TestServiceStartAgent' -count=1
```

Expected: FAIL because production still calls `CreatePane`.

- [ ] **Step 3: Replace creation and remove obsolete partial-create recovery**

Call:

```go
paneResponse, err := s.RuntimeService.ClaimPane(ctx, runtime.ClaimPaneRequest{
	ID: created.PaneID, AgentID: runtime.AgentID(created.ID), Role: "coding-agent",
	ZellijSession: sourceSession, ZellijPaneID: sourcePaneID,
	Command: profile.BuildCommand(true, request.ExtraArgs), CWD: cwd,
})
```

On error, call only `cleanupOwnedRecord(owner, true)` and return `claim coding agent pane`. Remove the `ErrCleanupPartial` branch, `partialCleanupTimeout`, `recoverPartialCreate`, `TestServicePartialRecoveryReservationPreventsSameIDReuseUntilCleanupReturns`, and `TestServiceStartAgentPartialRuntimeCleanupPolicy`. Keep generic owner/orphan cleanup logic.

- [ ] **Step 4: Verify and commit**

```bash
gofmt -w internal/codingagent/service.go internal/codingagent/service_test.go
go test ./internal/codingagent -count=1
git add internal/codingagent
git commit -m "feat: 코딩 에이전트를 현재 pane에 등록"
```

---

### Task 4: Run the interactive agent and close the pane from the CLI

**Files:**
- Modify: `internal/cli/agent/agent.go:29-42,44-67,202-287,430-453`
- Modify: `internal/cli/agent/agent_test.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Consumes: `StartAgentResponse.Agent.Pane.Command`, `.CWD`, and existing transport `Client.ClosePane`.
- Produces: `Config.RunAgent`, a default interactive runner, and close-after-exit lifecycle.

- [ ] **Step 1: Write failing CLI lifecycle tests**

Extend `AgentClient` and `testClient` with:

```go
ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
```

Add:

```go
type AgentRunner func(command []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error
```

Add `RunAgent AgentRunner` to `Config`. Update fake start responses with command and CWD. Test exact runner command/CWD/stdio, no old `started agent=` output, and one close request for the logical pane ID. Cover registration failure, empty command, runner error, close error, and fresh close deadline. Assert the registration context is canceled before the runner starts. In `cmd/zellij-agent/main_test.go`, return `Command: []string{"/usr/bin/true"}` with the temporary CWD and add fake `ClosePane` support so the top-level dispatch test exercises the lifecycle without launching a real agent.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/cli/agent ./cmd/zellij-agent -run 'RunStart|AgentStart' -count=1
```

Expected: FAIL because start returns immediately.

- [ ] **Step 3: Implement the default runner**

Pass stdin into `runStart`. Implement:

```go
func runAgentProcess(command []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return errors.New("coding agent command is required")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = cwd, stdin, stdout, stderr
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return cmd.Run()
}
```

Do not create a separate process group and do not call Zellij.

- [ ] **Step 4: Implement register-run-close sequencing**

Cancel the registration context immediately after `StartAgent` returns. Run the response command without that timeout. After every successful registration, call `ClosePane` with a fresh timeout context:

```go
runErr := runner(response.Agent.Pane.Command, response.Agent.Pane.CWD, stdin, stdout, stderr)
closeCtx, cancelClose := context.WithTimeout(context.Background(), opts.timeout)
_, closeErr := client.ClosePane(closeCtx, startedPaneID)
cancelClose()
```

Return 1 if runner or close fails and 0 otherwise. Remove success output and update help to describe current-pane management and close-on-exit.

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main_test.go
go test ./internal/cli/agent ./cmd/zellij-agent -count=1
git add internal/cli/agent cmd/zellij-agent/main_test.go
git commit -m "feat: 현재 pane에서 코딩 에이전트 실행"
```

---

### Task 5: Document behavior and verify ticket-worker isolation

**Files:**
- Modify: `README.md:84-103`
- Verify unchanged: `internal/ticketworker/plan.go`
- Verify unchanged: `internal/ticketworker/manager.go`
- Verify: `internal/ticketworker/plan_test.go`
- Verify: `internal/ticketworker/manager_test.go`

**Interfaces:**
- Consumes: Tasks 1-4.
- Produces: accurate current-pane lifecycle documentation and ticket-worker regression evidence.

- [ ] **Step 1: Update README**

Replace the new-pane statement with: current context is registered, the selected agent runs in the current terminal, the pane closes when the agent exits, and an already-managed pane is rejected. Keep all four start examples. State explicitly that ticket-worker still creates manager and worker panes through execution plans and `CreatePane`.

- [ ] **Step 2: Run ticket-worker regressions**

```bash
go test ./internal/ticketworker -run 'BuildStartPlan|Manager.*Start|FillEmptySlots' -count=1
```

Expected: PASS. Plan tests still assert the ticket-manager execution plan, and manager tests still assert `CreatePaneRequest` plus `zellij-agent role coding-agent --yolo ...`.

- [ ] **Step 3: Check and commit docs**

```bash
git diff --check
rg -n 'current pane|현재 pane|agent exits|ticket-worker' README.md
git add README.md
git commit -m "docs: 현재 pane 에이전트 시작 동작 반영"
```

---

### Task 6: Full verification, build, and atomic installation

**Files:**
- Verify all modified Go and Markdown files.
- Build: `bin/zellij-agent`
- Install: `/Users/in05908_mac/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: all previous tasks.
- Produces: a tested and locally installed unified binary.

- [ ] **Step 1: Format and inspect**

```bash
gofmt -w internal/registry/types.go internal/registry/registry.go internal/registry/registry_test.go internal/runtime/types.go internal/runtime/claim.go internal/runtime/claim_test.go internal/codingagent/service.go internal/codingagent/service_test.go internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main_test.go
git diff --check
git status --short
```

- [ ] **Step 2: Run the complete suite**

```bash
go test ./...
```

Expected: PASS, including ticket-worker.

- [ ] **Step 3: Build and install atomically**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
```

- [ ] **Step 4: Verify installed help and repository state**

```bash
/Users/in05908_mac/.config/custom-cli/zellij-agent agent start --help
/Users/in05908_mac/.config/custom-cli/zellij-agent agent --help
git log -8 --oneline
git status --short
```

Expected: help documents current-pane lifecycle and the worktree is clean.

- [ ] **Step 5: Manual Zellij smoke test**

```text
1. In an unmanaged shell pane, run zellij-agent agent start codex.
2. Confirm no new pane appears and dashboard lists the current pane.
3. Confirm agent next can focus it.
4. Exit Codex and confirm that pane closes.
5. Run ticket-worker start and confirm manager and worker panes are still newly created.
```
