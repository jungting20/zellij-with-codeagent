# Debate Coordinator Role Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `agent-role debate-coordinator <path>` role that starts as a waiting coordinator pane, accepts a collected synthesis block after agent answers finish, runs `codex exec` only then, and emits the configured completion marker.

**Architecture:** `ctl debate` remains the orchestrator: it creates the debate tab, sends prompts, waits for agent markers, collects snapshots, sends one synthesis block to the coordinator, and waits for the coordinator marker. The new `debate-coordinator` role owns delayed Codex startup inside a pre-existing pane, so the coordinator pane is visible from the beginning without launching Codex before the synthesis prompt is ready.

**Tech Stack:** Go standard library, existing `agent-role` dispatch pattern, existing transport APIs, local Codex CLI `codex exec --cd <repo> -`.

---

## Current Context And Desired End State

The current debate MVP has moved through two designs:

- Earlier design: `debate-coordinator` was a `coding-agent` pane from the start. This caused unwanted Codex initialization before synthesis input existed.
- Current dirty design may have: `ctl debate` starts only agent panes, then lazily calls `CreatePane` for `debate-coordinator` after agent markers.

This plan replaces both with a role-based design:

```text
Initial execution plan:
  debate-coordinator  role=debate-coordinator  command=zellij-agent role debate-coordinator <repo>
  debate-a            role=coding-agent
  debate-b            role=coding-agent
  debate-c            role=coding-agent

After agent markers:
  ctl debate snapshots debate-a/b/c
  ctl debate sends a synthesis block to debate-coordinator via SendInput
  debate-coordinator role runs codex exec --cd <repo> -
  debate-coordinator role prints the completion marker after codex exits
  ctl debate snapshots debate-coordinator
```

The synthesis block format is line-oriented so it works over terminal input:

```text
<<<DEBATE_SYNTHESIS_BEGIN>>>
Completion-Marker: <<<AGENT_DEBATE_DONE debate=debate_123 round=1 agent=coordinator token=debate_123-1-coordinator>>>
Topic: Should we use a coordinator role?

[debate-a]
answer from a

[debate-b]
answer from b
<<<DEBATE_SYNTHESIS_END>>>
```

The role should pass everything except the wrapper lines to Codex. It should print the completion marker itself after `codex exec` exits, instead of relying on Codex to obey marker instructions. This gives `ctl debate` a reliable marker.

## File Structure

- Create `cmd/agent-role/debatecoordinator/debatecoordinator.go`: role implementation, synthesis block parser, delayed `codex exec` runner.
- Create `cmd/agent-role/debatecoordinator/debatecoordinator_test.go`: parser, command preparation, and fake Codex execution tests.
- Modify `internal/roles/roles.go`: add catalog metadata for `debate-coordinator`.
- Modify `internal/roles/roles_test.go`: assert the new role is listed with correct usage.
- Modify `internal/cli/role/role.go`: dispatch `debate-coordinator` to the new package.
- Modify `internal/cli/role/role_test.go`: assert role dispatch recognizes `debate-coordinator`.
- Modify `internal/cli/ctl/ctl.go`: update `ctl debate` to start coordinator role from the initial execution plan and remove lazy `CreatePane` coordinator creation from this command.
- Modify `cmd/agentctl/main_test.go`: update debate tests for initial coordinator role pane and synthesis block input.
- Modify `/Users/in05908_mac/.config/pi/docs/agent-roles.md`: document the new role.

## Task 1: Add Debate Coordinator Role Catalog Entry

**Files:**
- Modify: `internal/roles/roles.go`
- Test: `internal/roles/roles_test.go`

- [ ] **Step 1: Write the failing catalog test**

Add or extend a test in `internal/roles/roles_test.go`:

```go
func TestLookupDebateCoordinator(t *testing.T) {
	spec, ok := roles.Lookup(roles.RoleDebateCoordinator)
	if !ok {
		t.Fatal("Lookup(RoleDebateCoordinator) ok = false, want true")
	}
	if spec.Name != "debate-coordinator" {
		t.Fatalf("name = %q, want debate-coordinator", spec.Name)
	}
	if spec.Usage != "debate-coordinator <path>" {
		t.Fatalf("usage = %q, want debate-coordinator <path>", spec.Usage)
	}
	if len(spec.Arguments) != 1 || spec.Arguments[0].Name != "path" || !spec.Arguments[0].Required {
		t.Fatalf("arguments = %#v, want required path", spec.Arguments)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/roles -run TestLookupDebateCoordinator -count=1
```

Expected: FAIL because `RoleDebateCoordinator` does not exist.

- [ ] **Step 3: Add role metadata**

Add to `internal/roles/roles.go` constants:

```go
RoleDebateCoordinator = "debate-coordinator"
```

Add to `specs`:

```go
{
	Name:        RoleDebateCoordinator,
	Usage:       "debate-coordinator <path>",
	Description: "Waits for debate synthesis input, then runs Codex to produce the coordinator summary.",
	Arguments: []ArgumentSpec{
		{Name: "path", Required: true, Description: "File or directory path inside the repository where Codex should run."},
	},
},
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/roles -run TestLookupDebateCoordinator -count=1
```

Expected: PASS.

## Task 2: Implement The Debate Coordinator Role Parser

**Files:**
- Create: `cmd/agent-role/debatecoordinator/debatecoordinator.go`
- Create: `cmd/agent-role/debatecoordinator/debatecoordinator_test.go`

- [ ] **Step 1: Write failing parser tests**

Create `cmd/agent-role/debatecoordinator/debatecoordinator_test.go` with tests:

```go
package debatecoordinator

import (
	"strings"
	"testing"
)

func TestReadSynthesisBlockParsesMarkerAndPrompt(t *testing.T) {
	input := strings.NewReader(`noise before
<<<DEBATE_SYNTHESIS_BEGIN>>>
Completion-Marker: <<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>
Topic: marker design

[debate-a]
answer from a
<<<DEBATE_SYNTHESIS_END>>>
noise after
`)

	block, err := readSynthesisBlock(input)
	if err != nil {
		t.Fatalf("readSynthesisBlock() error = %v", err)
	}
	if block.CompletionMarker != "<<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>" {
		t.Fatalf("marker = %q", block.CompletionMarker)
	}
	if !strings.Contains(block.Prompt, "Topic: marker design") || !strings.Contains(block.Prompt, "answer from a") {
		t.Fatalf("prompt = %q, want topic and answer", block.Prompt)
	}
	if strings.Contains(block.Prompt, "DEBATE_SYNTHESIS_BEGIN") || strings.Contains(block.Prompt, "DEBATE_SYNTHESIS_END") {
		t.Fatalf("prompt = %q, wrapper markers should be excluded", block.Prompt)
	}
}

func TestReadSynthesisBlockRequiresCompletionMarker(t *testing.T) {
	input := strings.NewReader("<<<DEBATE_SYNTHESIS_BEGIN>>>\nTopic: no marker\n<<<DEBATE_SYNTHESIS_END>>>\n")

	_, err := readSynthesisBlock(input)
	if err == nil || !strings.Contains(err.Error(), "completion marker") {
		t.Fatalf("error = %v, want completion marker error", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator -run 'TestReadSynthesisBlock' -count=1
```

Expected: FAIL because the package/functions do not exist.

- [ ] **Step 3: Implement parser**

Create `cmd/agent-role/debatecoordinator/debatecoordinator.go`:

```go
package debatecoordinator

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	synthesisBegin = "<<<DEBATE_SYNTHESIS_BEGIN>>>"
	synthesisEnd   = "<<<DEBATE_SYNTHESIS_END>>>"
	markerPrefix   = "Completion-Marker:"
)

type synthesisBlock struct {
	CompletionMarker string
	Prompt           string
}

func readSynthesisBlock(r io.Reader) (synthesisBlock, error) {
	scanner := bufio.NewScanner(r)
	inBlock := false
	var marker string
	var lines []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if trimmed == synthesisBegin {
				inBlock = true
			}
			continue
		}
		if trimmed == synthesisEnd {
			if marker == "" {
				return synthesisBlock{}, errors.New("debate coordinator: completion marker is required")
			}
			return synthesisBlock{
				CompletionMarker: marker,
				Prompt:           strings.TrimSpace(strings.Join(lines, "\n")) + "\n",
			}, nil
		}
		if strings.HasPrefix(line, markerPrefix) {
			marker = strings.TrimSpace(strings.TrimPrefix(line, markerPrefix))
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return synthesisBlock{}, err
	}
	if inBlock {
		return synthesisBlock{}, fmt.Errorf("debate coordinator: missing %s", synthesisEnd)
	}
	return synthesisBlock{}, fmt.Errorf("debate coordinator: missing %s", synthesisBegin)
}
```

- [ ] **Step 4: Run parser tests**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator -run 'TestReadSynthesisBlock' -count=1
```

Expected: PASS.

## Task 3: Implement Delayed Codex Exec Runner

**Files:**
- Modify: `cmd/agent-role/debatecoordinator/debatecoordinator.go`
- Modify: `cmd/agent-role/debatecoordinator/debatecoordinator_test.go`

- [ ] **Step 1: Write failing command preparation test**

Append to `debatecoordinator_test.go`:

```go
func TestPrepareCodexCommandUsesExecWithPromptOnStdin(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	cmd, err := prepareCodexCommand(repo, "summarize this")
	if err != nil {
		t.Fatalf("prepareCodexCommand() error = %v", err)
	}
	if cmd.Path != codexPath {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, codexPath)
	}
	if strings.Join(cmd.Args[1:], " ") != "exec --cd "+repo+" -" {
		t.Fatalf("cmd.Args = %#v, want codex exec --cd repo -", cmd.Args)
	}
	if cmd.Dir != repo {
		t.Fatalf("cmd.Dir = %q, want repo", cmd.Dir)
	}
}
```

Add imports:

```go
import (
	"os"
	"path/filepath"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator -run TestPrepareCodexCommandUsesExecWithPromptOnStdin -count=1
```

Expected: FAIL because `prepareCodexCommand` is undefined.

- [ ] **Step 3: Implement command preparation and repository resolution**

Add to `debatecoordinator.go`:

```go
import (
	"os"
	"os/exec"
	"path/filepath"
)

func prepareCodexCommand(path string, prompt string) (*exec.Cmd, error) {
	repoPath, err := resolveRepositoryPath(path)
	if err != nil {
		return nil, err
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex executable not found on PATH")
	}
	cmd := exec.Command(codexPath, "exec", "--cd", repoPath, "-")
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(prompt)
	return cmd, nil
}

func resolveRepositoryPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("path %q is not accessible: %w", absPath, err)
	}
	searchPath := absPath
	if !info.IsDir() {
		searchPath = filepath.Dir(absPath)
	}
	for {
		if _, err := os.Stat(filepath.Join(searchPath, ".git")); err == nil {
			return searchPath, nil
		}
		parent := filepath.Dir(searchPath)
		if parent == searchPath {
			break
		}
		searchPath = parent
	}
	return "", fmt.Errorf("path %q is not inside a git repository", absPath)
}
```

- [ ] **Step 4: Run command preparation test**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator -run TestPrepareCodexCommandUsesExecWithPromptOnStdin -count=1
```

Expected: PASS.

## Task 4: Implement Role Run With IO And Marker Ownership

**Files:**
- Modify: `cmd/agent-role/debatecoordinator/debatecoordinator.go`
- Modify: `cmd/agent-role/debatecoordinator/debatecoordinator_test.go`

- [ ] **Step 1: Write failing end-to-end role test with fake Codex**

Append:

```go
func TestRunWithIOExecutesCodexAfterBlockAndPrintsMarker(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
printf 'fake codex synthesis\n'
cat >/tmp/debate-coordinator-prompt.txt
`
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	input := strings.NewReader(`<<<DEBATE_SYNTHESIS_BEGIN>>>
Completion-Marker: <<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>
Topic: fake
<<<DEBATE_SYNTHESIS_END>>>
`)
	var stdout, stderr strings.Builder

	code := runWithIO([]string{repo}, input, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runWithIO() code = %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "debate_coordinator_ready") ||
		!strings.Contains(output, "fake codex synthesis") ||
		!strings.Contains(output, "<<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>") {
		t.Fatalf("stdout = %q, want ready, codex output, and marker", output)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator -run TestRunWithIOExecutesCodexAfterBlockAndPrintsMarker -count=1
```

Expected: FAIL because `runWithIO` is undefined.

- [ ] **Step 3: Implement `Run` and `runWithIO`**

Add:

```go
func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Error: usage: agent-role debate-coordinator <path>")
		return 1
	}
	fmt.Fprintln(stdout, "debate_coordinator_ready")
	fmt.Fprintln(stdout, "waiting for synthesis input...")

	block, err := readSynthesisBlock(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cmd, err := prepareCodexCommand(args[0], block.Prompt)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "Error running codex: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, block.CompletionMarker)
	return 0
}
```

Ensure imports include `errors`.

- [ ] **Step 4: Run role package tests**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator -count=1
```

Expected: PASS.

## Task 5: Wire Role Dispatch

**Files:**
- Modify: `internal/cli/role/role.go`
- Test: `internal/cli/role/role_test.go`

- [ ] **Step 1: Write failing dispatch test**

Add a test that proves the role is not unknown. Use a temp repo and fake `codex` so dispatch can run to completion with a block:

```go
func TestRunDispatchesDebateCoordinator(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	input := "<<<DEBATE_SYNTHESIS_BEGIN>>>\nCompletion-Marker: <<<DONE>>>\nTopic: x\n<<<DEBATE_SYNTHESIS_END>>>\n"
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	t.Cleanup(func() { os.Stdin = oldStdin })
	os.Stdin = r
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if code := Run([]string{"debate-coordinator", repo}); code != 0 {
		t.Fatalf("Run(debate-coordinator) = %d, want 0", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/cli/role -run TestRunDispatchesDebateCoordinator -count=1
```

Expected: FAIL with unknown role.

- [ ] **Step 3: Wire dispatch**

In `internal/cli/role/role.go`, import:

```go
"zellij-with-codeagent/cmd/agent-role/debatecoordinator"
```

Add switch case:

```go
case roles.RoleDebateCoordinator:
	return debatecoordinator.Run(args[1:])
```

- [ ] **Step 4: Run dispatch test**

Run:

```bash
go test ./internal/cli/role -run TestRunDispatchesDebateCoordinator -count=1
```

Expected: PASS.

## Task 6: Update `ctl debate` To Use The Coordinator Role From The Start

**Files:**
- Modify: `internal/cli/ctl/ctl.go`
- Modify: `cmd/agentctl/main_test.go`

- [ ] **Step 1: Write/update failing tests**

Update `TestRunDebateSubmitsPlan` so the initial panes are:

```go
wantIDs := []string{"debate-coordinator", "debate-a", "debate-b", "debate-c"}
```

Assert coordinator pane role:

```go
if panes[0].Role != "debate-coordinator" || !slices.Contains(panes[0].Command, "debate-coordinator") {
	t.Fatalf("coordinator pane = %#v, want debate-coordinator role command", panes[0])
}
```

Update or add `TestRunDebateSendsSynthesisBlockToCoordinator`:

```go
synthesisInputs := filterInputRequests(client.inputRequests, "debate-coordinator")
if len(synthesisInputs) != 1 {
	t.Fatalf("coordinator inputs = %#v, want one synthesis block", synthesisInputs)
}
text := synthesisInputs[0].req.Text
if !strings.Contains(text, "<<<DEBATE_SYNTHESIS_BEGIN>>>") ||
	!strings.Contains(text, "Completion-Marker: <<<AGENT_DEBATE_DONE") ||
	!strings.Contains(text, "[debate-a]") ||
	!strings.Contains(text, "answer from a") ||
	!strings.Contains(text, "<<<DEBATE_SYNTHESIS_END>>>") {
	t.Fatalf("synthesis input = %q, want coordinator block", text)
}
if len(client.createPaneRequests) != 0 {
	t.Fatalf("create pane requests = %#v, want coordinator created by initial plan", client.createPaneRequests)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./cmd/agentctl -run 'TestRunDebate' -count=1
```

Expected: FAIL while `ctl debate` still uses lazy `CreatePane` or sends the old synthesis prompt format.

- [ ] **Step 3: Update `debateExecutionPlan`**

In `internal/cli/ctl/ctl.go`, make coordinator the first pane again, but with the new role:

```go
panes := []transport.ExecutionPlanPane{
	{
		ID:      "debate-coordinator",
		Role:    "debate-coordinator",
		AgentID: "coordinator",
		Command: append(cloneStringSlice(roleCommand), "debate-coordinator", cwd),
		CWD:     cwd,
	},
}
```

Then append `debate-a/b/c` coding-agent panes as before.

- [ ] **Step 4: Remove lazy coordinator creation from `runDebate`**

Delete the `debateResponseTabID` call and `client.CreatePane(...)` block. If `CreatePane` is no longer used by `ctl`, remove it from the `AgentClient` interface and fake client unless other tests require it.

- [ ] **Step 5: Send synthesis block via `SendInput`**

Replace `debateSynthesisPrompt` with a block formatter:

```go
func debateSynthesisBlock(topic string, agents []string, outputs map[string]string, marker string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "<<<DEBATE_SYNTHESIS_BEGIN>>>")
	fmt.Fprintf(&b, "Completion-Marker: %s\n", marker)
	fmt.Fprintf(&b, "Topic: %s\n\n", topic)
	fmt.Fprintln(&b, "You are the debate coordinator. Read all agent answers below and produce a concise synthesis.")
	fmt.Fprintln(&b, "Include consensus, disagreements, strongest arguments, weak assumptions, and a final recommendation.")
	fmt.Fprintln(&b)
	for _, agent := range agents {
		paneID := "debate-" + agent
		fmt.Fprintf(&b, "[%s]\n%s\n\n", paneID, outputs[paneID])
	}
	fmt.Fprintln(&b, "<<<DEBATE_SYNTHESIS_END>>>")
	return b.String()
}
```

Call:

```go
if err := client.SendInput(ctx, "debate-coordinator", transport.SendInputRequest{
	Text: debateSynthesisBlock(*topic, agents, agentOutputs, coordinatorMarker),
}); err != nil {
	fmt.Fprintf(stderr, "agentctl debate synthesis prompt failed: %v\n", err)
	return 1
}
```

- [ ] **Step 6: Run debate tests**

Run:

```bash
go test ./cmd/agentctl -run 'TestRunDebate' -count=1
```

Expected: PASS.

## Task 7: Update External Role Summary Document

**Files:**
- Modify or create: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

- [ ] **Step 1: Build agent-role and inspect roles**

Run:

```bash
go build -o bin/agent-role ./cmd/agent-role
./bin/agent-role roles
```

Expected: output includes:

```text
debate-coordinator <path>
```

- [ ] **Step 2: Update document**

Add an entry:

```markdown
## debate-coordinator

- Usage: `agent-role debate-coordinator <path>`
- Purpose: Waits in a coordinator pane for a debate synthesis block, then runs `codex exec --cd <repo> -` to produce a final synthesis.
- Required arguments: `<path>` must be inside the target Git repository.
- Input protocol: Reads terminal stdin until `<<<DEBATE_SYNTHESIS_BEGIN>>>`, requires a `Completion-Marker:` line, collects prompt text until `<<<DEBATE_SYNTHESIS_END>>>`.
- Runtime requirements: `codex` must be installed on `PATH`; the path must resolve to a Git repository.
```

- [ ] **Step 3: Verify document contains role**

Run:

```bash
rg -n "debate-coordinator|DEBATE_SYNTHESIS_BEGIN|codex exec" /Users/in05908_mac/.config/pi/docs/agent-roles.md
```

Expected: all three patterns are present.

## Task 8: Final Verification And Binary Registration

**Files:**
- All modified Go files.

- [ ] **Step 1: Format Go files**

Run:

```bash
gofmt -w internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go internal/cli/ctl/ctl.go cmd/agentctl/main_test.go cmd/agent-role/debatecoordinator/debatecoordinator.go cmd/agent-role/debatecoordinator/debatecoordinator_test.go
```

Expected: command exits 0.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./cmd/agent-role/debatecoordinator ./internal/roles ./internal/cli/role ./cmd/agentctl -run 'Test.*Debate|TestLookupDebateCoordinator|TestRunDispatchesDebateCoordinator' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Build role binary and unified binary**

Run:

```bash
go build -o bin/agent-role ./cmd/agent-role
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Expected: all commands exit 0.

- [ ] **Step 5: Verify role listing**

Run:

```bash
./bin/agent-role roles | rg "debate-coordinator"
```

Expected: output contains `debate-coordinator <path>`.

## Self-Review

- Spec coverage: the plan adds the role catalog entry, role implementation, role dispatch, `ctl debate` integration, tests, external docs, and binary registration.
- Placeholder scan: no placeholder tasks remain; each task has concrete files, code snippets, commands, and expected outcomes.
- Type consistency: role name is `debate-coordinator`, Go package is `debatecoordinator`, catalog constant is `RoleDebateCoordinator`, and transport inputs use existing `SendInput`/execution-plan types.
