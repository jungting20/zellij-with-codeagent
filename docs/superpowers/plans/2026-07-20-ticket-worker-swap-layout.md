# Ticket Worker Swap Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Launch every `ticket-worker` tab with a Zellij swap layout that keeps the ticket manager above a dynamically reflowing row of workers at a 50/50 height split.

**Architecture:** Add an optional tab-scoped `LayoutString` to the existing execution-plan transport/runtime path and forward it only through new-tab creation to `zellij action new-tab --layout-string`. `internal/ticketworker` supplies fixed KDL containing a one-pane base and a `min_panes=2` swap layout; the existing manager continues creating and closing workers through `RuntimeService`.

**Tech Stack:** Go, standard `testing`, JSON transport structs, Zellij CLI KDL layouts, existing runtime service and fake backend.

## Global Constraints

- Manager occupies the top 50%; all open workers share the bottom 50% side by side.
- With zero workers, the manager occupies the whole tab through the one-pane Base layout.
- Worker creation and cleanup continue through `RuntimeService`; ticket-worker code must not invoke Zellij directly.
- `layout_string` is optional and tab-scoped; the existing execution-plan `Layout` label remains unchanged.
- Existing plans without `layout_string` preserve their serialized JSON and Zellij command behavior.
- The existing `ticket-manager` role remains the default role; no role CLI or catalog contract changes.
- Use TDD, run `gofmt`, run `go test ./...`, rebuild `bin/zellij-agent`, and install it atomically.

---

## File Structure

- `internal/transport/types.go`: JSON-facing tab layout and runtime conversion.
- `internal/runtime/execution_plan.go`: runtime tab layout and first-pane forwarding.
- `internal/runtime/types.go` and `service.go`: new-tab request propagation.
- `internal/zellij/types.go` and `commands.go`: inline layout CLI emission.
- `internal/ticketworker/plan.go`: fixed ticket-worker KDL.
- Adjacent `*_test.go` files: boundary-focused regression tests.
- `README.md`: visible layout behavior.

### Task 1: Add the tab-scoped execution-plan layout contract

**Files:**
- Modify: `internal/transport/types.go:162-172,435-467`
- Modify: `internal/transport/types_test.go:12-21,87-128`
- Modify: `internal/runtime/execution_plan.go:25-34`

**Interfaces:**
- Consumes: `ExecutionPlanPayload.ToRuntime(reqID string) runtime.ApplyExecutionPlanRequest`.
- Produces: `transport.ExecutionPlanTab.LayoutString string` serialized as `layout_string`; `runtime.ExecutionPlanTabSpec.LayoutString string`.

- [ ] **Step 1: Write failing conversion and JSON tests**

Extend the source tab in `TestExecutionPlanPayloadToRuntimePreservesNestedPayload`:

```go
Tabs: []ExecutionPlanTab{{
    Name:         "frontend",
    LayoutString: `layout { pane; }`,
    Panes: []ExecutionPlanPane{{
        ID:                    "planner",
        Role:                  "planner",
        AgentID:               "agent-1",
        Command:               []string{"npm", "test"},
        CWD:                   "/tmp/app",
        InitialInput:          "inspect the auth flow",
        InitialInputReadyText: "›",
    }},
}},
```

Add:

```go
if converted.Tabs[0].LayoutString != `layout { pane; }` {
    t.Fatalf("ExecutionPlanPayload.ToRuntime() LayoutString = %q", converted.Tabs[0].LayoutString)
}
```

Add a focused JSON test:

```go
func TestExecutionPlanTabJSONUsesOptionalLayoutString(t *testing.T) {
    withLayout, err := json.Marshal(ExecutionPlanTab{Name: "ticket-worker", LayoutString: `layout { pane; }`})
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(string(withLayout), `"layout_string":"layout { pane; }"`) {
        t.Fatalf("marshaled tab = %s, want layout_string", withLayout)
    }

    withoutLayout, err := json.Marshal(ExecutionPlanTab{Name: "plain"})
    if err != nil {
        t.Fatal(err)
    }
    if strings.Contains(string(withoutLayout), "layout_string") {
        t.Fatalf("marshaled tab = %s, want layout_string omitted", withoutLayout)
    }
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

```bash
go test ./internal/transport -run 'TestExecutionPlan(TabJSONUsesOptionalLayoutString|PayloadToRuntimePreservesNestedPayload)$' -count=1
```

Expected: compilation fails because both execution-plan tab types lack `LayoutString`.

- [ ] **Step 3: Add the minimal transport and runtime fields**

```go
type ExecutionPlanTab struct {
    Name         string              `json:"name"`
    LayoutString string              `json:"layout_string,omitempty"`
    Panes        []ExecutionPlanPane `json:"panes"`
}
```

```go
type ExecutionPlanTabSpec struct {
    Name         string
    LayoutString string
    Panes        []ExecutionPlanPaneSpec
}
```

Update `ExecutionPlanTab.ToRuntime`:

```go
return rt.ExecutionPlanTabSpec{
    Name:         tab.Name,
    LayoutString: tab.LayoutString,
    Panes:        panes,
}
```

- [ ] **Step 4: Format and verify**

```bash
gofmt -w internal/transport/types.go internal/transport/types_test.go internal/runtime/execution_plan.go
go test ./internal/transport -run 'TestExecutionPlan(TabJSONUsesOptionalLayoutString|PayloadToRuntimePreservesNestedPayload)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/types.go internal/transport/types_test.go internal/runtime/execution_plan.go
git commit -m "feat: carry tab layout strings in execution plans"
```

### Task 2: Teach the Zellij backend to create tabs with inline layouts

**Files:**
- Modify: `internal/zellij/types.go:51-56`
- Modify: `internal/zellij/commands.go:47-61`
- Modify: `internal/zellij/backend_test.go:258-294`

**Interfaces:**
- Consumes: optional inline KDL passed from runtime.
- Produces: `zellij.CreateTabRequest.LayoutString string` and CLI form `new-tab --layout-string <KDL> -- <command>`.

- [ ] **Step 1: Write failing backend tests**

In `TestCreateTabParsesReturnedTabID`, set:

```go
id, err := backend.CreateTab(context.Background(), CreateTabRequest{
    Name:         "tests",
    CWD:          "/workspace",
    LayoutString: "layout { pane; }",
    Command:      []string{"go", "test", "./..."},
})
```

Expect:

```go
Args: []string{
    "--session", "agent-session",
    "action", "new-tab",
    "--layout-string", "layout { pane; }",
    "--name", "tests",
    "--cwd", "/workspace",
    "--", "go", "test", "./...",
},
```

Add:

```go
func TestCreateTabOmitsEmptyLayoutString(t *testing.T) {
    runner := &fakeRunner{results: []fakeResult{{result: CommandResult{Stdout: "4\n"}}}}
    backend := NewBackend(Options{Runner: runner})

    if _, err := backend.CreateTab(context.Background(), CreateTabRequest{Name: "plain"}); err != nil {
        t.Fatal(err)
    }
    want := []string{"action", "new-tab", "--name", "plain"}
    if !reflect.DeepEqual(runner.commands[0].Args, want) {
        t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, want)
    }
}
```

- [ ] **Step 2: Run and verify failure**

```bash
go test ./internal/zellij -run 'TestCreateTab(ParsesReturnedTabID|OmitsEmptyLayoutString)$' -count=1
```

Expected: compilation fails because `CreateTabRequest.LayoutString` does not exist.

- [ ] **Step 3: Add backend support**

```go
type CreateTabRequest struct {
    Session      string
    Name         string
    CWD          string
    LayoutString string
    Command      []string
}
```

At the start of `createTabCommand` optional arguments:

```go
if req.LayoutString != "" {
    args = append(args, "--layout-string", req.LayoutString)
}
```

Retain name, cwd, and command handling after it.

- [ ] **Step 4: Format and verify**

```bash
gofmt -w internal/zellij/types.go internal/zellij/commands.go internal/zellij/backend_test.go
go test ./internal/zellij -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/zellij/types.go internal/zellij/commands.go internal/zellij/backend_test.go
git commit -m "feat: create zellij tabs with inline layouts"
```

### Task 3: Forward a plan tab layout through RuntimeService

**Files:**
- Modify: `internal/runtime/types.go:103-118`
- Modify: `internal/runtime/execution_plan.go:85-97`
- Modify: `internal/runtime/service.go:247-285`
- Modify: `internal/runtime/service_test.go:999-1007`
- Modify: `internal/runtime/execution_plan_test.go:48-125`

**Interfaces:**
- Consumes: `runtime.ExecutionPlanTabSpec.LayoutString` and `zellij.CreateTabRequest.LayoutString`.
- Produces: `runtime.CreatePaneRequest.LayoutString string`, used only with `NewTab=true`.

- [ ] **Step 1: Write the failing runtime assertion**

In `TestApplyExecutionPlanCreatesPanesInOneTab`, set:

```go
{
    Name:         "feature-auth",
    LayoutString: `layout { pane; }`,
    Panes: []ExecutionPlanPaneSpec{
        {ID: "planner", Role: "planner"},
        {ID: "frontend", Role: "react-dev"},
    },
},
```

Assert:

```go
if backend.createTabRequests[0].Name != "feature-auth" ||
    backend.createTabRequests[0].LayoutString != `layout { pane; }` {
    t.Fatalf("CreateTab request = %#v, want name and inline layout", backend.createTabRequests[0])
}
```

Keep the existing second-pane request assertion unchanged to prove layout data is not sent to later `CreatePane` calls.

- [ ] **Step 2: Run and verify failure**

```bash
go test ./internal/runtime -run '^TestApplyExecutionPlanCreatesPanesInOneTab$' -count=1
```

Expected: FAIL because the backend request does not preserve the layout.

- [ ] **Step 3: Add runtime propagation**

Add `LayoutString string` beside `NewTab` and `TabName` in `CreatePaneRequest`. Add this to the first pane request in `ApplyExecutionPlan`:

```go
LayoutString: tabSpec.LayoutString,
```

Forward it only in the new-tab branch:

```go
tabID, err := s.backend.CreateTab(ctx, zellij.CreateTabRequest{
    Session:      req.ZellijSession,
    Name:         req.TabName,
    CWD:          req.CWD,
    LayoutString: req.LayoutString,
    Command:      cloneStrings(req.Command),
})
```

Update the fake backend copy:

```go
b.createTabRequests = append(b.createTabRequests, zellij.CreateTabRequest{
    Session:      req.Session,
    Name:         req.Name,
    CWD:          req.CWD,
    LayoutString: req.LayoutString,
    Command:      cloneStrings(req.Command),
})
```

- [ ] **Step 4: Format and verify**

```bash
gofmt -w internal/runtime/types.go internal/runtime/execution_plan.go internal/runtime/service.go internal/runtime/service_test.go internal/runtime/execution_plan_test.go
go test ./internal/runtime -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/types.go internal/runtime/execution_plan.go internal/runtime/service.go internal/runtime/service_test.go internal/runtime/execution_plan_test.go
git commit -m "feat: apply inline layouts to planned tabs"
```

### Task 4: Attach the manager-above-workers layout to ticket-worker

**Files:**
- Modify: `internal/ticketworker/plan.go:12-67`
- Modify: `internal/ticketworker/plan_test.go:9-51`
- Modify: `README.md:207-225`

**Interfaces:**
- Consumes: `transport.ExecutionPlanTab.LayoutString`.
- Produces: `ticketWorkerLayout` fixed KDL and a `BuildStartPlan` result carrying it.

- [ ] **Step 1: Write the failing layout contract test**

After the existing shape assertion:

```go
layout := got.Tabs[0].LayoutString
for _, want := range []string{
    `pane name="ticket-manager"`,
    `swap_tiled_layout name="ticket-worker"`,
    `tab min_panes=2 split_direction="horizontal"`,
    `pane size="50%"`,
    `split_direction="vertical"`,
    `children`,
} {
    if !strings.Contains(layout, want) {
        t.Fatalf("ticket-worker layout missing %q:\n%s", want, layout)
    }
}
if strings.Count(layout, `size="50%"`) != 2 {
    t.Fatalf("ticket-worker layout 50%% pane count = %d, want 2", strings.Count(layout, `size="50%"`))
}
```

- [ ] **Step 2: Run and verify failure**

```bash
go test ./internal/ticketworker -run '^TestBuildStartPlanCreatesTicketManagerAnchor$' -count=1
```

Expected: assertions fail because the layout is empty.

- [ ] **Step 3: Define and attach the KDL**

Add near `StartPlanRequest`:

```go
const ticketWorkerLayout = `layout {
    pane name="ticket-manager"

    swap_tiled_layout name="ticket-worker" {
        tab min_panes=2 split_direction="horizontal" {
            pane size="50%"
            pane size="50%" split_direction="vertical" {
                children
            }
        }
    }
}`
```

Attach it to the generated tab:

```go
{
    Name:         "ticket-worker",
    LayoutString: ticketWorkerLayout,
    Panes: []transport.ExecutionPlanPane{
        {
            ID:      anchor,
            Role:    "ticket-manager",
            Command: command,
            CWD:     root,
        },
    },
},
```

- [ ] **Step 4: Update README**

Replace the existing "beside itself" description with:

```markdown
`start` creates one runtime-managed `ticket-manager` pane in a new
`ticket-worker` tab. With no active workers, the manager fills the tab. While
workers are active, the manager occupies the top 50% and all coding-agent
workers share the bottom 50% side by side; Zellij reflows that row whenever a
worker opens or closes. The manager claims the oldest `ready` tickets, starts
up to `max_workers` coding-agent panes, and continues polling for new tickets.
```

Keep the YOLO mode, Zellij-session, and daemon behavior text.

- [ ] **Step 5: Format and verify**

```bash
gofmt -w internal/ticketworker/plan.go internal/ticketworker/plan_test.go
go test ./internal/ticketworker ./internal/cli/ticketworker ./cmd/agent-role/ticketmanager -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ticketworker/plan.go internal/ticketworker/plan_test.go README.md
git commit -m "feat: arrange ticket workers below manager"
```

### Task 5: Full verification and binary installation

**Files:**
- Verify: all modified Go files and `README.md`
- Build: `bin/zellij-agent`
- Install: `/Users/in05908_mac/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: completed Tasks 1-4.
- Produces: verified repository state and atomically installed unified binary.

- [ ] **Step 1: Format and check diffs**

```bash
gofmt -w internal/transport/types.go internal/transport/types_test.go internal/runtime/types.go internal/runtime/execution_plan.go internal/runtime/service.go internal/runtime/service_test.go internal/runtime/execution_plan_test.go internal/zellij/types.go internal/zellij/commands.go internal/zellij/backend_test.go internal/ticketworker/plan.go internal/ticketworker/plan_test.go
git diff --check
```

Expected: exit 0 with no diff-check output.

- [ ] **Step 2: Run the full suite**

```bash
go test ./...
```

Expected: PASS for every package.

- [ ] **Step 3: Build the unified binary**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
```

Expected: exit 0 and `bin/zellij-agent` exists.

- [ ] **Step 4: Install atomically**

```bash
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
```

Expected: exit 0; the existing executable is not overwritten in place.

- [ ] **Step 5: Verify installed CLI and repository state**

```bash
/Users/in05908_mac/.config/custom-cli/zellij-agent ticket-worker --help
git status --short
git log -5 --oneline
```

Expected: help succeeds and lists `start`; worktree is clean; recent history contains the task commits.

- [ ] **Step 6: Record formatting changes only if needed**

No commit is needed when verification creates no tracked changes. If Step 1 changed tracked formatting, commit only those changes:

```bash
git add internal/transport internal/runtime internal/zellij internal/ticketworker
git commit -m "style: format ticket worker layout changes"
```
