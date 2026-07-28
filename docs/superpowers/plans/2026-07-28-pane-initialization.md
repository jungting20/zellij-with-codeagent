# Single Pane Initialization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `CreatePane` optionally wait for input readiness and deliver initial input before reporting success, then use that guarantee for execution plans and ticket-manager coding-agent workers.

**Architecture:** Extend the existing runtime and transport `CreatePaneRequest` instead of adding a second endpoint. Keep pane creation deduplication as the transaction boundary, centralize generation-safe initialization and rollback in runtime, map complete and partial rollback outcomes to distinct transport errors, and leave ticket lifecycle recovery in ticket-manager.

**Tech Stack:** Go standard library (`context`, `errors`, `time`, `testing`), existing runtime registry/Zellij backend, Unix-socket HTTP transport, ticket-worker manager state machine.

## Global Constraints

- Preserve existing `CreatePane` behavior and response timing when `InitialInput` is empty.
- Treat initial input delivery as part of successful pane creation when `InitialInput` is non-empty.
- Use `initial_input` and `initial_input_ready_text` as transport JSON field names.
- Use `ErrPaneInitializationFailed` in runtime and `CodeInitializationFailed` with wire value `initialization_failed` in transport.
- Return `cleanup_partial` instead of `initialization_failed` when rollback is incomplete.
- Keep registry identity after a physical close failure so ticket-manager can inspect and clean up the pane.
- Never call Zellij directly from ticket-manager, planners, or clients.
- Do not add a default role because this is background worker lifecycle logic.
- Preserve the unrelated untracked file `docs/agent-status-detection.md`.
- Write every commit message in Korean.
- Follow TDD for new behavior and run `gofmt` on every edited Go file.
- Finish with `go test ./...`, rebuild the unified binary, and register it with the documented atomic rename.

---

### Task 1: Add the Successful Runtime Initialization Path

**Files:**
- Modify: `internal/runtime/types.go:103-119`
- Modify: `internal/runtime/service.go:87-123`
- Create: `internal/runtime/pane_initialization.go`
- Create: `internal/runtime/pane_initialization_test.go`

**Interfaces:**
- Consumes: `Service.createPaneOnce`, `registry.Registry.GetPane`, `zellij.Backend.DumpScreen`, and `zellij.Backend.SendInput`.
- Produces: `CreatePaneRequest.InitialInput string`, `CreatePaneRequest.InitialInputReadyText string`, and `Service.initializeCreatedPane(context.Context, CreatePaneResponse, string, string) error`.

- [ ] **Step 1: Write failing successful-path tests**

Create `pane_initialization_test.go` with these tests:

```go
func TestCreatePaneWaitsForReadyTextBeforeInitialInput(t *testing.T) {
	backend := &fakeBackend{
		createID: "terminal_ready",
		dumpOutputs: []string{"starting", "OpenAI Codex\n›"},
	}
	service := newTestService(backend)

	response, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a",
		InitialInput: "implement ticket\n", InitialInputReadyText: "›",
	})
	if err != nil {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if response.Pane.ID != "coder" || len(backend.dumpRequests) != 2 {
		t.Fatalf("response/dumps = %#v/%d", response.Pane, len(backend.dumpRequests))
	}
	want := []zellij.SendInputRequest{{
		Session: "physical-a", PaneID: "terminal_ready", Text: "implement ticket\n",
	}}
	if !reflect.DeepEqual(backend.sendRequests, want) {
		t.Fatalf("SendInput requests = %#v, want %#v", backend.sendRequests, want)
	}
}

func TestCreatePaneSendsInitialInputImmediatelyWithoutReadyText(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_immediate"}
	service := newTestService(backend)
	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.dumpRequests) != 0 || len(backend.sendRequests) != 1 {
		t.Fatalf("dump/send = %d/%d, want 0/1", len(backend.dumpRequests), len(backend.sendRequests))
	}
}

func TestCreatePaneIgnoresReadyTextWithoutInitialInput(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_empty"}
	service := newTestService(backend)
	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInputReadyText: "›",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.dumpRequests) != 0 || len(backend.sendRequests) != 0 {
		t.Fatalf("dump/send = %d/%d, want 0/0", len(backend.dumpRequests), len(backend.sendRequests))
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

```bash
go test ./internal/runtime -run '^TestCreatePane(WaitsForReadyTextBeforeInitialInput|SendsInitialInputImmediatelyWithoutReadyText|IgnoresReadyTextWithoutInitialInput)$' -count=1
```

Expected: build failure because the request fields do not exist.

- [ ] **Step 3: Add request fields and the successful initializer**

Add to `runtime.CreatePaneRequest`:

```go
InitialInput          string
InitialInputReadyText string
```

Create `pane_initialization.go` with a 50ms polling loop. `initializeCreatedPane` must:

1. return immediately for empty input;
2. poll `DumpScreen` until `strings.Contains(output, readyText)` when ready text is present;
3. re-read the registry record;
4. reject a changed generation with `registry.ErrStaleRecord`;
5. send the exact input to the created record's session and Zellij pane.

Use these exact signatures:

```go
const paneInitialInputPollInterval = 50 * time.Millisecond

func (s *Service) initializeCreatedPane(
	ctx context.Context,
	created CreatePaneResponse,
	initialInput string,
	readyText string,
) error

func (s *Service) waitForPaneInitialInputReady(
	ctx context.Context,
	created CreatePaneResponse,
	readyText string,
) error
```

In `Service.CreatePane`, call initialization before `finishCreatePane`:

```go
response, createErr := s.createPaneOnce(ctx, req, id)
if createErr == nil {
	createErr = s.initializeCreatedPane(ctx, response, req.InitialInput, req.InitialInputReadyText)
}
s.finishCreatePane(call, response, createErr)
return response, createErr
```

- [ ] **Step 4: Verify GREEN and add deduplication coverage**

Add `TestCreatePaneSharesInitialInputResultForConcurrentIdenticalRequest`. Block the first `DumpScreen` call with channels, start two identical calls, release readiness, and assert two successful responses but one backend create and one input send.

```bash
gofmt -w internal/runtime/types.go internal/runtime/service.go internal/runtime/pane_initialization.go internal/runtime/pane_initialization_test.go
go test ./internal/runtime -run '^TestCreatePane(WaitsForReadyTextBeforeInitialInput|SendsInitialInputImmediatelyWithoutReadyText|IgnoresReadyTextWithoutInitialInput|SharesInitialInputResultForConcurrentIdenticalRequest)$' -count=1
```

Expected: `PASS`.

- [ ] **Step 5: Run runtime tests and commit**

```bash
go test ./internal/runtime -count=1
git add internal/runtime/types.go internal/runtime/service.go internal/runtime/pane_initialization.go internal/runtime/pane_initialization_test.go
git commit -m "feat: pane 초기 입력 성공 경로 추가"
```

---

### Task 2: Add Strong Rollback and Runtime Error Semantics

**Files:**
- Modify: `internal/runtime/types.go:12-20`
- Modify: `internal/runtime/service.go:120-122`
- Modify: `internal/runtime/pane_initialization.go`
- Modify: `internal/runtime/pane_initialization_test.go`

**Interfaces:**
- Consumes: `CreatePaneResponse.record`, `SubscriptionManager.StopPaneGeneration`, and `registry.RemovePaneGeneration`.
- Produces: `ErrPaneInitializationFailed`, `Service.cleanupCreatedPane(context.Context, registry.PaneRecord) error`, and `paneInitializationError(error, error) error`.

- [ ] **Step 1: Write failing rollback tests**

Add:

```go
func TestCreatePaneInitialInputFailureRollsBack(t *testing.T) {
	backend := &fakeBackend{createID: "terminal_failed", sendErr: errors.New("paste failed")}
	service := newTestService(backend)

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n",
	})
	if !errors.Is(err, ErrPaneInitializationFailed) {
		t.Fatalf("CreatePane() error = %v", err)
	}
	if len(backend.closeRequests) != 1 || backend.closeRequests[0].PaneID != "terminal_failed" {
		t.Fatalf("ClosePane requests = %#v", backend.closeRequests)
	}
	if _, getErr := service.registry.GetPane("coder"); !errors.Is(getErr, registry.ErrNotFound) {
		t.Fatalf("GetPane() error = %v, want not found", getErr)
	}
}

func TestCreatePaneInitializationCleanupFailurePreservesRegistry(t *testing.T) {
	backend := &fakeBackend{
		createID: "terminal_leaked",
		sendErr: errors.New("paste failed"),
		closeErr: errors.New("close failed"),
	}
	service := newTestService(backend)

	_, err := service.CreatePane(context.Background(), CreatePaneRequest{
		ID: "coder", ZellijSession: "physical-a", InitialInput: "go\n",
	})
	if !errors.Is(err, ErrPaneInitializationFailed) || !errors.Is(err, ErrCleanupPartial) {
		t.Fatalf("CreatePane() error = %v", err)
	}
	record, getErr := service.registry.GetPane("coder")
	if getErr != nil || record.ZellijPaneID != "terminal_leaked" {
		t.Fatalf("record/error = %#v/%v", record, getErr)
	}
}
```

Also add:

- `TestCreatePaneReadinessTimeoutRollsBackWithFreshContext`: a 20ms request timeout, output that never contains `›`, one close request, `closeContextErrs[0] == nil`, empty registry.
- `TestCreatePaneInitializationRollbackStopsSubscription`: use `scriptedSubscriptionRunner`, cancel while readiness is pending, then assert the subscription entry and registry record are removed.

- [ ] **Step 2: Run rollback tests and verify RED**

```bash
go test ./internal/runtime -run '^TestCreatePane(InitialInputFailureRollsBack|InitializationCleanupFailurePreservesRegistry|ReadinessTimeoutRollsBackWithFreshContext|InitializationRollbackStopsSubscription)$' -count=1
```

Expected: failures because initialization errors currently leave the pane registered.

- [ ] **Step 3: Implement typed rollback**

Add:

```go
ErrPaneInitializationFailed = errors.New("runtime pane initialization failed")
```

Implement:

```go
func (s *Service) cleanupCreatedPane(ctx context.Context, record registry.PaneRecord) error {
	if err := s.backend.ClosePane(ctx, zellij.ClosePaneRequest{
		Session: string(record.SessionID),
		PaneID:  zellij.PaneID(record.ZellijPaneID),
	}); err != nil {
		return err
	}
	if s.subs != nil {
		s.subs.StopPaneGeneration(record.ID, record.Generation)
	}
	if _, err := s.registry.RemovePaneGeneration(record.ID, record.Generation); err != nil &&
		!errors.Is(err, registry.ErrNotFound) &&
		!errors.Is(err, registry.ErrStaleRecord) {
		return err
	}
	return nil
}

func paneInitializationError(cause, cleanupErr error) error {
	initializationErr := errors.Join(ErrPaneInitializationFailed, cause)
	if cleanupErr == nil {
		return initializationErr
	}
	return errors.Join(initializationErr, fmt.Errorf("%w: %v", ErrCleanupPartial, cleanupErr))
}
```

On initialization failure, create a `context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)`, call `cleanupCreatedPane`, clear the response, and pass `paneInitializationError` to `finishCreatePane`. Preserve the existing create-call cache behavior for `ErrCleanupPartial`.

- [ ] **Step 4: Verify GREEN and generation identity**

Add tests that replace the registry record before input and use different initial input with the same reserved pane ID. Assert no input reaches the replacement, the stale error is wrapped by `ErrPaneInitializationFailed`, and differing requests receive `registry.ErrAlreadyExists`.

```bash
gofmt -w internal/runtime/types.go internal/runtime/service.go internal/runtime/pane_initialization.go internal/runtime/pane_initialization_test.go
go test ./internal/runtime -run '^TestCreatePane(InitialInputFailureRollsBack|InitializationCleanupFailurePreservesRegistry|ReadinessTimeoutRollsBackWithFreshContext|InitializationRollbackStopsSubscription|InitialInputDoesNotReachReusedPane|RejectsDifferentInitialInputForReservedLogicalID)$' -count=1
go test ./internal/runtime -count=1
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/types.go internal/runtime/service.go internal/runtime/pane_initialization.go internal/runtime/pane_initialization_test.go
git commit -m "feat: pane 초기화 실패 롤백 보장"
```

---

### Task 3: Expose Initialization Through Transport

**Files:**
- Modify: `internal/transport/types.go:12-25`
- Modify: `internal/transport/types.go:230-250`
- Modify: `internal/transport/types_test.go:22-55`
- Modify: `internal/transport/errors.go:14-72`
- Modify: `internal/transport/errors_test.go`
- Modify: `internal/transport/server_test.go`

**Interfaces:**
- Consumes: runtime initialization fields and runtime initialization/cleanup sentinels.
- Produces: JSON fields `initial_input`, `initial_input_ready_text`, and `CodeInitializationFailed`.

- [ ] **Step 1: Write failing transport tests**

Extend `TestCreatePaneRequestToRuntimePreservesPayloadFields` with:

```go
InitialInput:          "implement ticket\n",
InitialInputReadyText: "›",
```

Assert both converted values match. Add a server test that POSTs:

```json
{
  "id": "coder",
  "zellij_session": "physical-a",
  "initial_input": "implement ticket\n",
  "initial_input_ready_text": "›"
}
```

and asserts the fake runtime service receives both values.

Add:

```go
func TestErrorForPaneInitializationFailed(t *testing.T) {
	apiErr, status := ErrorFor(errors.Join(rt.ErrPaneInitializationFailed, errors.New("prompt timeout")))
	if status != http.StatusInternalServerError ||
		apiErr.Code != CodeInitializationFailed ||
		!apiErr.Retryable {
		t.Fatalf("status/error = %d/%#v", status, apiErr)
	}
}

func TestErrorForCleanupPartialOverridesInitializationFailure(t *testing.T) {
	err := errors.Join(rt.ErrPaneInitializationFailed, rt.ErrCleanupPartial, errors.New("close failed"))
	apiErr, status := ErrorFor(err)
	if status != http.StatusConflict || apiErr.Code != CodeCleanupPartial {
		t.Fatalf("status/error = %d/%#v", status, apiErr)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

```bash
go test ./internal/transport -run '^(TestCreatePaneRequestToRuntimePreservesPayloadFields|TestHandle.*CreatePane.*InitialInput|TestErrorForPaneInitializationFailed|TestErrorForCleanupPartialOverridesInitializationFailure)$' -count=1
```

Expected: build failures for missing transport fields and code.

- [ ] **Step 3: Implement transport fields and mapping**

Add to transport `CreatePaneRequest`:

```go
InitialInput          string `json:"initial_input,omitempty"`
InitialInputReadyText string `json:"initial_input_ready_text,omitempty"`
```

Copy both in `ToRuntime`. Add:

```go
CodeInitializationFailed ErrorCode = "initialization_failed"
```

In `ErrorFor`, keep cleanup before initialization:

```go
case errors.Is(err, rt.ErrCleanupPartial):
	return APIError{Code: CodeCleanupPartial, Message: err.Error(), Retryable: true}, http.StatusConflict
case errors.Is(err, rt.ErrPaneInitializationFailed):
	return APIError{Code: CodeInitializationFailed, Message: err.Error(), Retryable: true}, http.StatusInternalServerError
```

- [ ] **Step 4: Verify and commit**

```bash
gofmt -w internal/transport/types.go internal/transport/types_test.go internal/transport/errors.go internal/transport/errors_test.go internal/transport/server_test.go
go test ./internal/transport -count=1
git add internal/transport/types.go internal/transport/types_test.go internal/transport/errors.go internal/transport/errors_test.go internal/transport/server_test.go
git commit -m "feat: pane 초기화 전송 계약 추가"
```

---

### Task 4: Route Execution Plans Through the Common Initializer

**Files:**
- Modify: `internal/runtime/execution_plan.go`
- Modify: `internal/runtime/execution_plan_test.go`
- Modify: `internal/runtime/pane_initialization_test.go`

**Interfaces:**
- Consumes: extended runtime `CreatePaneRequest` and `Service.cleanupCreatedPane`.
- Produces: unchanged execution-plan results with no execution-plan-specific ready/input implementation.

- [ ] **Step 1: Run characterization tests while GREEN**

```bash
go test ./internal/runtime -run '^(TestApplyExecutionPlan|TestRollbackExecutionPlan|TestExecutionPlanInitialInput)' -count=1
```

Expected: `PASS`.

- [ ] **Step 2: Forward initialization fields**

Add these fields to both first-pane and remaining-pane `CreatePaneRequest` literals:

```go
InitialInput:          spec.InitialInput,
InitialInputReadyText: spec.InitialInputReadyText,
```

Use `firstSpec` for the first pane. Remove successful-path calls to `sendExecutionPlanInitialInput`; `CreatePane` now returns only after initialization.

- [ ] **Step 3: Remove duplicate implementation and reuse cleanup**

Delete:

- `executionPlanInitialInputPollInterval`
- `sendExecutionPlanInitialInput`
- `waitForExecutionPlanInitialInputReady`

Change `rollbackExecutionPlan` to create one 5-second context and call `cleanupCreatedPane` for each successful record:

```go
func (s *Service) rollbackExecutionPlan(ctx context.Context, created []createdExecutionPlanPane) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var rollbackErr error
	for _, pane := range created {
		rollbackErr = errors.Join(rollbackErr, s.cleanupCreatedPane(rollbackCtx, pane.record))
	}
	return rollbackErr
}
```

Only successful `CreatePane` responses enter the plan rollback list. A current pane whose initialization failed has already rolled itself back.

- [ ] **Step 4: Update tests and verify no double rollback**

Change error assertions from `send initial input to pane` to `initialize pane`. Keep exact close sets in the initial-input failure tests; they prove every physical pane closes exactly once.

Move `TestExecutionPlanInitialInputDoesNotReachReusedPane` to `pane_initialization_test.go` under the name `TestCreatePaneInitialInputDoesNotReachReusedPane`.

```bash
gofmt -w internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go internal/runtime/pane_initialization_test.go
go test ./internal/runtime -run '^(TestApplyExecutionPlan|TestRollbackExecutionPlan|TestCreatePaneInitialInputDoesNotReachReusedPane)' -count=1
go test ./internal/runtime -count=1
```

Expected: all tests pass and ready/input logic exists only in `pane_initialization.go`.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go internal/runtime/pane_initialization_test.go
git commit -m "refactor: 실행 계획 pane 초기화 통합"
```

---

### Task 5: Make Ticket-manager Use Atomic Worker Initialization

**Files:**
- Modify: `internal/ticketworker/manager.go:25-32`
- Modify: `internal/ticketworker/manager.go:319-414`
- Modify: `internal/ticketworker/manager_test.go`

**Interfaces:**
- Consumes: transport initial-input fields and `CodeInitializationFailed`.
- Produces: a worker slot that becomes `working` only after runtime initialization success.

- [ ] **Step 1: Write failing manager tests**

In `TestManagerWaitsForAnchorThenFillsConfiguredCapacity`, wait for two create calls instead of input calls and assert:

```go
if req.InitialInputReadyText != "›" {
	t.Fatalf("create[%d] ready text = %q", i, req.InitialInputReadyText)
}
if !strings.HasSuffix(req.InitialInput, "\n") ||
	!strings.Contains(req.InitialInput, "Implement ticket.") {
	t.Fatalf("create[%d] initial input = %q", i, req.InitialInput)
}
```

In `TestManagerIgnoresPromptEchoAndCompletesExactMarker`, use `client.created()[0].InitialInput` as the echoed prompt.

Replace the old input-failure test with:

```go
func TestManagerInitializationFailureRequeuesWithoutClose(t *testing.T) {
	store := &fakeManagerStore{ready: []Ticket{managerTicket(10)}}
	client := newFakeManagerClient()
	client.createErrors = []error{&transport.ClientError{APIError: transport.APIError{
		Code: transport.CodeInitializationFailed, Message: "prompt failed", Retryable: true,
	}}}
	client.streams = []*fakeEventStream{newFakeEventStream()}
	manager := newTestManager(t, store, client, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := runManager(ctx, manager)
	waitFor(t, func() bool { return len(store.requeues()) == 1 })
	if len(client.closed()) != 0 {
		t.Fatalf("closed panes = %v, want none", client.closed())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

Add:

- `TestManagerCleanupPartialKeepsUncertainRecovery`: return `CodeCleanupPartial` with `createOnError = true`, tick once, then assert discovery, one close, one requeue, and no second create.
- `TestManagerCreateUsesStartupTimeout`: record `ctx.Deadline()` in fake `CreatePane` and assert it is bounded by `StartupTimeout`.

- [ ] **Step 2: Run tests and verify RED**

```bash
go test ./internal/ticketworker -run '^TestManager(WaitsForAnchorThenFillsConfiguredCapacity|IgnoresPromptEchoAndCompletesExactMarker|InitializationFailureRequeuesWithoutClose|CleanupPartialKeepsUncertainRecovery|CreateUsesStartupTimeout)$' -count=1
```

Expected: failures because create requests lack initialization fields and manager still sends input separately.

- [ ] **Step 3: Collapse worker start into one request**

Remove `SendInput` from `ManagerClient` but keep `SnapshotOutput`. Build:

```go
req := transport.CreatePaneRequest{
	ID: slot.paneID, TaskID: m.taskID, ZellijSession: m.zellijSession,
	Role: "coding-agent", Name: workerPaneName(ticket), SameTabAsPaneID: m.anchorPaneID,
	Command: []string{m.roleBin, "role", "coding-agent", "--yolo", m.root}, CWD: m.root,
	InitialInput: slot.prompt + "\n", InitialInputReadyText: "›",
}
```

Use:

```go
createCtx, cancel := context.WithTimeout(ctx, m.startupTimeout)
_, createErr := m.client.CreatePane(createCtx, req)
cancel()
```

Preserve the existing safe/uncertain branches. On success, set `paneCreated` and `managerSlotWorking`, then log `started`. Delete `waitForInputReady` and the separate `SendInput` block.

Extend safe failures:

```go
return clientErr.APIError.Code == transport.CodeBadRequest ||
	clientErr.APIError.Code == transport.CodeNotFound ||
	clientErr.APIError.Code == transport.CodeInitializationFailed
```

- [ ] **Step 4: Simplify fake client and verify**

Delete `fakeInput`, `inputRequests`, `inputErrors`, fake `SendInput`, and `inputs()`. Add deadline recording without changing existing create-error and `createOnError` behavior. Confirm `slot.createRequest` retains initial input during uncertain retries.

```bash
gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go
go test ./internal/ticketworker -count=1
go test ./cmd/agent-role/ticketmanager -count=1
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git commit -m "feat: 티켓 worker 초기화를 단일 연산으로 전환"
```

---

### Task 6: Document, Verify, Build, and Register

**Files:**
- Modify: `README.md:260-280`

**Interfaces:**
- Consumes: final behavior from Tasks 1-5.
- Produces: documented runtime behavior and an atomically registered unified binary.

- [ ] **Step 1: Document optional initialization**

Add to the README `CreatePaneRequest` example:

```go
InitialInput:          "Run the assigned task.\n",
InitialInputReadyText: "›",
```

Add after the example:

```markdown
When `InitialInput` is set, `CreatePane` returns success only after the pane
shows `InitialInputReadyText` (when provided) and the runtime delivers the
input. Initialization failure rolls back the new pane; a `cleanup_partial`
error means callers must inspect and finish cleanup.
```

- [ ] **Step 2: Run focused and complete verification**

```bash
gofmt -w internal/runtime/types.go internal/runtime/service.go internal/runtime/pane_initialization.go internal/runtime/pane_initialization_test.go internal/runtime/execution_plan.go internal/runtime/execution_plan_test.go internal/transport/types.go internal/transport/types_test.go internal/transport/errors.go internal/transport/errors_test.go internal/transport/server_test.go internal/ticketworker/manager.go internal/ticketworker/manager_test.go
go test ./internal/runtime ./internal/transport ./internal/ticketworker ./cmd/agent-role/ticketmanager -count=1
go test ./...
```

Expected: all tests pass.

- [ ] **Step 3: Build the unified binary**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
```

Expected: build succeeds.

- [ ] **Step 4: Register the binary atomically**

```bash
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
```

Expected: the custom-cli binary is replaced through atomic rename.

- [ ] **Step 5: Commit documentation and inspect status**

```bash
git add README.md
git commit -m "docs: pane 초기화 성공 조건 설명"
git status --short
```

Expected: only pre-existing `docs/agent-status-detection.md` remains untracked. Report focused tests, `go test ./...`, build, and registration results.

