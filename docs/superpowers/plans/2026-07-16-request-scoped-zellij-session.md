# Request-Scoped Zellij Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route every daemon-managed pane and tab to the physical Zellij session resolved by the invoking CLI process.

**Architecture:** CLI entrypoints resolve `--zellij-session` first and otherwise read their own `ZELLIJ_SESSION_NAME`, then put the result in the transport request. Runtime and Zellij backend requests carry that value explicitly; registry records retain it so later input, snapshot, subscription, cleanup, rollback, and reconciliation operations return to the same physical session. A single daemon can therefore manage panes in multiple Zellij sessions without using the daemon process environment as a client fallback.

**Tech Stack:** Go 1.x, standard `flag`, `os`, `testing`, Unix-socket JSON transport, Zellij CLI backend

## Global Constraints

- Keep logical execution-plan `session` and pane `task_id` semantics unchanged.
- Name the physical session `zellij_session` in JSON and `--zellij-session` in CLI flags.
- Resolve the physical session in the calling CLI process: explicit flag, then `ZELLIJ_SESSION_NAME`, then a local error.
- Never use the daemon process's `ZELLIJ_SESSION_NAME` as an implicit client fallback.
- Route Zellij only through `RuntimeService` and the backend; planners and clients must not invoke Zellij directly.
- Preserve user changes and use standard Go formatting.
- After the final code change, run `go test ./...`, build `bin/zellij-agent`, and immediately copy it to `~/.config/custom-cli`.

## File Structure

- `internal/cli/zellij_session.go`: one shared CLI-side physical-session resolver.
- `internal/cli/zellij_session_test.go`: precedence, trimming, and missing-session tests.
- `internal/zellij/types.go`: session-bearing backend request types.
- `internal/zellij/backend.go`: choose the request session for every backend operation.
- `internal/zellij/commands.go`: continue rendering the chosen session as `zellij --session`.
- `internal/zellij/backend_test.go`: exact command-argument routing tests.
- `internal/runtime/types.go`: physical session on direct pane creation.
- `internal/runtime/execution_plan.go`: physical session on execution plans and rollbacks.
- `internal/runtime/service.go`: creation and pane-specific follow-up routing.
- `internal/runtime/cleanup.go`: session-specific cleanup routing.
- `internal/runtime/reconcile.go`: group live-pane discovery by physical session.
- `internal/runtime/subscriptions.go`: session-specific subscribe commands.
- `internal/runtime/*_test.go`: runtime validation and multi-session routing coverage.
- `internal/transport/types.go`: `zellij_session` JSON fields and runtime conversions.
- `internal/transport/server_test.go`, `internal/transport/types_test.go`, `internal/transport/client_test.go`: transport contract coverage.
- `internal/planner/envelope.go`, `internal/planner/envelope_test.go`: strict envelope validation for the new field.
- `internal/cli/work/*`, `internal/cli/chrome/*`, `internal/cli/planner/*`, `internal/cli/ctl/*`: resolve and inject the physical session at user-facing submission boundaries.
- `internal/work/work.go`, `internal/chrome/chrome.go`, `internal/planner/page.go`, `internal/debate/debate.go`: carry the resolved value into generated plans.
- `cmd/agent-role/tabwatcher/tabwatcher.go`, `cmd/agent-role/tabnetwork/tabnetwork.go`: preserve the physical session through delayed Chrome-driven creation.
- `README.md`, `docs/api-types-definition.md`: document the distinct logical and physical session fields.

---

### Task 1: Shared CLI Session Resolution

**Files:**
- Create: `internal/cli/zellij_session.go`
- Create: `internal/cli/zellij_session_test.go`

**Interfaces:**
- Consumes: process environment variable `ZELLIJ_SESSION_NAME`.
- Produces: `func ResolveZellijSession(explicit string) (string, error)` and `var ErrZellijSessionRequired error`.

- [ ] **Step 1: Write resolver tests**

```go
package cli

import (
	"errors"
	"testing"
)

func TestResolveZellijSessionPrefersExplicitValue(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "from-env")
	got, err := ResolveZellijSession("  from-flag  ")
	if err != nil || got != "from-flag" {
		t.Fatalf("ResolveZellijSession() = %q, %v, want from-flag, nil", got, err)
	}
}

func TestResolveZellijSessionUsesCallingProcessEnvironment(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "  caller-session  ")
	got, err := ResolveZellijSession("")
	if err != nil || got != "caller-session" {
		t.Fatalf("ResolveZellijSession() = %q, %v, want caller-session, nil", got, err)
	}
}

func TestResolveZellijSessionRejectsMissingValue(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	_, err := ResolveZellijSession("   ")
	if !errors.Is(err, ErrZellijSessionRequired) {
		t.Fatalf("ResolveZellijSession() error = %v, want %v", err, ErrZellijSessionRequired)
	}
}
```

- [ ] **Step 2: Run the tests and verify the missing symbols fail**

Run: `go test ./internal/cli -run '^TestResolveZellijSession' -count=1`

Expected: FAIL because `ResolveZellijSession` and `ErrZellijSessionRequired` are undefined.

- [ ] **Step 3: Implement the resolver**

```go
package cli

import (
	"errors"
	"os"
	"strings"
)

var ErrZellijSessionRequired = errors.New("zellij session is required: pass --zellij-session or run inside Zellij")

func ResolveZellijSession(explicit string) (string, error) {
	if session := strings.TrimSpace(explicit); session != "" {
		return session, nil
	}
	if session := strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME")); session != "" {
		return session, nil
	}
	return "", ErrZellijSessionRequired
}
```

- [ ] **Step 4: Format and run the focused tests**

Run: `gofmt -w internal/cli/zellij_session.go internal/cli/zellij_session_test.go && go test ./internal/cli -run '^TestResolveZellijSession' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the resolver**

```bash
git add internal/cli/zellij_session.go internal/cli/zellij_session_test.go
git commit -m "feat: resolve caller zellij session"
```

### Task 2: Request-Scoped Zellij Backend

**Files:**
- Modify: `internal/zellij/types.go`
- Modify: `internal/zellij/backend.go`
- Modify: `internal/zellij/backend_test.go`

**Interfaces:**
- Consumes: `Session string` on every Zellij request.
- Produces: `ListPanes(ctx context.Context, req ListPanesRequest) ([]Pane, error)` and request-scoped routing for all backend methods.

- [ ] **Step 1: Add failing backend routing tests**

Add tests that construct a backend with `Options{Session: "default-session", Runner: runner}` and invoke requests with `Session: "request-session"`. Assert exact commands for create tab, create pane, list panes, close tab, close pane, send input, dump screen, and subscribe begin with:

```go
wantPrefix := []string{"--session", "request-session"}
if !reflect.DeepEqual(runner.specs[0].Args[:2], wantPrefix) {
	t.Fatalf("command prefix = %#v, want %#v", runner.specs[0].Args[:2], wantPrefix)
}
```

Also add one fallback test using an empty request session and asserting `default-session` is used. For `SubscribeCommand`, assert its returned `CommandSpec` directly.

- [ ] **Step 2: Run the backend tests and verify request fields are unavailable**

Run: `go test ./internal/zellij -run 'TestBackend.*Session|TestSubscribe.*Session' -count=1`

Expected: FAIL because the request types have no `Session` field and `ListPanes` accepts no request.

- [ ] **Step 3: Extend backend request types and interface**

Add `Session string` to `CreatePaneRequest`, `CreateTabRequest`, `CloseTabRequest`, `ClosePaneRequest`, `SendInputRequest`, `DumpScreenRequest`, and `SubscribeRequest`. Add:

```go
type ListPanesRequest struct {
	Session string
}
```

Change the backend interface method to:

```go
ListPanes(ctx context.Context, req ListPanesRequest) ([]Pane, error)
```

- [ ] **Step 4: Route each operation through the request session**

Add this helper to `internal/zellij/backend.go`:

```go
func (b *CLIBackend) requestSession(session string) string {
	if session = strings.TrimSpace(session); session != "" {
		return session
	}
	return b.session
}
```

Pass `b.requestSession(req.Session)` into every command builder. Change `ListPanes` to accept `ListPanesRequest` and call `listPanesCommand(b.binary, b.requestSession(req.Session))`. Preserve `Options.Session` only as a low-level fallback.

- [ ] **Step 5: Update existing backend tests and compile callers**

Change existing calls from:

```go
backend.ListPanes(ctx)
```

to:

```go
backend.ListPanes(ctx, ListPanesRequest{})
```

Do not change runtime callers yet beyond adding `zellij.ListPanesRequest{}` so the repository compiles at this checkpoint.

- [ ] **Step 6: Format and run backend and repository tests**

Run: `gofmt -w internal/zellij internal/runtime && go test ./internal/zellij ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 7: Commit backend routing**

```bash
git add internal/zellij internal/runtime
git commit -m "feat: scope zellij backend requests by session"
```

### Task 3: Runtime Creation and Execution-Plan Session Propagation

**Files:**
- Modify: `internal/runtime/types.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/execution_plan.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/runtime/execution_plan_test.go`

**Interfaces:**
- Consumes: `CreatePaneRequest.ZellijSession string` and `ApplyExecutionPlanRequest.ZellijSession string`.
- Produces: registry records whose `SessionID` is the physical session and all plan-created panes in that session.

- [ ] **Step 1: Add failing runtime validation and propagation tests**

Add tests with these assertions:

```go
_, err := service.CreatePane(context.Background(), CreatePaneRequest{ID: "missing-session"})
if !errors.Is(err, ErrZellijSessionRequired) {
	t.Fatalf("CreatePane() error = %v, want %v", err, ErrZellijSessionRequired)
}
```

```go
created, err := service.CreatePane(context.Background(), CreatePaneRequest{
	ID: "pane-a", ZellijSession: "session-a",
})
if err != nil {
	t.Fatal(err)
}
if created.Pane.SessionID != "session-a" || backend.createPaneRequests[0].Session != "session-a" {
	t.Fatalf("pane/backend session = %q/%q, want session-a/session-a", created.Pane.SessionID, backend.createPaneRequests[0].Session)
}
```

For execution plans, create two panes and assert both fake-backend create requests use `session-a`. Add a same-tab anchor test where the anchor is in `session-a` and the child requests `session-b`; expect `ErrInvalidPaneTarget`.

- [ ] **Step 2: Run focused runtime tests and verify they fail**

Run: `go test ./internal/runtime -run 'TestCreatePane.*Session|TestApplyExecutionPlan.*Session|TestCreatePaneRejectsCrossSessionAnchor' -count=1`

Expected: FAIL because runtime request fields and validation do not exist.

- [ ] **Step 3: Add runtime fields and errors**

Add to `internal/runtime/types.go`:

```go
var ErrZellijSessionRequired = errors.New("runtime: zellij session is required")
```

Add `ZellijSession string` to `CreatePaneRequest`. Add `ZellijSession string` to `ApplyExecutionPlanRequest` in `execution_plan.go`.

- [ ] **Step 4: Validate and store the physical session**

At the start of `CreatePane`, normalize and validate:

```go
req.ZellijSession = strings.TrimSpace(req.ZellijSession)
if req.ZellijSession == "" {
	return CreatePaneResponse{}, ErrZellijSessionRequired
}
```

Register with:

```go
SessionID: registry.SessionID(req.ZellijSession),
```

In `resolveCreatePaneTarget`, reject a same-tab anchor whose `SessionID` differs from `req.ZellijSession` before copying its tab ID.

- [ ] **Step 5: Put the session on every creation and discovery request**

Pass `Session: req.ZellijSession` to `CreateTabRequest`, `CreatePaneRequest`, `CloseTabRequest`, `ClosePaneRequest`, and pane discovery. Change discovery helpers to:

```go
func (s *Service) findPaneByID(ctx context.Context, session string, paneID zellij.PaneID) (zellij.Pane, error)
func (s *Service) findPaneInTab(ctx context.Context, session string, tabID zellij.TabID) (zellij.Pane, error)
```

and list with `zellij.ListPanesRequest{Session: session}`.

- [ ] **Step 6: Propagate execution-plan sessions**

Validate `strings.TrimSpace(req.ZellijSession) != ""` in `validateExecutionPlan`. Pass it into the first pane and every remaining pane. Extend `createRemainingExecutionPlanTabPanes` with a `zellijSession string` parameter. Add `Session: string(created.record.SessionID)` to initial-input, readiness-dump, and rollback backend requests.

- [ ] **Step 7: Update runtime fixtures with explicit sessions**

Give existing test helpers a deterministic default such as `ZellijSession: "test-session"` before calling `CreatePane`, while preserving explicit multi-session cases. Update integration test requests to use the session passed to their configured backend.

- [ ] **Step 8: Format and run creation tests**

Run: `gofmt -w internal/runtime && go test ./internal/runtime -run 'TestCreatePane|TestApplyExecutionPlan' -count=1`

Expected: PASS.

- [ ] **Step 9: Commit runtime creation routing**

```bash
git add internal/runtime
git commit -m "feat: propagate zellij session through runtime creation"
```

### Task 4: Runtime Follow-Up Operations and Multi-Session Reconciliation

**Files:**
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/cleanup.go`
- Modify: `internal/runtime/reconcile.go`
- Modify: `internal/runtime/subscriptions.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/runtime/cleanup_test.go`
- Modify: `internal/runtime/reconcile_test.go`
- Modify: `internal/runtime/subscriptions_test.go`

**Interfaces:**
- Consumes: `registry.PaneRecord.SessionID` populated by Task 3.
- Produces: all post-creation backend operations scoped to that stored session.

- [ ] **Step 1: Add failing follow-up routing tests**

Create panes in `session-a` and `session-b`, then verify fake-backend request logs after `SendInput`, `SendMessage`, `SnapshotOutput`, `ClosePane`, `Cleanup`, and subscription startup. Each assertion must compare the request session with the target record's session:

```go
if got := backend.closePaneRequests[0].Session; got != "session-a" {
	t.Fatalf("close session = %q, want session-a", got)
}
```

For reconcile, configure session-specific live panes and assert the backend receives one `ListPanesRequest` for each of `session-a` and `session-b` and that an ID collision such as `terminal_1` does not cross-match sessions.

- [ ] **Step 2: Run follow-up tests and verify session assertions fail**

Run: `go test ./internal/runtime -run 'Test.*Routes.*Session|TestReconcile.*MultipleSessions|TestSubscription.*Session' -count=1`

Expected: FAIL because follow-up requests do not yet carry the record session and reconciliation lists only once.

- [ ] **Step 3: Route single-pane operations by record session**

For every backend call after `lookupPane`, include:

```go
Session: string(record.SessionID),
```

Use `toRecord.SessionID` for message delivery. Include the created record's session for initial input and readiness polling.

- [ ] **Step 4: Route cleanup and subscriptions**

In cleanup close requests use `Session: string(record.SessionID)`. In `SubscriptionManager.run`, build:

```go
spec, err := m.opts.Backend.SubscribeCommand(zellij.SubscribeRequest{
	Session: string(record.SessionID),
	PaneID:  zellij.PaneID(record.ZellijPaneID),
	JSON:    true,
})
```

- [ ] **Step 5: Make same-tab identity session-aware**

Change `sameManagedTab` so equal numeric `ZellijTabID` values count as the same tab only when `SessionID` also matches:

```go
if a.SessionID == "" || a.SessionID != b.SessionID {
	return false
}
if a.ZellijTabID != nil && b.ZellijTabID != nil {
	return *a.ZellijTabID == *b.ZellijTabID
}
return a.TabID != "" && a.TabID == b.TabID
```

- [ ] **Step 6: Reconcile by composite session and pane ID**

Introduce a composite key:

```go
type livePaneKey struct {
	session registry.SessionID
	paneID  registry.ZellijPaneID
}
```

Build the set of non-terminal record sessions, call `ListPanes(ctx, zellij.ListPanesRequest{Session: string(sessionID)})` once per sorted session, and key live and managed maps by `livePaneKey`. Pass the record session into `reconcileRecord` lookups. If listing one session fails, publish runtime health and return the error without marking its records lost.

- [ ] **Step 7: Format and run all runtime tests**

Run: `gofmt -w internal/runtime && go test ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 8: Commit lifecycle routing**

```bash
git add internal/runtime
git commit -m "feat: route pane lifecycle by zellij session"
```

### Task 5: Transport and Planner Contract

**Files:**
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/types_test.go`
- Modify: `internal/transport/client_test.go`
- Modify: `internal/transport/server_test.go`
- Modify: `internal/planner/envelope.go`
- Modify: `internal/planner/envelope_test.go`
- Modify: `docs/api-types-definition.md`

**Interfaces:**
- Consumes: runtime fields from Task 3.
- Produces: `CreatePaneRequest.ZellijSession` and `ExecutionPlanPayload.ZellijSession`, both encoded as `zellij_session`, plus separate strict-decode and semantic-validation functions for CLI enrichment.

- [ ] **Step 1: Add failing JSON and conversion tests**

Add `ZellijSession: "physical-a"` to direct pane and execution-plan fixtures. Assert conversions preserve it:

```go
if converted.ZellijSession != "physical-a" {
	t.Fatalf("ZellijSession = %q, want physical-a", converted.ZellijSession)
}
```

Assert marshaled dry payload contains `"zellij_session":"physical-a"`. Add server tests that missing `zellij_session` returns HTTP 400 for both `/v1/panes` and `/v1/requests`.

- [ ] **Step 2: Run transport and planner tests and verify failures**

Run: `go test ./internal/transport ./internal/planner -run 'Test.*ZellijSession|Test.*Missing.*Session' -count=1`

Expected: FAIL because transport fields and planner validation are absent.

- [ ] **Step 3: Add transport fields and conversions**

Add:

```go
ZellijSession string `json:"zellij_session"`
```

to `transport.CreatePaneRequest` and `transport.ExecutionPlanPayload`. Map the values into `rt.CreatePaneRequest.ZellijSession` and `rt.ApplyExecutionPlanRequest.ZellijSession` in `ToRuntime` methods, including the nil-tabs branch.

- [ ] **Step 4: Validate strict execution-plan envelopes with an explicit fallback path**

Keep `ParseExecutionPlanEnvelope(data)` strict for validation-only paths. Split its internals into two exported stages:

```go
func DecodeExecutionPlanEnvelope(data []byte) (ValidatedExecutionPlan, error)
func ValidateExecutionPlan(plan ValidatedExecutionPlan) error
```

`DecodeExecutionPlanEnvelope` strictly decodes the envelope and payload, rejects unknown fields and malformed envelope structure, but does not require semantic payload fields. `ValidateExecutionPlan` applies `validateExecutionPlanPayload`. `ParseExecutionPlanEnvelope` calls both stages in order. In `validateExecutionPlanPayload`, add:

```go
if strings.TrimSpace(payload.ZellijSession) == "" {
	return fmt.Errorf("%w: payload.zellij_session is required", ErrInvalidExecutionPlanEnvelope)
}
```

Import `strings`. Runtime validation remains the final guard for raw server requests and direct pane requests.

- [ ] **Step 5: Update API documentation examples**

Add `"zellij_session": "gregarious-iguanadon"` beside logical `"session"` in execution-plan examples and beside `task_id` in direct pane examples. Explain in one paragraph that `session` groups a task while `zellij_session` selects the physical Zellij target.

- [ ] **Step 6: Format and run contract tests**

Run: `gofmt -w internal/transport internal/planner && go test ./internal/transport ./internal/planner -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the contract**

```bash
git add internal/transport internal/planner docs/api-types-definition.md
git commit -m "feat: add zellij session to transport requests"
```

### Task 6: User-Facing Plan Submission Commands

**Files:**
- Modify: `internal/cli/work/work.go`
- Modify: `internal/cli/work/work_test.go`
- Modify: `internal/work/work.go`
- Modify: `internal/cli/chrome/chrome.go`
- Modify: `internal/cli/chrome/chrome_test.go`
- Modify: `internal/chrome/chrome.go`
- Modify: `internal/chrome/chrome_test.go`
- Modify: `internal/cli/planner/planner.go`
- Modify: `internal/cli/planner/planner_test.go`
- Modify: `internal/planner/page.go`
- Modify: `internal/planner/page_test.go`
- Modify: `internal/cli/ctl/ctl.go`
- Modify: `internal/cli/ctl/ctl_test.go`
- Modify: `internal/debate/debate.go`
- Modify: `internal/debate/debate_test.go`

**Interfaces:**
- Consumes: `cli.ResolveZellijSession(string)` and `transport.ExecutionPlanPayload.ZellijSession`.
- Produces: all immediate CLI submissions and dry runs with a resolved physical session.

- [ ] **Step 1: Add failing CLI precedence and dry-run tests**

For work, Chrome, planner page/tui/submit, ctl plan, and ctl debate, add one test with `t.Setenv("ZELLIJ_SESSION_NAME", "env-session")` and no flag, then assert the submitted or dry-run payload uses `env-session`. Add at least one precedence test using `--zellij-session flag-session`. Add one missing-session test with the environment cleared and assert exit code `1` plus the resolver error text.

- [ ] **Step 2: Run command tests and verify missing flags/fields fail**

Run: `go test ./internal/cli/work ./internal/cli/chrome ./internal/cli/planner ./internal/cli/ctl ./internal/debate -run 'Test.*ZellijSession' -count=1`

Expected: FAIL because the commands do not expose or resolve `--zellij-session`.

- [ ] **Step 3: Add `--zellij-session` to generated-plan CLIs**

In each relevant flag set declare:

```go
zellijSessionFlag := fs.String("zellij-session", "", "physical Zellij session; defaults to ZELLIJ_SESSION_NAME")
```

Resolve it after argument validation and before building or validating the envelope:

```go
zellijSession, err := cli.ResolveZellijSession(*zellijSessionFlag)
if err != nil {
	fmt.Fprintf(stderr, "resolve zellij session: %v\n", err)
	return 1
}
```

Set `payload.ZellijSession = zellijSession` before dry-run encoding or submission. For builders, add `ZellijSession string` to their request/options and construct payloads with that field rather than patching it afterward.

- [ ] **Step 4: Enrich file-based plan submissions before semantic validation**

For planner submit and ctl plan, accept `--zellij-session`. Strictly decode the file first, choose an explicit flag over the file's `zellij_session`, and pass that candidate to the shared resolver so a missing request value falls back to the calling process environment. Assign the result and run semantic validation:

```go
plan, err := planner.DecodeExecutionPlanEnvelope(data)
if err != nil {
	fmt.Fprintf(stderr, "submit decode failed: %v\n", err)
	return 1
}
sessionCandidate := plan.Payload.ZellijSession
if strings.TrimSpace(*zellijSessionFlag) != "" {
	sessionCandidate = *zellijSessionFlag
}
resolvedSession, err := cli.ResolveZellijSession(sessionCandidate)
if err != nil {
	fmt.Fprintf(stderr, "resolve zellij session: %v\n", err)
	return 1
}
plan.Payload.ZellijSession = resolvedSession
if err := planner.ValidateExecutionPlan(plan); err != nil {
	fmt.Fprintf(stderr, "submit validation failed: %v\n", err)
	return 1
}
```

Rebuild `plan.Envelope.Payload` from `plan.Payload` before dry-run output or submission. The validation-only `planner validate` command continues to call `ParseExecutionPlanEnvelope` and rejects a missing `zellij_session`. Raw HTTP requests also continue to reject it in runtime validation.

- [ ] **Step 5: Carry the session through debate options**

Add `ZellijSession string` to `debate.Options`, pass it from ctl, and change plan creation to:

```go
payload := executionPlan(requestID, opts.ZellijSession, agentSpecs, coordinatorSpec)
```

Set `ZellijSession: zellijSession` in the returned payload.

- [ ] **Step 6: Update usage output**

Document the option uniformly:

```text
--zellij-session string
    physical Zellij session; defaults to ZELLIJ_SESSION_NAME
```

Ensure help tests assert the new flag for every exposed command.

- [ ] **Step 7: Format and run submission-command tests**

Run: `gofmt -w internal/cli/work internal/work internal/cli/chrome internal/chrome internal/cli/planner internal/planner internal/cli/ctl internal/debate && go test ./internal/cli/work ./internal/work ./internal/cli/chrome ./internal/chrome ./internal/cli/planner ./internal/planner ./internal/cli/ctl ./internal/debate -count=1`

Expected: PASS.

- [ ] **Step 8: Commit immediate CLI propagation**

```bash
git add internal/cli/work internal/work internal/cli/chrome internal/chrome internal/cli/planner internal/planner internal/cli/ctl internal/debate
git commit -m "feat: target caller zellij session from cli plans"
```

### Task 7: Delayed Chrome Pane Creation

**Files:**
- Modify: `cmd/agent-role/tabwatcher/tabwatcher.go`
- Modify: `cmd/agent-role/tabwatcher/tabwatcher_test.go`
- Modify: `cmd/agent-role/tabnetwork/tabnetwork.go`
- Modify: `cmd/agent-role/tabnetwork/tabnetwork_test.go`

**Interfaces:**
- Consumes: the resolved physical session from Task 1.
- Produces: delayed `CreatePaneRequest` and `ExecutionPlanPayload` values that retain their originating physical session.

- [ ] **Step 1: Add failing Chrome delayed-creation tests**

Assert watcher-generated plans and tab-network child pane requests contain `physical-a`. Assert both role parsers accept `--zellij-session physical-a` and reject a missing physical session when their mode will create panes.

- [ ] **Step 2: Run delayed-creation tests and verify failures**

Run: `go test ./cmd/agent-role/tabwatcher ./cmd/agent-role/tabnetwork -run 'Test.*ZellijSession' -count=1`

Expected: FAIL because delayed creators do not retain the physical session.

- [ ] **Step 3: Preserve Chrome watcher session state**

Add `ZellijSession string` to Chrome `PlanRequest`, watcher config, and tab-network tracker config. The top-level Chrome CLI resolves the value and passes it to `BuildPlan`. Generated watcher commands include `--zellij-session`, watcher-generated plans set `ZellijSession`, and tab-network child requests set `ZellijSession`.

When a role is executed directly and its mode creates panes, use `cli.ResolveZellijSession` so direct callers get the same flag/environment behavior.

- [ ] **Step 4: Format and run delayed-creation tests**

Run: `gofmt -w cmd/agent-role/tabwatcher cmd/agent-role/tabnetwork && go test ./cmd/agent-role/tabwatcher ./cmd/agent-role/tabnetwork -count=1`

Expected: PASS.

- [ ] **Step 5: Commit delayed-creation propagation**

```bash
git add cmd/agent-role/tabwatcher cmd/agent-role/tabnetwork
git commit -m "feat: preserve zellij session for delayed workers"
```

### Task 8: Documentation, Full Verification, and Custom CLI Registration

**Files:**
- Modify: `README.md`
- Modify: `docs/manual-smoke-test.md`
- Test: all Go packages
- Build: `bin/zellij-agent`
- Register: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: documented behavior, a passing repository, and the registered unified binary.

- [ ] **Step 1: Update user documentation**

Document these exact rules in the work, Chrome, and planner sections:

```text
--zellij-session selects the physical Zellij session. When omitted, the CLI
uses its own ZELLIJ_SESSION_NAME. The logical --session flag remains the
execution task ID. Commands fail before submission when neither source names
a physical Zellij session.
```

Add dry-run examples that show both `session` and `zellij_session`.

- [ ] **Step 2: Add a manual two-session smoke flow**

In `docs/manual-smoke-test.md`, document creating two named Zellij sessions, submitting one dry-run/real plan from each, checking `zellij-agent ctl status`, and confirming each physical session received only its own tab and panes.

- [ ] **Step 3: Run formatting and static diff checks**

Run: `gofmt -w cmd internal && git diff --check`

Expected: no output from `git diff --check`.

- [ ] **Step 4: Run the complete test suite**

Run: `go test ./...`

Expected: PASS for every package.

- [ ] **Step 5: Build and immediately register the unified binary**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent && cp bin/zellij-agent ~/.config/custom-cli`

Expected: exit code 0 and `~/.config/custom-cli/zellij-agent` updated.

- [ ] **Step 6: Verify the registered artifact matches the build**

Run: `cmp bin/zellij-agent ~/.config/custom-cli/zellij-agent && ~/.config/custom-cli/zellij-agent --help >/dev/null`

Expected: exit code 0.

- [ ] **Step 7: Commit documentation and final integration adjustments**

```bash
git add README.md docs/manual-smoke-test.md
git commit -m "docs: explain physical zellij session targeting"
```

- [ ] **Step 8: Review final repository state**

Run: `git status --short && git log -8 --oneline`

Expected: no uncommitted source or documentation changes; recent commits correspond to Tasks 1 through 8.
