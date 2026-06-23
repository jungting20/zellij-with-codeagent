# Work Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `zellij-agent work "<goal>"`, a personal mixed-mode workspace launcher that creates coder, test, review, and notes panes through the existing execution-plan transport.

**Architecture:** Add a pure `internal/work` plan builder that converts a goal into a `transport.ExecutionPlanPayload`, then add `internal/cli/work` to parse flags, dry-run the generated envelope, or submit it through the daemon client. Wire the command into `cmd/zellij-agent` without changing runtime, transport, or Zellij backend boundaries.

**Tech Stack:** Go standard library, existing `internal/transport`, existing `internal/planner` envelope validation, existing `zellij-agent role coding-agent`, Go `testing`.

---

## File Structure

- Create `internal/work/work.go`: pure plan builder, session slugging, shell command construction, and request ID helper.
- Create `internal/work/work_test.go`: unit tests for plan shape, session generation, explicit session override, auto-test script, and role command prefix.
- Create `internal/cli/work/work.go`: CLI flag parsing, cwd resolution, dry-run envelope output, daemon submission, and response summaries.
- Create `internal/cli/work/work_test.go`: CLI tests with a fake daemon client for dry-run, submit, empty goal, cwd validation, and submit error messaging.
- Modify `cmd/zellij-agent/main.go`: import `internal/cli/work`, dispatch top-level `work`, pass the stable `zellij-agent role` command prefix, and show help.
- Modify `cmd/zellij-agent/main_test.go`: verify help lists `work` and top-level dry-run dispatch works without a daemon.
- Modify `README.md`: document the new personal `work` command, dry-run mode, auto-test mode, and daemon requirement.

Do not modify `internal/runtime`, `internal/zellij`, or `internal/transport` for this feature. The command must submit an existing `execution_plan` payload through the transport boundary.

---

### Task 1: Pure Work Plan Builder

**Files:**
- Create: `internal/work/work_test.go`
- Create: `internal/work/work.go`

- [ ] **Step 1: Write failing tests for generated mixed-mode plans**

Create `internal/work/work_test.go`:

```go
package work

import (
	"strings"
	"testing"
)

func TestBuildPlanCreatesMixedWorkPanes(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:        "implement the mixed work command",
		CWD:         "/tmp/app",
		RoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if payload.Session == "" || !strings.HasPrefix(payload.Session, "work-implement-the-mixed-work-command-") {
		t.Fatalf("Session = %q, want stable work slug", payload.Session)
	}
	if payload.Layout != "triple-horizontal" {
		t.Fatalf("Layout = %q, want triple-horizontal", payload.Layout)
	}
	if len(payload.Tabs) != 1 {
		t.Fatalf("Tabs = %#v, want one tab", payload.Tabs)
	}
	tab := payload.Tabs[0]
	if tab.Name != payload.Session {
		t.Fatalf("Tab name = %q, want session %q", tab.Name, payload.Session)
	}
	if len(tab.Panes) != 4 {
		t.Fatalf("Panes = %#v, want four panes", tab.Panes)
	}

	panes := tab.Panes
	if panes[0].ID != "coder" || panes[0].Role != "coding-agent" || panes[0].CWD != "/tmp/app" {
		t.Fatalf("coder pane = %#v, want coding-agent in cwd", panes[0])
	}
	if got := panes[0].Command; len(got) != 4 || got[0] != "/tmp/bin/zellij-agent" || got[1] != "role" || got[2] != "coding-agent" || got[3] != "/tmp/app" {
		t.Fatalf("coder command = %#v, want zellij-agent role coding-agent /tmp/app", got)
	}
	if panes[1].ID != "test" || panes[1].Role != "test-runner" || panes[1].Command[0] != "sh" || panes[1].Command[1] != "-lc" {
		t.Fatalf("test pane = %#v, want shell test runner", panes[1])
	}
	if !strings.Contains(panes[1].Command[2], "go test ./...") || strings.Contains(panes[1].Command[2], "$ go test ./...") {
		t.Fatalf("test script = %q, want suggested test command without auto execution", panes[1].Command[2])
	}
	if panes[2].ID != "review" || panes[2].Role != "review-assistant" || panes[2].Command[0] != "sh" || panes[2].Command[1] != "-lc" {
		t.Fatalf("review pane = %#v, want shell review assistant", panes[2])
	}
	if !strings.Contains(panes[2].Command[2], "codex exec --cd '/tmp/app' -") || !strings.Contains(panes[2].Command[2], "Do not edit files") {
		t.Fatalf("review script = %q, want non-editing codex exec review", panes[2].Command[2])
	}
	if panes[3].ID != "notes" || panes[3].Role != "notes" || panes[3].Command[0] != "sh" || panes[3].Command[1] != "-lc" {
		t.Fatalf("notes pane = %#v, want shell notes pane", panes[3])
	}
	if !strings.Contains(panes[3].Command[2], "zellij-agent ctl status") || !strings.Contains(panes[3].Command[2], "zellij-agent ctl cleanup --task") {
		t.Fatalf("notes script = %q, want control command hints", panes[3].Command[2])
	}
}

func TestBuildPlanUsesExplicitSession(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:    "fix parser",
		CWD:     "/tmp/app",
		Session: "custom-session",
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if payload.Session != "custom-session" || payload.Tabs[0].Name != "custom-session" {
		t.Fatalf("payload session/tab = %q/%q, want custom-session", payload.Session, payload.Tabs[0].Name)
	}
	if got := RequestID(payload.Session); got != "req_custom-session" {
		t.Fatalf("RequestID() = %q, want req_custom-session", got)
	}
}

func TestBuildPlanAutoTestRunsGoTestOnce(t *testing.T) {
	payload, err := BuildPlan(PlanRequest{
		Goal:     "run tests",
		CWD:      "/tmp/app",
		AutoTest: true,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	script := payload.Tabs[0].Panes[1].Command[2]
	if !strings.Contains(script, "printf '$ go test ./...\\n'") || !strings.Contains(script, "go test ./...") || !strings.Contains(script, "go test finished with exit=%s") {
		t.Fatalf("test script = %q, want auto go test execution and exit marker", script)
	}
}

func TestBuildPlanRejectsMissingInputs(t *testing.T) {
	if _, err := BuildPlan(PlanRequest{CWD: "/tmp/app"}); err == nil {
		t.Fatalf("BuildPlan() error = nil, want missing goal error")
	}
	if _, err := BuildPlan(PlanRequest{Goal: "ship it"}); err == nil {
		t.Fatalf("BuildPlan() error = nil, want missing cwd error")
	}
}

func TestSessionFromGoalHandlesNonASCII(t *testing.T) {
	got := SessionFromGoal("작업 기능 만들어줘")
	if !strings.HasPrefix(got, "work-goal-") {
		t.Fatalf("SessionFromGoal() = %q, want non-ASCII fallback slug", got)
	}
	if len(got) > 64 {
		t.Fatalf("SessionFromGoal() length = %d, want <= 64", len(got))
	}
}
```

- [ ] **Step 2: Run the new package test and verify it fails**

Run:

```bash
go test ./internal/work
```

Expected:

```text
FAIL	zellij-with-codeagent/internal/work [setup failed]
```

The failure should mention that package files do not exist or that `BuildPlan`, `PlanRequest`, `RequestID`, and `SessionFromGoal` are undefined.

- [ ] **Step 3: Add the pure work plan builder**

Create `internal/work/work.go`:

```go
package work

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"zellij-with-codeagent/internal/transport"
)

var ErrInvalidPlanRequest = errors.New("work: invalid plan request")

type PlanRequest struct {
	Goal        string
	CWD         string
	Session     string
	RoleCommand []string
	AutoTest    bool
}

func BuildPlan(req PlanRequest) (transport.ExecutionPlanPayload, error) {
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: goal is required", ErrInvalidPlanRequest)
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: cwd is required", ErrInvalidPlanRequest)
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		session = SessionFromGoal(goal)
	}
	roleCommand := normalizeRoleCommand(req.RoleCommand)

	return transport.ExecutionPlanPayload{
		Session: session,
		Layout:  "triple-horizontal",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name: session,
				Panes: []transport.ExecutionPlanPane{
					{
						ID:      "coder",
						Role:    "coding-agent",
						CWD:     cwd,
						Command: appendRoleCommand(roleCommand, "coding-agent", cwd),
					},
					{
						ID:      "test",
						Role:    "test-runner",
						CWD:     cwd,
						Command: []string{"sh", "-lc", testScript(session, goal, req.AutoTest)},
					},
					{
						ID:      "review",
						Role:    "review-assistant",
						CWD:     cwd,
						Command: []string{"sh", "-lc", reviewScript(cwd, goal)},
					},
					{
						ID:      "notes",
						Role:    "notes",
						CWD:     cwd,
						Command: []string{"sh", "-lc", notesScript(session, goal, cwd)},
					},
				},
			},
		},
	}, nil
}

func RequestID(session string) string {
	return "req_" + strings.TrimSpace(session)
}

func SessionFromGoal(goal string) string {
	trimmed := strings.TrimSpace(goal)
	slug := nonSessionChars.ReplaceAllString(strings.ToLower(trimmed), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "goal"
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
		if slug == "" {
			slug = "goal"
		}
	}
	return fmt.Sprintf("work-%s-%08x", slug, stableHash(trimmed))
}

var nonSessionChars = regexp.MustCompile(`[^a-z0-9]+`)

func stableHash(value string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum32()
}

func normalizeRoleCommand(command []string) []string {
	var normalized []string
	for _, part := range command {
		part = strings.TrimSpace(part)
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	if len(normalized) != 0 {
		return normalized
	}
	return []string{"zellij-agent", "role"}
}

func appendRoleCommand(command []string, args ...string) []string {
	result := make([]string, 0, len(command)+len(args))
	result = append(result, command...)
	result = append(result, args...)
	return result
}

func testScript(session, goal string, autoTest bool) string {
	lines := []string{
		"printf '[work:test] session=%s\\n' " + shellQuote(session),
		"printf '[work:test] goal=%s\\n\\n' " + shellQuote(goal),
	}
	if autoTest {
		lines = append(lines,
			"printf '$ go test ./...\\n'",
			"go test ./...",
			"status=$?",
			"printf '\\n[work:test] go test finished with exit=%s\\n' \"$status\"",
		)
	} else {
		lines = append(lines,
			"printf 'Run tests with:\\n  go test ./...\\n\\n'",
		)
	}
	lines = append(lines, "exec sh")
	return strings.Join(lines, "\n")
}

func reviewScript(cwd, goal string) string {
	prompt := strings.TrimSpace(`You are a review assistant for a local coding workspace.

Goal:
`+goal+`

Review the repository for this goal. Focus on:
- likely implementation risks
- missing or weak tests
- files that probably need attention
- practical next steps for the interactive coder

Do not edit files. Return concise findings first.`) + "\n"

	lines := []string{
		"printf '[work:review] running codex review assistant\\n\\n'",
		"printf '%s' " + shellQuote(prompt) + " | codex exec --cd " + shellQuote(cwd) + " -",
		"status=$?",
		"printf '\\n[work:review] codex review finished with exit=%s\\n' \"$status\"",
		"exec sh",
	}
	return strings.Join(lines, "\n")
}

func notesScript(session, goal, cwd string) string {
	lines := []string{
		"printf '[work:notes] session=%s\\n' " + shellQuote(session),
		"printf '[work:notes] cwd=%s\\n' " + shellQuote(cwd),
		"printf '[work:notes] goal=%s\\n\\n' " + shellQuote(goal),
		"printf 'Useful commands:\\n'",
		"printf '  zellij-agent ctl status\\n'",
		"printf '  zellij-agent ctl events --limit 20\\n'",
		"printf '  zellij-agent ctl snapshot coder --full\\n'",
		"printf '  zellij-agent ctl cleanup --task %s\\n\\n' " + shellQuote(session),
		"exec sh",
	}
	return strings.Join(lines, "\n")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
```

- [ ] **Step 4: Run builder tests and verify they pass**

Run:

```bash
go test ./internal/work
```

Expected:

```text
ok  	zellij-with-codeagent/internal/work
```

- [ ] **Step 5: Commit the builder**

Run:

```bash
git add internal/work/work.go internal/work/work_test.go
git commit -m "feat: add work plan builder"
```

Expected: commit succeeds with only the new `internal/work` files staged.

---

### Task 2: Work CLI Package

**Files:**
- Create: `internal/cli/work/work_test.go`
- Create: `internal/cli/work/work.go`

- [ ] **Step 1: Write failing CLI tests with a fake daemon client**

Create `internal/cli/work/work_test.go`:

```go
package workcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunDryRunPrintsExecutionPlanEnvelope(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--cwd", cwd,
		"--session", "work-command",
		"--dry-run",
		"implement", "work", "command",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("requestID = %q, want no submit during dry-run", client.requestID)
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_work-command" {
		t.Fatalf("envelope = %#v, want execution_plan req_work-command", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "work-command" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want work-command with four panes", payload)
	}
	if got := payload.Tabs[0].Panes[0].Command; len(got) != 4 || got[0] != "/tmp/bin/zellij-agent" || got[1] != "role" || got[2] != "coding-agent" || got[3] != cwd {
		t.Fatalf("coder command = %#v, want configured role command", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty dry-run stderr", stderr.String())
	}
}

func TestRunSubmitsGeneratedPlan(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--socket", "/tmp/custom.sock",
		"--timeout", "5s",
		"--cwd", cwd,
		"--session", "work-command",
		"--auto-test",
		"implement work command",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{
		DefaultRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	})

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" || client.timeout != 5*time.Second || client.requestID != "req_work-command" {
		t.Fatalf("client socket=%q timeout=%s request=%q, want custom socket timeout request", client.socketPath, client.timeout, client.requestID)
	}
	if client.payload.Session != "work-command" || len(client.payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want submitted work-command plan", client.payload)
	}
	if !strings.Contains(client.payload.Tabs[0].Panes[1].Command[2], "go test finished with exit=%s") {
		t.Fatalf("test command = %q, want auto-test marker", client.payload.Tabs[0].Panes[1].Command[2])
	}
	if !strings.Contains(stdout.String(), "request=req_work-command session=work-command") || !strings.Contains(stdout.String(), "- coder role=coding-agent") {
		t.Fatalf("stdout = %q, want work summary", stdout.String())
	}
}

func TestRunRejectsEmptyGoal(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", t.TempDir()}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 2 {
		t.Fatalf("Run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "work requires a goal") {
		t.Fatalf("stderr = %q, want missing goal message", stderr.String())
	}
}

func TestRunRejectsInvalidCWD(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")

	code := Run([]string{"--cwd", missing, "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve cwd") {
		t.Fatalf("stderr = %q, want cwd error", stderr.String())
	}
}

func TestRunSubmitErrorIncludesDaemonHint(t *testing.T) {
	cwd := t.TempDir()
	client := &fakeAgentClient{submitErr: errors.New("dial unix /tmp/agentd.sock: connect: no such file")}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--cwd", cwd, "ship it"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client), Config{})

	if code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "work submit failed via socket") || !strings.Contains(stderr.String(), "zellij-agent daemon serve") {
		t.Fatalf("stderr = %q, want submit error and daemon hint", stderr.String())
	}
}

func TestResolveCWDReturnsAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(old)
	})

	got, err := resolveCWD("")
	if err != nil {
		t.Fatalf("resolveCWD() error = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveCWD() = %q, want %q", got, dir)
	}
}

func fakeFactory(client *fakeAgentClient) ClientFactory {
	return func(socketPath string, timeout time.Duration) AgentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration
	requestID  string
	payload    transport.ExecutionPlanPayload
	submitErr  error
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	if c.submitErr != nil {
		return transport.ExecutionPlanResponse{}, c.submitErr
	}
	c.requestID = requestID
	c.payload = payload
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name: payload.Tabs[0].Name,
				Panes: []transport.Pane{
					{ID: "coder", Role: "coding-agent", Status: "running", ZellijPaneID: "terminal_1"},
					{ID: "test", Role: "test-runner", Status: "running", ZellijPaneID: "terminal_2"},
					{ID: "review", Role: "review-assistant", Status: "running", ZellijPaneID: "terminal_3"},
					{ID: "notes", Role: "notes", Status: "running", ZellijPaneID: "terminal_4"},
				},
			},
		},
	}, nil
}
```

- [ ] **Step 2: Run CLI package tests and verify they fail**

Run:

```bash
go test ./internal/cli/work
```

Expected:

```text
FAIL	zellij-with-codeagent/internal/cli/work [setup failed]
```

The failure should mention the missing package or undefined `Run`, `Config`, `ClientFactory`, `AgentClient`, and `resolveCWD` identifiers.

- [ ] **Step 3: Add the work CLI implementation**

Create `internal/cli/work/work.go`:

```go
package workcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/planner"
	workplan "zellij-with-codeagent/internal/work"
	"zellij-with-codeagent/internal/transport"
)

type AgentClient interface {
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

type ClientFactory func(socketPath string, timeout time.Duration) AgentClient

type Config struct {
	DefaultRoleCommand []string
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, newClient ClientFactory, cfg Config) int {
	fs := flag.NewFlagSet("work", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	cwdFlag := fs.String("cwd", "", "working directory; defaults to current directory")
	session := fs.String("session", "", "execution session/task id override")
	dryRun := fs.Bool("dry-run", false, "print the /v1/requests envelope without submitting it")
	autoTest := fs.Bool("auto-test", false, "run go test ./... once in the test pane before leaving a shell open")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	goal := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if goal == "" {
		fmt.Fprintln(stderr, "work requires a goal")
		return 2
	}

	cwd, err := resolveCWD(*cwdFlag)
	if err != nil {
		fmt.Fprintf(stderr, "resolve cwd: %v\n", err)
		return 1
	}

	payload, err := workplan.BuildPlan(workplan.PlanRequest{
		Goal:        goal,
		CWD:         cwd,
		Session:     *session,
		RoleCommand: cfg.DefaultRoleCommand,
		AutoTest:    *autoTest,
	})
	if err != nil {
		fmt.Fprintf(stderr, "build work plan: %v\n", err)
		return 1
	}
	requestID := workplan.RequestID(payload.Session)
	envelope, err := executionPlanEnvelope(requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "encode work plan: %v\n", err)
		return 1
	}
	if _, err := planner.ParseExecutionPlanEnvelope(mustMarshalEnvelope(envelope)); err != nil {
		fmt.Fprintf(stderr, "validate generated work plan: %v\n", err)
		return 1
	}

	if *dryRun {
		if err := writeEnvelope(stdout, envelope); err != nil {
			fmt.Fprintf(stderr, "write dry-run envelope: %v\n", err)
			return 1
		}
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	response, err := newClient(*socketPath, *timeout).SubmitExecutionPlan(ctx, requestID, payload)
	if err != nil {
		fmt.Fprintf(stderr, "work submit failed via socket %s: %v\n", *socketPath, err)
		fmt.Fprintln(stderr, "hint: start the daemon with zellij-agent daemon serve")
		return 1
	}
	printExecutionPlanResponse(stdout, response)
	return 0
}

func resolveCWD(value string) (string, error) {
	cwd := strings.TrimSpace(value)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("cwd must be a directory")
	}
	return abs, nil
}

func executionPlanEnvelope(requestID string, payload transport.ExecutionPlanPayload) (transport.RequestEnvelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return transport.RequestEnvelope{}, err
	}
	return transport.RequestEnvelope{
		Type:      transport.RequestTypeExecutionPlan,
		RequestID: requestID,
		Payload:   raw,
	}, nil
}

func writeEnvelope(w io.Writer, envelope transport.RequestEnvelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

func mustMarshalEnvelope(envelope transport.RequestEnvelope) []byte {
	data, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return data
}

func printExecutionPlanResponse(w io.Writer, response transport.ExecutionPlanResponse) {
	paneCount := 0
	for _, tab := range response.Tabs {
		paneCount += len(tab.Panes)
	}
	fmt.Fprintf(w, "request=%s session=%s layout=%s tabs=%d panes=%d\n",
		response.RequestID,
		response.Session,
		response.Layout,
		len(response.Tabs),
		paneCount,
	)
	for _, tab := range response.Tabs {
		fmt.Fprintf(w, "tab=%s panes=%d\n", tab.Name, len(tab.Panes))
		for _, pane := range tab.Panes {
			if pane.ZellijPaneID != "" {
				fmt.Fprintf(w, "- %s role=%s status=%s zellij=%s\n", pane.ID, pane.Role, pane.Status, pane.ZellijPaneID)
				continue
			}
			fmt.Fprintf(w, "- %s role=%s status=%s\n", pane.ID, pane.Role, pane.Status)
		}
	}
}
```

- [ ] **Step 4: Run CLI package tests and verify they pass**

Run:

```bash
go test ./internal/cli/work
```

Expected:

```text
ok  	zellij-with-codeagent/internal/cli/work
```

- [ ] **Step 5: Commit the CLI package**

Run:

```bash
git add internal/cli/work/work.go internal/cli/work/work_test.go
git commit -m "feat: add work command cli"
```

Expected: commit succeeds with only the new CLI package files staged.

---

### Task 3: Top-Level Command Wiring

**Files:**
- Modify: `cmd/zellij-agent/main_test.go`
- Modify: `cmd/zellij-agent/main.go`

- [ ] **Step 1: Add failing top-level dispatch tests**

Modify `cmd/zellij-agent/main_test.go` so the imports include `encoding/json`, `os`, `zellij-with-codeagent/internal/transport`, and the help test checks for `work`:

```go
import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"zellij-with-codeagent/internal/transport"
)
```

Update the help assertion inside `TestRunHelp`:

```go
if !strings.Contains(stdout.String(), "Usage: zellij-agent") || !strings.Contains(stdout.String(), "planner") || !strings.Contains(stdout.String(), "work") {
	t.Fatalf("stdout = %q, want unified usage with work command", stdout.String())
}
```

Add this new test:

```go
func TestRunDispatchesWorkDryRun(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := run([]string{"work", "--cwd", cwd, "--session", "work-command", "--dry-run", "implement work command"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var envelope transport.RequestEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("dry-run JSON decode error = %v; output=%q", err, stdout.String())
	}
	if envelope.Type != transport.RequestTypeExecutionPlan || envelope.RequestID != "req_work-command" {
		t.Fatalf("envelope = %#v, want work execution plan", envelope)
	}
	var payload transport.ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload decode error = %v", err)
	}
	if payload.Session != "work-command" || len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 4 {
		t.Fatalf("payload = %#v, want work-command with four panes", payload)
	}
	coderCommand := payload.Tabs[0].Panes[0].Command
	if len(coderCommand) != 4 || coderCommand[1] != "role" || coderCommand[2] != "coding-agent" || coderCommand[3] != cwd {
		t.Fatalf("coder command = %#v, want executable role coding-agent cwd", coderCommand)
	}
	if _, err := os.Stat(coderCommand[0]); err != nil {
		t.Fatalf("coder command executable = %q is not stat-able: %v", coderCommand[0], err)
	}
}
```

- [ ] **Step 2: Run top-level command tests and verify they fail**

Run:

```bash
go test ./cmd/zellij-agent
```

Expected:

```text
FAIL	zellij-with-codeagent/cmd/zellij-agent
```

The failure should show that `work` is not in usage or is an unknown command group.

- [ ] **Step 3: Wire `work` into the unified binary**

Modify `cmd/zellij-agent/main.go` imports to add `workcli`:

```go
import (
	"fmt"
	"io"
	"os"
	"time"

	ctlcli "zellij-with-codeagent/internal/cli/ctl"
	daemoncli "zellij-with-codeagent/internal/cli/daemon"
	plannercli "zellij-with-codeagent/internal/cli/planner"
	rolecli "zellij-with-codeagent/internal/cli/role"
	workcli "zellij-with-codeagent/internal/cli/work"
	"zellij-with-codeagent/internal/transport"
)
```

Add the switch case:

```go
case "work":
	return workcli.Run(args[1:], stdin, stdout, stderr, newWorkClient, workcli.Config{
		DefaultRoleCommand: []string{executablePath(), "role"},
	})
```

Add the client factory:

```go
func newWorkClient(socketPath string, timeout time.Duration) workcli.AgentClient {
	return transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
}
```

Add the usage line:

```go
fmt.Fprintln(w, "  work     Start a personal mixed-mode coding workspace")
```

- [ ] **Step 4: Run top-level command tests and verify they pass**

Run:

```bash
go test ./cmd/zellij-agent
```

Expected:

```text
ok  	zellij-with-codeagent/cmd/zellij-agent
```

- [ ] **Step 5: Commit command wiring**

Run:

```bash
git add cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
git commit -m "feat: wire work command"
```

Expected: commit succeeds with only top-level command files staged.

---

### Task 4: README and Full Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add README usage text**

In `README.md`, add this section after the `zellij-agent ctl` command examples:

````markdown
### Personal Work Launcher

`zellij-agent work` starts a daemon-managed mixed coding workspace for the current repository:

```bash
./bin/zellij-agent work "implement the mixed work command"
```

The command creates one Zellij tab with four panes:

- `coder`: interactive Codex session through `zellij-agent role coding-agent <cwd>`.
- `test`: test shell prepared for `go test ./...`.
- `review`: non-interactive Codex review assistant seeded with the goal.
- `notes`: session notes and useful `zellij-agent ctl` commands.

Useful options:

```bash
./bin/zellij-agent work --dry-run "implement the mixed work command"
./bin/zellij-agent work --session work-command "implement the mixed work command"
./bin/zellij-agent work --cwd /path/to/repo "implement the mixed work command"
./bin/zellij-agent work --auto-test "implement the mixed work command"
```

The daemon must be running before non-dry-run submission:

```bash
./bin/zellij-agent daemon serve
```
````

- [ ] **Step 2: Run the focused test packages**

Run:

```bash
go test ./internal/work ./internal/cli/work ./cmd/zellij-agent
```

Expected:

```text
ok  	zellij-with-codeagent/internal/work
ok  	zellij-with-codeagent/internal/cli/work
ok  	zellij-with-codeagent/cmd/zellij-agent
```

- [ ] **Step 3: Run the full unit test suite**

Run:

```bash
go test ./...
```

Expected: every package passes.

- [ ] **Step 4: Build and register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Expected: both commands exit with status 0.

- [ ] **Step 5: Verify dry-run output from the built binary**

Run:

```bash
./bin/zellij-agent work --cwd "$PWD" --session work-command --dry-run "implement the mixed work command"
```

Expected: JSON envelope with:

```json
{
  "type": "execution_plan",
  "request_id": "req_work-command"
}
```

The nested payload should contain `session: "work-command"` and panes named `coder`, `test`, `review`, and `notes`.

- [ ] **Step 6: Commit README and verification-ready changes**

Run:

```bash
git add README.md
git commit -m "docs: document work command"
```

Expected: commit succeeds with only README staged. If earlier implementation tasks were not committed separately, stage the related implementation files together and use:

```bash
git add internal/work internal/cli/work cmd/zellij-agent README.md
git commit -m "feat: add mixed work command"
```

---

## Self-Review Notes

- Spec coverage: this plan covers top-level `zellij-agent work`, fixed mixed pane generation, dry-run envelope output, session/cwd/auto-test options, transport submission, daemon hint errors, tests, README docs, full `go test ./...`, build, and custom-cli registration.
- Runtime boundary: no task modifies `internal/runtime`, `internal/zellij`, or daemon internals. The new CLI submits `transport.ExecutionPlanPayload` through the existing client.
- Type consistency: `workplan.PlanRequest`, `workplan.BuildPlan`, `workplan.RequestID`, `workcli.Run`, `workcli.Config`, `workcli.ClientFactory`, and `workcli.AgentClient` are used consistently across tasks.
