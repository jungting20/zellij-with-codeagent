# Work Goal Prefill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prefill the interactive coder pane created by zellij-agent work "<goal>" with the exact trimmed goal while leaving submission to the user.

**Architecture:** Add optional initial input to the transport and runtime execution-plan pane contracts. The work plan builder sets it only for the coder pane, and RuntimeService.ApplyExecutionPlan sends it through the managed-pane input path after creation, rolling back the plan if delivery fails.

**Tech Stack:** Go standard library, internal/transport, internal/runtime, internal/work, Go testing, Zellij CLI backend.

## Global Constraints

- The coder input is exactly the trimmed goal; do not append instructions, a newline, or Enter.
- The user reviews the text and presses Enter before Codex begins processing it.
- Planners and clients never call Zellij directly; delivery uses RuntimeService.SendInput.
- initial_input is optional and serialized with omitempty.
- Empty initial input does not call the backend.
- Delivery failure fails plan application and rolls back every pane created for that plan.
- Only the work coder pane receives initial input in this increment.
- Project detection, test-command selection, optional-tool checks, new flags, and feedback-loop behavior are out of scope.

---

## File Structure

- internal/transport/types.go: define and convert the JSON execution-plan field.
- internal/transport/types_test.go: verify transport-to-runtime preservation.
- internal/runtime/execution_plan.go: carry and deliver initial input, including rollback.
- internal/runtime/execution_plan_test.go: cover delivery, empty input, and failure.
- internal/work/work.go: set coder initial input from the normalized goal.
- internal/work/work_test.go: verify exact coder-only prefill.
- internal/cli/work/work_test.go: verify dry-run JSON.
- README.md: describe review-before-submit behavior.
- docs/manual-smoke-test.md: document real-Zellij verification.
- docs/next-steps-todolist.md: mark only the delivered Phase A item complete.

---

### Task 1: Add Initial Input to the Execution-Plan Contract

**Files:**
- Modify: internal/transport/types.go:155
- Modify: internal/transport/types.go:444
- Modify: internal/transport/types_test.go:72
- Modify: internal/runtime/execution_plan.go:14

**Interfaces:**
- Consumes: ExecutionPlanPane.ToRuntime() rt.ExecutionPlanPaneSpec.
- Produces: transport.ExecutionPlanPane.InitialInput string and runtime.ExecutionPlanPaneSpec.InitialInput string.

- [ ] **Step 1: Extend the nested conversion test first**

In TestExecutionPlanPayloadToRuntimePreservesNestedPayload, add this field to the source pane:

    InitialInput: "inspect the auth flow",

Require the converted pane to preserve it:

    if pane.ID != "planner" ||
        pane.Role != "planner" ||
        pane.AgentID != "agent-1" ||
        pane.CWD != "/tmp/app" ||
        pane.InitialInput != "inspect the auth flow" {
        t.Fatalf("ExecutionPlanPayload.ToRuntime() pane = %#v, want payload fields preserved", pane)
    }

- [ ] **Step 2: Run the focused test and verify RED**

    go test ./internal/transport -run '^TestExecutionPlanPayloadToRuntimePreservesNestedPayload$' -count=1

Expected: build failure because ExecutionPlanPane and ExecutionPlanPaneSpec do not define InitialInput.

- [ ] **Step 3: Add the optional transport field**

Change transport.ExecutionPlanPane to:

    type ExecutionPlanPane struct {
        ID           string   `json:"id"`
        Role         string   `json:"role,omitempty"`
        AgentID      string   `json:"agent_id,omitempty"`
        Command      []string `json:"command,omitempty"`
        CWD          string   `json:"cwd,omitempty"`
        InitialInput string   `json:"initial_input,omitempty"`
    }

- [ ] **Step 4: Add the runtime field and conversion**

Change runtime.ExecutionPlanPaneSpec to:

    type ExecutionPlanPaneSpec struct {
        ID           PaneID
        Role         string
        AgentID      AgentID
        Command      []string
        CWD          string
        InitialInput string
    }

Change ExecutionPlanPane.ToRuntime to:

    func (pane ExecutionPlanPane) ToRuntime() rt.ExecutionPlanPaneSpec {
        return rt.ExecutionPlanPaneSpec{
            ID:           rt.PaneID(pane.ID),
            Role:         pane.Role,
            AgentID:      rt.AgentID(pane.AgentID),
            Command:      cloneStrings(pane.Command),
            CWD:          pane.CWD,
            InitialInput: pane.InitialInput,
        }
    }

- [ ] **Step 5: Format and verify GREEN**

    gofmt -w internal/transport/types.go internal/transport/types_test.go internal/runtime/execution_plan.go
    go test ./internal/transport -run '^TestExecutionPlanPayloadToRuntimePreservesNestedPayload$' -count=1

Expected: PASS.

- [ ] **Step 6: Run affected suites**

    go test ./internal/transport ./internal/runtime -count=1

Expected: both packages PASS.

- [ ] **Step 7: Commit**

    git add internal/transport/types.go internal/transport/types_test.go internal/runtime/execution_plan.go
    git commit -m "feat: add execution plan initial input"

---

### Task 2: Deliver Initial Input Through Runtime and Roll Back Failures

**Files:**
- Modify: internal/runtime/execution_plan.go:32
- Modify: internal/runtime/execution_plan.go:52
- Modify: internal/runtime/execution_plan.go:108
- Modify: internal/runtime/execution_plan_test.go

**Interfaces:**
- Consumes: ExecutionPlanPaneSpec.InitialInput and Service.SendInput(context.Context, SendInputRequest).
- Produces: sendExecutionPlanInitialInput(context.Context, Pane, string) error and atomic apply behavior on delivery failure.

- [ ] **Step 1: Write a failing delivery test**

Append to internal/runtime/execution_plan_test.go:

    func TestApplyExecutionPlanSendsInitialInputForFirstAndRemainingPanes(t *testing.T) {
        tabID := ZellijTabID(31)
        backend := &fakeBackend{
            createTabID: zellij.TabID(tabID),
            listPanes: []zellij.Pane{
                {ID: "terminal_31a", TabID: int(tabID), TabName: "goal-prefill"},
                {ID: "terminal_31b", TabID: int(tabID), TabName: "goal-prefill"},
            },
            createIDs: []zellij.PaneID{"terminal_31b"},
        }
        service := newTestService(backend)

        _, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
            Session: "goal-prefill",
            Tabs: []ExecutionPlanTabSpec{{
                Name: "goal-prefill",
                Panes: []ExecutionPlanPaneSpec{
                    {ID: "coder", InitialInput: "fix the parser"},
                    {ID: "notes", InitialInput: "review these notes"},
                },
            }},
        })
        if err != nil {
            t.Fatalf("ApplyExecutionPlan() error = %v", err)
        }

        got := append([]zellij.SendInputRequest(nil), backend.sendRequests...)
        sort.Slice(got, func(i, j int) bool { return got[i].PaneID < got[j].PaneID })
        want := []zellij.SendInputRequest{
            {PaneID: "terminal_31a", Text: "fix the parser"},
            {PaneID: "terminal_31b", Text: "review these notes"},
        }
        if !reflect.DeepEqual(got, want) {
            t.Fatalf("SendInput requests = %#v, want %#v", got, want)
        }
    }

- [ ] **Step 2: Write an empty-input compatibility test**

Append:

    func TestApplyExecutionPlanSkipsEmptyInitialInput(t *testing.T) {
        tabID := ZellijTabID(32)
        backend := &fakeBackend{
            createTabID: zellij.TabID(tabID),
            listPanes: []zellij.Pane{
                {ID: "terminal_32a", TabID: int(tabID), TabName: "empty-prefill"},
            },
        }
        service := newTestService(backend)

        _, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
            Session: "empty-prefill",
            Tabs: []ExecutionPlanTabSpec{{
                Name:  "empty-prefill",
                Panes: []ExecutionPlanPaneSpec{{ID: "coder"}},
            }},
        })
        if err != nil {
            t.Fatalf("ApplyExecutionPlan() error = %v", err)
        }
        if len(backend.sendRequests) != 0 {
            t.Fatalf("SendInput requests = %#v, want none", backend.sendRequests)
        }
    }

- [ ] **Step 3: Write a failing rollback test**

Add strings to the imports and append:

    func TestApplyExecutionPlanRollsBackOnInitialInputFailure(t *testing.T) {
        tabID := ZellijTabID(33)
        backend := &fakeBackend{
            createTabID: zellij.TabID(tabID),
            listPanes: []zellij.Pane{
                {ID: "terminal_33a", TabID: int(tabID), TabName: "failed-prefill"},
            },
            sendErr: errors.New("paste failed"),
        }
        service := newTestService(backend)

        _, err := service.ApplyExecutionPlan(context.Background(), ApplyExecutionPlanRequest{
            Session: "failed-prefill",
            Tabs: []ExecutionPlanTabSpec{{
                Name: "failed-prefill",
                Panes: []ExecutionPlanPaneSpec{{
                    ID:           "coder",
                    InitialInput: "fix the parser",
                }},
            }},
        })
        if err == nil || !strings.Contains(err.Error(), `send initial input to pane "coder"`) {
            t.Fatalf("ApplyExecutionPlan() error = %v, want pane-specific error", err)
        }

        list, listErr := service.ListPanes(context.Background())
        if listErr != nil {
            t.Fatalf("ListPanes() error = %v", listErr)
        }
        if len(list.Panes) != 0 {
            t.Fatalf("ListPanes() = %#v, want empty registry", list.Panes)
        }
        if len(backend.closeRequests) != 1 || backend.closeRequests[0].PaneID != "terminal_33a" {
            t.Fatalf("ClosePane requests = %#v, want created pane rollback", backend.closeRequests)
        }
    }

- [ ] **Step 4: Verify RED**

    go test ./internal/runtime -run 'InitialInput' -count=1

Expected: delivery and rollback tests FAIL because ApplyExecutionPlan does not send input. The empty-input guard may pass.

- [ ] **Step 5: Add the delivery helper**

Add to internal/runtime/execution_plan.go:

    func (s *Service) sendExecutionPlanInitialInput(ctx context.Context, pane Pane, initialInput string) error {
        if initialInput == "" {
            return nil
        }
        if err := s.SendInput(ctx, SendInputRequest{
            PaneID: pane.ID,
            Text:   initialInput,
        }); err != nil {
            return fmt.Errorf("send initial input to pane %q: %w", pane.ID, err)
        }
        return nil
    }

- [ ] **Step 6: Deliver input for the first pane**

Immediately after appending response.Pane to createdTabPanes and createdAll:

    if err := s.sendExecutionPlanInitialInput(ctx, response.Pane, firstSpec.InitialInput); err != nil {
        _ = s.rollbackExecutionPlan(ctx, createdAll)
        return ApplyExecutionPlanResponse{}, err
    }

- [ ] **Step 7: Deliver input for remaining panes and retain failed panes for rollback**

After a remaining CreatePane succeeds:

    if err := s.sendExecutionPlanInitialInput(ctx, response.Pane, spec.InitialInput); err != nil {
        cancel()
        results <- executionPlanPaneResult{index: i, pane: response.Pane, err: err}
        return
    }

Replace the result loop with:

    for result := range results {
        if result.pane.ID != "" {
            panes[result.index] = result.pane
            created = append(created, result.pane)
        }
        if result.err != nil && firstErr == nil {
            firstErr = result.err
        }
    }

- [ ] **Step 8: Format and verify GREEN**

    gofmt -w internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go
    go test ./internal/runtime -run 'InitialInput' -count=1

Expected: all initial-input tests PASS.

- [ ] **Step 9: Run runtime and race suites**

    go test ./internal/runtime -count=1
    ./scripts/test-race-core.sh

Expected: both commands exit 0 without test or race failures.

- [ ] **Step 10: Commit**

    git add internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go
    git commit -m "feat: deliver execution plan initial input"

---

### Task 3: Put the Work Goal in the Coder Pane

**Files:**
- Modify: internal/work/work.go:43
- Modify: internal/work/work_test.go
- Modify: internal/cli/work/work_test.go:14

**Interfaces:**
- Consumes: transport.ExecutionPlanPane.InitialInput.
- Produces: coder initial input containing the normalized goal; all other work panes remain empty.

- [ ] **Step 1: Write a failing plan-builder test**

Append to internal/work/work_test.go:

    func TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal(t *testing.T) {
        payload, err := BuildPlan(PlanRequest{
            Goal: "  fix the parser  ",
            CWD:  "/tmp/app",
        })
        if err != nil {
            t.Fatalf("BuildPlan() error = %v", err)
        }

        panes := payload.Tabs[0].Panes
        if got := panes[0].InitialInput; got != "fix the parser" {
            t.Fatalf("coder InitialInput = %q, want exact trimmed goal", got)
        }
        if strings.HasSuffix(panes[0].InitialInput, "\n") {
            t.Fatalf("coder InitialInput = %q, want no newline", panes[0].InitialInput)
        }
        for _, pane := range panes[1:] {
            if pane.InitialInput != "" {
                t.Fatalf("pane %q InitialInput = %q, want coder-only prefill", pane.ID, pane.InitialInput)
            }
        }
    }

- [ ] **Step 2: Extend the dry-run test before implementation**

In TestRunDryRunPrintsExecutionPlanEnvelope, add:

    if got := payload.Tabs[0].Panes[0].InitialInput; got != "implement work command" {
        t.Fatalf("coder InitialInput = %q, want dry-run goal", got)
    }
    for _, pane := range payload.Tabs[0].Panes[1:] {
        if pane.InitialInput != "" {
            t.Fatalf("pane %q InitialInput = %q, want coder-only prefill", pane.ID, pane.InitialInput)
        }
    }

- [ ] **Step 3: Verify RED**

    go test ./internal/work -run '^TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal$' -count=1
    go test ./internal/cli/work -run '^TestRunDryRunPrintsExecutionPlanEnvelope$' -count=1

Expected: both tests FAIL because coder InitialInput is empty.

- [ ] **Step 4: Set coder initial input**

Change the coder pane literal in internal/work/work.go to:

    {
        ID:           "coder",
        Role:         "coding-agent",
        CWD:          cwd,
        Command:      append(append([]string{}, roleCommand...), "coding-agent", cwd),
        InitialInput: goal,
    },

Do not change the coding-agent command or append the goal as a command argument.

- [ ] **Step 5: Format and verify GREEN**

    gofmt -w internal/work/work.go internal/work/work_test.go internal/cli/work/work_test.go
    go test ./internal/work -run '^TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal$' -count=1
    go test ./internal/cli/work -run '^TestRunDryRunPrintsExecutionPlanEnvelope$' -count=1

Expected: both tests PASS.

- [ ] **Step 6: Run complete work suites**

    go test ./internal/work ./internal/cli/work ./cmd/zellij-agent -count=1

Expected: all packages PASS.

- [ ] **Step 7: Commit**

    git add internal/work/work.go internal/work/work_test.go internal/cli/work/work_test.go
    git commit -m "feat: prefill work coder goal"

---

### Task 4: Document and Verify Product Behavior

**Files:**
- Modify: README.md:82
- Modify: docs/manual-smoke-test.md
- Modify: docs/next-steps-todolist.md:74

**Interfaces:**
- Consumes: the implemented initial_input behavior and rebuilt bin/zellij-agent.
- Produces: user documentation, a smoke procedure, and the updated P2 checklist.

- [ ] **Step 1: Update README**

Replace the coder bullet with:

    - `coder`: interactive Codex session through `zellij-agent role coding-agent <cwd>`, with the goal prefilled for review; press Enter to submit it.

Add after the pane list:

    The coder pane receives the exact trimmed goal without an Enter key. Review or edit the text in Codex, then press Enter when you want the coding session to begin. `--dry-run` exposes the value as the coder pane's `initial_input` without creating a workspace.

- [ ] **Step 2: Add the manual smoke flow**

Append this exact section to docs/manual-smoke-test.md:

    ## Work Goal Prefill Smoke

    Build and register the unified binary as described above, then run these commands inside a real Zellij session with the daemon serving `/tmp/agentd.sock`:

    ```bash
    zellij-agent work --dry-run --session goal-prefill-smoke "fix the parser"
    zellij-agent work --session goal-prefill-smoke "fix the parser"
    ```

    In the dry-run JSON, confirm the `coder` pane contains `"initial_input": "fix the parser"` and the other panes omit `initial_input`.

    In the created coder pane, confirm `fix the parser` is visible in the Codex input field and no response begins automatically. Edit the text if desired, press Enter once, and confirm Codex begins only then.

    Clean up the managed workspace:

    ```bash
    zellij-agent ctl cleanup --task goal-prefill-smoke
    ```

- [ ] **Step 3: Mark only the delivered P2 item**

Change the first Phase A checkbox in docs/next-steps-todolist.md to:

    - [x] Deliver the supplied goal to the interactive coder pane as its initial prompt. The goal is prefilled without Enter so the user reviews and submits it.

Leave all other Phase A and Phase B checkboxes unchanged.

- [ ] **Step 4: Run focused verification**

    gofmt -w internal/transport/types.go internal/transport/types_test.go internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go internal/work/work.go internal/work/work_test.go internal/cli/work/work_test.go
    go test ./internal/transport ./internal/runtime ./internal/work ./internal/cli/work ./cmd/zellij-agent -count=1

Expected: all listed packages PASS.

- [ ] **Step 5: Run full and race verification**

    go test ./... -count=1
    ./scripts/test-race-core.sh
    git diff --check

Expected: all commands exit 0 without failures, races, or whitespace errors.

- [ ] **Step 6: Build and immediately register the unified binary**

Run consecutively as required by AGENTS.md:

    go build -o bin/zellij-agent ./cmd/zellij-agent
    cp bin/zellij-agent ~/.config/custom-cli

Expected: both commands exit 0.

- [ ] **Step 7: Verify the rebuilt dry-run contract**

    ./bin/zellij-agent work --dry-run --session goal-prefill-smoke "fix the parser"

Expected: valid JSON with initial_input only on coder and no daemon connection attempt.

- [ ] **Step 8: Perform real-Zellij smoke when available**

Follow Work Goal Prefill Smoke in docs/manual-smoke-test.md. Record whether it ran. If no interactive Zellij session is available, report it as pending rather than passed.

- [ ] **Step 9: Commit documentation**

    git add README.md docs/manual-smoke-test.md docs/next-steps-todolist.md
    git commit -m "docs: document work goal prefill"

- [ ] **Step 10: Inspect final state**

    git status --short
    git log -5 --oneline --decorate

Expected: no uncommitted implementation files and separate commits for contract, runtime delivery, work wiring, and docs.

---

### Task 5: Wait for the Codex Input Prompt Before Prefill

**Files:**
- Modify: internal/transport/types.go
- Modify: internal/transport/types_test.go
- Modify: internal/runtime/execution_plan.go
- Modify: internal/runtime/execution_plan_test.go
- Modify: internal/runtime/service_test.go
- Modify: internal/work/work.go
- Modify: internal/work/work_test.go
- Modify: internal/cli/work/work.go
- Modify: internal/cli/work/work_test.go
- Modify: README.md
- Modify: docs/manual-smoke-test.md

**Interfaces:**
- Consumes: ExecutionPlanPane.InitialInput, Backend.DumpScreen, Service.SendInput, and the request context created by work CLI.
- Produces: ExecutionPlanPane.InitialInputReadyText string, ExecutionPlanPaneSpec.InitialInputReadyText string, readiness-gated initial input, and rollback that remains usable after request timeout.

- [ ] **Step 1: Write the failing transport contract assertion**

Add this field to the source pane in TestExecutionPlanPayloadToRuntimePreservesNestedPayload:

    InitialInputReadyText: "›",

Add this condition to the converted-pane assertion:

    pane.InitialInputReadyText != "›"

- [ ] **Step 2: Verify transport RED**

    go test ./internal/transport -run '^TestExecutionPlanPayloadToRuntimePreservesNestedPayload$' -count=1

Expected: build failure because transport and runtime pane specs do not define InitialInputReadyText.

- [ ] **Step 3: Add and convert the readiness field**

Add to transport.ExecutionPlanPane:

    InitialInputReadyText string `json:"initial_input_ready_text,omitempty"`

Add to runtime.ExecutionPlanPaneSpec:

    InitialInputReadyText string

Add to ExecutionPlanPane.ToRuntime:

    InitialInputReadyText: pane.InitialInputReadyText,

- [ ] **Step 4: Verify transport GREEN**

    gofmt -w internal/transport/types.go internal/transport/types_test.go internal/runtime/execution_plan.go
    go test ./internal/transport -run '^TestExecutionPlanPayloadToRuntimePreservesNestedPayload$' -count=1

Expected: PASS.

- [ ] **Step 5: Write failing work-plan and CLI timeout assertions**

Extend TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal:

    if got := panes[0].InitialInputReadyText; got != "›" {
        t.Fatalf("coder InitialInputReadyText = %q, want Codex prompt marker", got)
    }
    for _, pane := range panes[1:] {
        if pane.InitialInputReadyText != "" {
            t.Fatalf("pane %q InitialInputReadyText = %q, want coder-only readiness", pane.ID, pane.InitialInputReadyText)
        }
    }

Extend TestRunDryRunPrintsExecutionPlanEnvelope:

    if got := bytes.Count(stdout.Bytes(), []byte(`"initial_input_ready_text"`)); got != 1 {
        t.Fatalf("dry-run initial_input_ready_text keys = %d, want coder-only JSON field", got)
    }
    if got := payload.Tabs[0].Panes[0].InitialInputReadyText; got != "›" {
        t.Fatalf("coder InitialInputReadyText = %q, want Codex prompt marker", got)
    }

Extend TestRunHelpPrintsUsageToStdout to require the default timeout text:

    !strings.Contains(stdout.String(), "default 15s")

- [ ] **Step 6: Verify work/CLI RED**

    go test ./internal/work -run '^TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal$' -count=1
    go test ./internal/cli/work -run 'TestRun(DryRunPrintsExecutionPlanEnvelope|HelpPrintsUsageToStdout)' -count=1

Expected: tests FAIL because the marker is empty and help still reports 10 seconds.

- [ ] **Step 7: Add the work marker and 15-second default**

Add to the coder pane literal in internal/work/work.go:

    InitialInputReadyText: "›",

Change the work flag default and usage text in internal/cli/work/work.go:

    timeout := fs.Duration("timeout", 15*time.Second, "request timeout")

    fmt.Fprintln(w, "    \trequest timeout (default 15s)")

- [ ] **Step 8: Verify work/CLI GREEN**

    gofmt -w internal/work/work.go internal/work/work_test.go internal/cli/work/work.go internal/cli/work/work_test.go
    go test ./internal/work -run '^TestBuildPlanPrefillsOnlyCoderWithTrimmedGoal$' -count=1
    go test ./internal/cli/work -run 'TestRun(DryRunPrintsExecutionPlanEnvelope|HelpPrintsUsageToStdout)' -count=1

Expected: PASS.

- [ ] **Step 9: Extend the runtime fake backend for readiness sequences**

Add these fields to fakeBackend in internal/runtime/service_test.go:

    dumpOutputs      []string
    dumpErrors       []error
    closeContextErrs []error

In DumpScreen, after recording the request, shift one configured output and error when present; otherwise retain dumpOutput and dumpErr:

    output := b.dumpOutput
    if len(b.dumpOutputs) > 0 {
        output = b.dumpOutputs[0]
        b.dumpOutputs = b.dumpOutputs[1:]
    }
    err := b.dumpErr
    if len(b.dumpErrors) > 0 {
        err = b.dumpErrors[0]
        b.dumpErrors = b.dumpErrors[1:]
    }
    return output, err

Change fakeBackend.ClosePane to record ctx.Err before returning:

    b.closeContextErrs = append(b.closeContextErrs, ctx.Err())

- [ ] **Step 10: Write failing readiness and timeout rollback tests**

Add TestApplyExecutionPlanWaitsForInitialInputReadyText. Configure dumpOutputs as `[]string{"starting", "OpenAI Codex\n›"}`, apply a one-pane plan with input and marker, and assert two dump requests followed by one exact SendInput request.

Add TestApplyExecutionPlanReadinessTimeoutRollsBackWithFreshContext. Use context.WithTimeout with 20 milliseconds, keep dumpOutput at `"starting"`, and assert:

    err != nil && strings.Contains(err.Error(), `wait for initial input readiness in pane "coder"`)
    len(backend.closeRequests) == 1
    len(backend.closeContextErrs) == 1 && backend.closeContextErrs[0] == nil
    len(service.ListPanes(...).Panes) == 0

- [ ] **Step 11: Verify runtime RED**

    go test ./internal/runtime -run 'InitialInput(ReadyText|ReadinessTimeout)' -count=1

Expected: readiness sequencing fails because input is sent immediately, and timeout cleanup fails because rollback receives a canceled context.

- [ ] **Step 12: Implement condition-based readiness waiting**

Add imports strings and time, then add:

    const executionPlanInitialInputPollInterval = 50 * time.Millisecond

    func (s *Service) waitForExecutionPlanInitialInputReady(ctx context.Context, pane Pane, readyText string) error {
        if readyText == "" {
            return nil
        }
        ticker := time.NewTicker(executionPlanInitialInputPollInterval)
        defer ticker.Stop()

        var lastErr error
        for {
            output, err := s.backend.DumpScreen(ctx, zellij.DumpScreenRequest{
                PaneID: zellij.PaneID(pane.ZellijPaneID),
            })
            if err == nil && strings.Contains(output, readyText) {
                return nil
            }
            if err != nil {
                lastErr = err
            }

            select {
            case <-ctx.Done():
                cause := ctx.Err()
                if lastErr != nil {
                    cause = errors.Join(cause, lastErr)
                }
                return fmt.Errorf("wait for initial input readiness in pane %q: %w", pane.ID, cause)
            case <-ticker.C:
            }
        }
    }

Change sendExecutionPlanInitialInput to accept readyText and call the helper before SendInput:

    func (s *Service) sendExecutionPlanInitialInput(ctx context.Context, pane Pane, initialInput, readyText string) error

Update both call sites to pass spec.InitialInputReadyText.

- [ ] **Step 13: Make rollback independent of request cancellation**

At the start of rollbackExecutionPlan, create and use a bounded cleanup context:

    rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
    defer cancel()

Use rollbackCtx for ClosePane calls. Registry removal remains unchanged.

- [ ] **Step 14: Verify runtime GREEN and race safety**

    gofmt -w internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go internal/runtime/service_test.go
    go test ./internal/runtime -run 'InitialInput(ReadyText|ReadinessTimeout)' -count=1
    go test ./internal/runtime -count=1
    ./scripts/test-race-core.sh

Expected: all commands exit 0.

- [ ] **Step 15: Update documentation for readiness and hyphen-goal smoke**

Update README.md to say the launcher waits up to the configured timeout for the Codex input prompt before pasting.

Extend Work Goal Prefill Smoke in docs/manual-smoke-test.md with:

    zellij-agent work --session hyphen-goal-smoke -- --help

Require the coder snapshot to show `--help` without automatic submission, then clean up task hyphen-goal-smoke.

- [ ] **Step 16: Run final verification and rebuild**

    go test ./... -count=1
    ./scripts/test-race-core.sh
    git diff --check
    go build -o bin/zellij-agent ./cmd/zellij-agent
    cp bin/zellij-agent ~/.config/custom-cli

Expected: all commands exit 0 and the rebuilt binary is registered immediately after build.

- [ ] **Step 17: Perform real-Zellij smoke with an isolated daemon socket**

Start the rebuilt daemon on a temporary socket without disturbing the existing daemon. Run normal and hyphen-goal work commands, capture coder snapshots, and assert exact prefill with no Codex response. Clean both tasks, stop the temporary daemon, and remove its socket.

Do not commit. The user will inspect and commit the final working tree.

---

## Plan Self-Review

- Spec coverage: Tasks 1-5 cover optional JSON, conversion, exact coder-only goal, no newline, readiness polling, timeout-safe rollback, leading-hyphen input, dry-run visibility, docs, build registration, and manual verification.
- Scope: the plan excludes project detection, command selection, optional-tool checks, launcher overrides, and feedback-loop behavior.
- Type consistency: both layers use InitialInput string; JSON uses initial_input; runtime passes it through SendInputRequest.Text.
- Rollback consistency: the first pane is recorded before delivery, and a remaining-pane delivery error returns its created pane to the existing rollback path.
- Readiness consistency: work supplies marker `›`; transport and runtime preserve InitialInputReadyText; runtime waits through Backend.DumpScreen before SendInput; work context defaults to 15 seconds.
