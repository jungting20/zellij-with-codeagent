# List Selector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the standalone code-agent-list-selector TUI into this module and expose it as `zellij-agent list-selector` without direct Zellij pane manipulation.

**Architecture:** `internal/listselector` owns the Bubble Tea model and command construction. `internal/cli/listselector` owns CLI parsing, terminal I/O, and exit-code translation, while `cmd/zellij-agent` only dispatches the new command. The port preserves the standalone selector behavior but uses the repository's unified module and binary.

**Tech Stack:** Go 1.26, Bubble Tea 1.3.10, Bubbles 1.0.0, Lip Gloss 1.1.0, standard `testing` package.

## Global Constraints

- Preserve the `agent`, `antigravity`, `codex`, and `claude` entries and their original base/yolo arguments.
- Keep `agent` selected by default and yolo enabled by default.
- Do not copy the standalone `go.mod`, `go.sum`, or compiled binary.
- Do not call `zellij` directly from the selector.
- Expose the feature as `zellij-agent list-selector`.
- Use package directory/name `listselector`, without a hyphen.
- Use Korean commit messages with concise conventional prefixes.

---

### Task 1: Selector model and command construction

**Files:**
- Create: `internal/listselector/model_test.go`
- Create: `internal/listselector/model.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `func NewModel() Model`
- Produces: `func ResultError(tea.Model) error`
- Produces: `type Model` implementing `tea.Model`
- Uses: `tea.ExecProcess` to hand terminal control to the selected child command.

- [ ] **Step 1: Add the Bubbles module dependency**

Run: `go get github.com/charmbracelet/bubbles@v1.0.0`

Expected: `go.mod` directly requires Bubbles 1.0.0 and existing Bubble Tea/Lip Gloss versions remain unchanged.

- [ ] **Step 2: Write failing model tests**

Create `internal/listselector/model_test.go` with table-driven tests for these exact cases:

```go
{"agent", true, "", "zellij-agent", []string{"agent", "start", "agent"}}
{"antigravity", true, "fix it", "zellij-agent", []string{"agent", "start", "agy", "--dangerously-skip-permissions", "fix it"}}
{"codex", true, "", "zellij-agent", []string{"agent", "start", "codex", "--dangerously-bypass-approvals-and-sandbox"}}
{"claude", false, "review", "claude", []string{"review"}}
```

Also test that `opencode` and `pi` are absent; `NewModel` selects index 0, agent focus, and yolo=true; up/down/space update selection and focus; and `ResultError` returns a stored child error.

- [ ] **Step 3: Run the model tests and verify RED**

Run: `go test ./internal/listselector -count=1`

Expected: FAIL because production symbols do not exist.

- [ ] **Step 4: Implement the selector model**

Port the standalone model to package `listselector`. Keep the four exact agent definitions, focus/key behavior, prompt settings, rendering, `exec.Command` stream attachment, and `tea.ExecProcess`. Export only:

```go
func NewModel() Model
func ResultError(final tea.Model) error
```

Remove `expandHome` and `renameZellijPane`; no configured command needs home expansion and selector code must not call Zellij directly.

- [ ] **Step 5: Format and verify GREEN**

Run:

```bash
gofmt -w internal/listselector/model.go internal/listselector/model_test.go
go test ./internal/listselector -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the model**

```bash
git add go.mod go.sum internal/listselector/model.go internal/listselector/model_test.go
git commit -m "feat: 에이전트 리스트 선택기 모델 추가"
```

### Task 2: CLI adapter and exit-code behavior

**Files:**
- Create: `internal/cli/listselector/listselector_test.go`
- Create: `internal/cli/listselector/listselector.go`

**Interfaces:**
- Consumes: `listselector.NewModel()` and `listselector.ResultError(tea.Model)`.
- Produces: `func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg Config) int`
- Produces: `type ProgramRunner func(tea.Model, io.Reader, io.Writer) (tea.Model, error)`
- Produces: `type Config struct { NewModel func() tea.Model; RunProgram ProgramRunner }`

- [ ] **Step 1: Write failing CLI adapter tests**

Test `--help` returns 0 and does not invoke the program; positional input returns 2 with an error; injected runner failure returns 1 with stderr; and success forwards the exact stdin/stdout objects and returns 0.

- [ ] **Step 2: Run the CLI tests and verify RED**

Run: `go test ./internal/cli/listselector -count=1`

Expected: FAIL because the adapter does not exist.

- [ ] **Step 3: Implement the CLI adapter**

Implement help and zero-positional-argument validation. Resolve default factories from `Config`, run:

```go
tea.NewProgram(model, tea.WithInput(stdin), tea.WithOutput(stdout)).Run()
```

Do not enable alternate-screen mode. Print Bubble Tea errors once. Inspect `listselector.ResultError`; return `(*exec.ExitError).ExitCode()` for child exit failures and 1 for other errors.

- [ ] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w internal/cli/listselector/listselector.go internal/cli/listselector/listselector_test.go
go test ./internal/cli/listselector -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the CLI adapter**

```bash
git add internal/cli/listselector/listselector.go internal/cli/listselector/listselector_test.go
git commit -m "feat: 리스트 선택기 CLI 실행기 추가"
```

### Task 3: Unified command dispatch

**Files:**
- Modify: `cmd/zellij-agent/main_test.go`
- Modify: `cmd/zellij-agent/main.go`

**Interfaces:**
- Consumes: `listselectorcli.Run(args, stdin, stdout, stderr, cfg)`.
- Produces: `zellij-agent list-selector` dispatch and help entry.

- [ ] **Step 1: Write failing dispatch and usage tests**

Add `TestRunDispatchesListSelector`, using an injectable runner to assert empty child args, exact stream forwarding, and stub exit-code propagation. Add `TestUsageIncludesListSelector` asserting help contains `list-selector` and its description.

- [ ] **Step 2: Run the command tests and verify RED**

Run: `go test ./cmd/zellij-agent -run 'Test(RunDispatchesListSelector|UsageIncludesListSelector)' -count=1`

Expected: FAIL because dispatch and help are absent.

- [ ] **Step 3: Add dispatch and usage**

Import `internal/cli/listselector`, define an injectable package variable for its `Run`, dispatch `case "list-selector"`, and add:

```text
  list-selector
           Select and start a coding agent in the current terminal
```

Keep the command package as composition only.

- [ ] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
go test ./cmd/zellij-agent ./internal/cli/listselector ./internal/listselector -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit unified dispatch**

```bash
git add cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
git commit -m "feat: 통합 CLI에 리스트 선택기 연결"
```

### Task 4: Repository verification and binary registration

**Files:**
- Verify only; no source changes expected.
- Build: `bin/zellij-agent`
- Register: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: all implementation from Tasks 1-3.
- Produces: a tested unified binary on the configured custom CLI path.

- [ ] **Step 1: Check formatting and module consistency**

Run:

```bash
gofmt -w internal/listselector internal/cli/listselector cmd/zellij-agent
go mod tidy
git diff --check
git status --short
```

Expected: no formatting errors and only intended changes, if any.

- [ ] **Step 2: Run focused and full tests**

Run:

```bash
go test ./internal/listselector ./internal/cli/listselector ./cmd/zellij-agent -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Build and register atomically**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
```

Expected: build succeeds and the custom CLI executable is replaced atomically.

- [ ] **Step 4: Smoke-check and inspect final state**

Run:

```bash
./bin/zellij-agent list-selector --help
git status --short
git log -5 --oneline
```

Expected: selector usage is printed and the worktree is clean.

