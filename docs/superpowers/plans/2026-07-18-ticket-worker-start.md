# Ticket Worker Start Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `zellij-agent ticket-worker start` launch the existing ticket-manager role as one runtime-managed anchor pane instead of manually transitioning a ticket.

**Architecture:** Add a pure plan builder to `internal/ticketworker` that derives stable per-project runtime identities and returns a single-pane `transport.ExecutionPlanPayload`. Extend the ticket-worker CLI to validate initialization/configuration and submit that plan through an injected daemon client, then wire the unified binary's executable path and auto-start client into the command.

**Tech Stack:** Go 1.x, standard `flag`/`context`/`crypto/sha256`, existing `internal/transport`, `internal/planner`, SQLite ticket store, Go `testing`.

## Global Constraints

- Pane creation must go through the daemon transport and `RuntimeService`; no planner or CLI code may call Zellij directly.
- `ticket-worker start` accepts no ticket ID and does not mutate ticket state.
- The physical Zellij session comes from `--zellij-session` or `ZELLIJ_SESSION_NAME`.
- Runtime identities are stable per canonical project root and distinct across projects.
- Existing queue command behavior remains unchanged.
- Go files must be formatted with `gofmt`, and the final regression command is `go test ./...`.
- Rebuild and register `bin/zellij-agent` atomically after the unified CLI changes.

---

### Task 1: Pure Ticket-Manager Launch Plan

**Files:**
- Create: `internal/ticketworker/plan.go`
- Create: `internal/ticketworker/plan_test.go`

**Interfaces:**
- Consumes: `transport.ExecutionPlanPayload`, the existing `ticket-manager` role CLI contract.
- Produces: `type StartPlanRequest struct { Root string; ZellijSession string; SocketPath string; Executable []string }`, `BuildStartPlan(StartPlanRequest) (transport.ExecutionPlanPayload, error)`, and `StartRequestID(string) string`.

- [ ] **Step 1: Write failing plan-shape and identity tests**

```go
func TestBuildStartPlanCreatesTicketManagerAnchor(t *testing.T) {
    root := t.TempDir()
    got, err := BuildStartPlan(StartPlanRequest{
        Root: root, ZellijSession: "physical-a", SocketPath: "/tmp/tickets.sock",
        Executable: []string{"/opt/bin/zellij-agent"},
    })
    if err != nil { t.Fatal(err) }
    if got.Layout != "single-tab" || len(got.Tabs) != 1 || len(got.Tabs[0].Panes) != 1 {
        t.Fatalf("plan shape = %#v", got)
    }
    pane := got.Tabs[0].Panes[0]
    if got.Tabs[0].Name != "ticket-worker" || pane.Role != "ticket-manager" || pane.CWD != root {
        t.Fatalf("manager pane = %#v", pane)
    }
    if !strings.Contains(got.Session, "ticket-worker-") || !strings.Contains(pane.ID, "ticket-manager-") {
        t.Fatalf("identities = session %q pane %q", got.Session, pane.ID)
    }
    wantCommand := []string{"/opt/bin/zellij-agent", "role", "ticket-manager", "--socket", "/tmp/tickets.sock", "--task", got.Session, "--anchor-pane", pane.ID, "--zellij-session", "physical-a", root}
    if !reflect.DeepEqual(pane.Command, wantCommand) { t.Fatalf("command = %#v, want %#v", pane.Command, wantCommand) }
}

func TestBuildStartPlanIdentityIsStableAndProjectScoped(t *testing.T) {
    first, _ := BuildStartPlan(StartPlanRequest{Root: "/repo/a", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}})
    again, _ := BuildStartPlan(StartPlanRequest{Root: "/repo/a", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}})
    other, _ := BuildStartPlan(StartPlanRequest{Root: "/repo/b", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}})
    if first.Session != again.Session || first.Tabs[0].Panes[0].ID != again.Tabs[0].Panes[0].ID { t.Fatal("same root produced unstable identities") }
    if first.Session == other.Session || first.Tabs[0].Panes[0].ID == other.Tabs[0].Panes[0].ID { t.Fatal("different roots collided") }
}
```

Use this validation table in `TestBuildStartPlanRejectsInvalidInput`:

```go
tests := []struct {
    name string
    req  StartPlanRequest
}{
    {name: "empty root", req: StartPlanRequest{ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}}},
    {name: "relative root", req: StartPlanRequest{Root: "repo", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za"}}},
    {name: "empty session", req: StartPlanRequest{Root: "/repo", SocketPath: "/tmp/a", Executable: []string{"za"}}},
    {name: "empty socket", req: StartPlanRequest{Root: "/repo", ZellijSession: "z", Executable: []string{"za"}}},
    {name: "empty executable", req: StartPlanRequest{Root: "/repo", ZellijSession: "z", SocketPath: "/tmp/a"}},
    {name: "blank executable argument", req: StartPlanRequest{Root: "/repo", ZellijSession: "z", SocketPath: "/tmp/a", Executable: []string{"za", " "}}},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        if _, err := BuildStartPlan(tt.req); err == nil { t.Fatal("BuildStartPlan() error=nil") }
    })
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/ticketworker -run '^TestBuildStartPlan' -count=1`

Expected: FAIL because `StartPlanRequest` and `BuildStartPlan` are undefined.

- [ ] **Step 3: Implement the minimal pure builder**

```go
type StartPlanRequest struct {
    Root          string
    ZellijSession string
    SocketPath    string
    Executable    []string
}

func BuildStartPlan(req StartPlanRequest) (transport.ExecutionPlanPayload, error) {
    root := filepath.Clean(strings.TrimSpace(req.Root))
    if root == "." || !filepath.IsAbs(root) { return transport.ExecutionPlanPayload{}, fmt.Errorf("ticket-worker start plan: absolute root is required") }
    zellijSession := strings.TrimSpace(req.ZellijSession)
    if zellijSession == "" { return transport.ExecutionPlanPayload{}, fmt.Errorf("ticket-worker start plan: Zellij session is required") }
    socketPath := strings.TrimSpace(req.SocketPath)
    if socketPath == "" { return transport.ExecutionPlanPayload{}, fmt.Errorf("ticket-worker start plan: socket path is required") }
    executable, err := normalizeStartExecutable(req.Executable)
    if err != nil { return transport.ExecutionPlanPayload{}, err }
    suffix := startIdentity(root)
    session, anchor := "ticket-worker-"+suffix, "ticket-manager-"+suffix
    command := append(executable, "role", "ticket-manager", "--socket", socketPath, "--task", session, "--anchor-pane", anchor, "--zellij-session", zellijSession, root)
    return transport.ExecutionPlanPayload{Session: session, ZellijSession: zellijSession, Layout: "single-tab", Tabs: []transport.ExecutionPlanTab{{Name: "ticket-worker", Panes: []transport.ExecutionPlanPane{{ID: anchor, Role: "ticket-manager", Command: command, CWD: root}}}}}, nil
}

func StartRequestID(session string) string { return "req_" + session }
```

Implement `startIdentity` with SHA-256 over the cleaned root and the first four digest bytes encoded as eight lowercase hex characters. Clone and trim the executable without mutating caller-owned slices.

- [ ] **Step 4: Format and run the package tests**

Run: `gofmt -w internal/ticketworker/plan.go internal/ticketworker/plan_test.go && go test ./internal/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the pure plan builder**

```bash
git add internal/ticketworker/plan.go internal/ticketworker/plan_test.go
git commit -m "feat: build ticket manager launch plan"
```

### Task 2: Ticket-Worker Start CLI

**Files:**
- Modify: `internal/cli/ticketworker/ticketworker.go`
- Modify: `internal/cli/ticketworker/ticketworker_test.go`

**Interfaces:**
- Consumes: `ticketworker.BuildStartPlan`, `ticketworker.StartRequestID`, `planner.ParseExecutionPlanEnvelope`, `cli.ResolveZellijSession`, and `transport.ExecutionPlanPayload`.
- Produces: `AgentClient`, `ClientFactory`, expanded `Dependencies`, and the `runStart` submission path.

- [ ] **Step 1: Add failing CLI tests with a fake submission client**

```go
type fakeAgentClient struct {
    socketPath string
    timeout time.Duration
    requestID string
    payload transport.ExecutionPlanPayload
    err error
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
    c.requestID, c.payload = requestID, payload
    if c.err != nil { return transport.ExecutionPlanResponse{}, c.err }
    pane := payload.Tabs[0].Panes[0]
    return transport.ExecutionPlanResponse{RequestID: requestID, Session: payload.Session, Layout: payload.Layout, Tabs: []transport.ExecutionPlanTabResponse{{Name: payload.Tabs[0].Name, Panes: []transport.Pane{{ID: pane.ID, Role: pane.Role, Status: "starting"}}}}}, nil
}

func TestStartSubmitsTicketManagerPlan(t *testing.T) {
    h := newHarness(t)
    t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
    client := &fakeAgentClient{}
    h.deps.Executable = []string{"/opt/zellij-agent"}
    h.deps.NewClient = func(socket string, timeout time.Duration) AgentClient { client.socketPath, client.timeout = socket, timeout; return client }
    if got := h.run(t, "start", "--socket", "/tmp/tickets.sock", "--timeout", "2s"); got != ExitOK { t.Fatalf("exit=%d stderr=%s", got, h.stderr.String()) }
    if client.requestID != ticketworker.StartRequestID(client.payload.Session) || client.payload.ZellijSession != "physical-a" { t.Fatalf("submission = %#v", client) }
    if !strings.Contains(h.stdout.String(), "role=ticket-manager status=starting") { t.Fatalf("stdout=%q", h.stdout.String()) }
}
```

Implement these exact focused cases using the harness and fake above:

```go
func TestStartRejectsFormerTicketID(t *testing.T) {
    h := newHarness(t)
    if got := h.run(t, "start", "1"); got != ExitUsage { t.Fatalf("exit=%d stderr=%s", got, h.stderr.String()) }
    if !strings.Contains(h.stderr.String(), "start does not accept positional arguments") { t.Fatalf("stderr=%q", h.stderr.String()) }
}

func TestStartRequiresPositiveTimeout(t *testing.T) {
    h := newHarness(t)
    if got := h.run(t, "start", "--timeout", "0s"); got != ExitUsage { t.Fatalf("exit=%d", got) }
}

func TestStartRequiresZellijSession(t *testing.T) {
    h := newHarness(t); t.Setenv("ZELLIJ_SESSION_NAME", "")
    if got := h.run(t, "start"); got == ExitOK { t.Fatal("start succeeded without a Zellij session") }
}
```

Add `TestStartExplicitZellijSessionOverridesEnvironment`, `TestStartRejectsInvalidConfigWithoutSubmission`, `TestStartRejectsMissingDatabaseWithoutSubmission`, `TestStartReportsClientFailure`, and `TestStartReportsWriterFailure`; each assigns a fake client factory, invokes the named condition, checks a nonzero exit and the stage-specific stderr text, and checks `client.requestID == ""` for every pre-submission failure. Update `TestLifecycleCommandsApplyTransitions` to remove the `start` row and prepare `done`/in-progress `cancel` cases with `next`. Update the help assertion to require `start   Start the ticket manager pane`.

- [ ] **Step 2: Run focused CLI tests and verify failure**

Run: `go test ./internal/cli/ticketworker -run '^(TestStart|TestLifecycle|TestHelp)' -count=1`

Expected: FAIL because the dependency/client interfaces and launch behavior do not exist, and current `start` still expects an ID.

- [ ] **Step 3: Add the launch interfaces and route start before queue transitions**

```go
type AgentClient interface {
    SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}
type ClientFactory func(string, time.Duration) AgentClient

type Dependencies struct {
    StartDirectory string
    Now func() time.Time
    Executable []string
    NewClient ClientFactory
}
```

After root discovery and before the shared `OpenExisting` path, route `args[0] == "start"` to `runStart`. Remove `ActionStart` from the transition switch while retaining the store action internally for `next` and manager behavior.

- [ ] **Step 4: Implement start parsing, preconditions, plan validation, submission, and output**

```go
func runStart(ctx context.Context, root string, args []string, stdout, stderr io.Writer, deps Dependencies) int {
    flags := newFlagSet("start")
    flags.SetOutput(stderr)
    socketPath := flags.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
    timeout := flags.Duration("timeout", 15*time.Second, "request timeout")
    zellijSessionFlag := flags.String("zellij-session", "", "physical Zellij session")
    if err := flags.Parse(args); err != nil { return reportUsage(stderr, false, err.Error()) }
    if flags.NArg() != 0 { return reportUsage(stderr, false, "start does not accept positional arguments") }
    if *timeout <= 0 { return reportUsage(stderr, false, "start --timeout must be positive") }
    zellijSession, err := cli.ResolveZellijSession(*zellijSessionFlag)
    if err != nil { return reportError(stderr, false, err) }
    store, err := ticketworker.OpenExisting(ctx, root, deps.Now)
    if err != nil { return reportError(stderr, false, err) }
    if err := store.Close(); err != nil { return reportError(stderr, false, fmt.Errorf("close ticket database: %w", err)) }
    if _, err := ticketworker.LoadConfig(root); err != nil { return reportError(stderr, false, fmt.Errorf("load ticket-worker config: %w", err)) }
    payload, err := ticketworker.BuildStartPlan(ticketworker.StartPlanRequest{Root: root, ZellijSession: zellijSession, SocketPath: *socketPath, Executable: deps.Executable})
    if err != nil { return reportError(stderr, false, err) }
    requestID := ticketworker.StartRequestID(payload.Session)
    if err := validateStartEnvelope(requestID, payload); err != nil { return reportError(stderr, false, err) }
    if deps.NewClient == nil { return reportError(stderr, false, errors.New("ticket-worker start client is not configured")) }
    submitCtx, cancel := context.WithTimeout(ctx, *timeout); defer cancel()
    response, err := deps.NewClient(*socketPath, *timeout).SubmitExecutionPlan(submitCtx, requestID, payload)
    if err != nil { return reportError(stderr, false, fmt.Errorf("ticket-worker submit failed via socket %s: %w", *socketPath, err)) }
    return reportExecutionPlan(stdout, stderr, response)
}
```

Encode a `transport.RequestEnvelope`, validate it with `planner.ParseExecutionPlanEnvelope`, and add a concise response printer matching the established `work`/`chrome` output shape. Map session/config/start validation failures to the existing database-style failure code and flag/arity/timeout failures to `ExitUsage`.

- [ ] **Step 5: Format and run all ticket-worker CLI tests**

Run: `gofmt -w internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go && go test ./internal/cli/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the CLI behavior**

```bash
git add internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go
git commit -m "feat: launch ticket manager from ticket worker"
```

### Task 3: Unified Binary Wiring and User Documentation

**Files:**
- Modify: `cmd/zellij-agent/main.go`
- Modify: `cmd/zellij-agent/main_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `ticketworkercli.Dependencies`, `executablePath`, and `newAutoStartClient`.
- Produces: production dependency injection for the start command and updated public usage documentation.

- [ ] **Step 1: Add a failing unified dispatch test**

Extend the existing unified-binary help test to require `start   Start the ticket manager pane` and reject the old `Move a ready ticket to in_progress` description. Define the production factory as an overridable package variable:

```go
var newTicketWorkerClient ticketworkercli.ClientFactory = func(socketPath string, timeout time.Duration) ticketworkercli.AgentClient {
    return newAutoStartClient(socketPath, timeout)
}
```

In `TestUnifiedTicketWorkerStartDispatchesPlan`, initialize a temporary Git repository with `ticketworker.InitializeProject`, set `getWorkingDirectory` to its root, set `ZELLIJ_SESSION_NAME`, replace `newTicketWorkerClient` with a fake that captures request ID/payload, and restore both variables with `t.Cleanup`. Assert `run([]string{"ticket-worker", "start"}, ...) == 0`, the submitted pane role is `ticket-manager`, and its command starts with `executablePath(), "role", "ticket-manager"`.

- [ ] **Step 2: Run unified tests and verify failure**

Run: `go test ./cmd/zellij-agent -count=1`

Expected: FAIL because the unified command does not inject an executable or transport client for ticket-worker start and help still describes the old transition.

- [ ] **Step 3: Wire the production dependencies**

```go
var newTicketWorkerClient ticketworkercli.ClientFactory = func(socketPath string, timeout time.Duration) ticketworkercli.AgentClient {
    return newAutoStartClient(socketPath, timeout)
}
```

Pass `Executable: []string{executablePath()}` and `NewClient: newTicketWorkerClient` alongside `StartDirectory` and `Now`. Tests restore the overridable client factory with `t.Cleanup`.

- [ ] **Step 4: Update README command examples and semantics**

Replace:

```text
./bin/zellij-agent ticket-worker start ID
```

with:

```text
./bin/zellij-agent ticket-worker start [--zellij-session NAME]
```

Explain that start opens one runtime-managed ticket-manager tab, the manager claims ready tickets automatically, and `next` remains the explicit manual claim operation.

- [ ] **Step 5: Format and run unified tests**

Run: `gofmt -w cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go && go test ./cmd/zellij-agent ./internal/cli/ticketworker ./internal/ticketworker -count=1`

Expected: PASS.

- [ ] **Step 6: Commit wiring and documentation**

```bash
git add cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go README.md
git commit -m "docs: expose ticket manager start command"
```

### Task 4: Regression Verification and Atomic Binary Registration

**Files:**
- Verify only: all Go packages and generated `bin/zellij-agent`

**Interfaces:**
- Consumes: completed start implementation.
- Produces: tested and atomically registered unified binary.

- [ ] **Step 1: Run static formatting and diff checks**

Run: `gofmt -w internal/ticketworker/plan.go internal/ticketworker/plan_test.go internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go && git diff --check`

Expected: no output from `git diff --check`.

- [ ] **Step 2: Run the full unit suite**

Run: `go test ./...`

Expected: PASS for every package.

- [ ] **Step 3: Build the unified binary**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent`

Expected: exit status 0 and an executable `bin/zellij-agent`.

- [ ] **Step 4: Register the binary atomically on the custom CLI path**

```bash
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
```

Expected: `~/.config/custom-cli/zellij-agent` is executable and matches the newly built binary.

- [ ] **Step 5: Run non-mutating CLI smoke checks**

Run: `./bin/zellij-agent ticket-worker --help && ~/.config/custom-cli/zellij-agent ticket-worker --help`

Expected: both commands succeed, list `start` as starting the ticket manager pane, and do not describe `start ID`.

- [ ] **Step 6: Inspect final repository state**

Run: `git status --short && git log -5 --oneline`

Expected: only intentional generated/untracked artifacts, if any, remain; all source, test, plan, and documentation changes are committed.
