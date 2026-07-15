# Work Project Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect Go, Node, and Rust projects from root marker files and use the detected profile to select the `work` test pane's default test command and display the detected build command.

**Architecture:** `internal/work` owns a side-effect-free root detector that returns a typed immutable result. The CLI resolves the cwd, calls the detector, and passes the result to the existing plan builder. The builder renders commands and disabled reasons into the existing test and notes panes; neither the detector nor plan builder executes project code.

**Tech Stack:** Go standard library (`os`, `encoding/json`, `filepath`, `testing`), existing work CLI and execution-plan transport types.

## Global Constraints

- Work directly on the existing `main` checkout and do not commit; the user reviews and commits.
- Read only known marker files at the requested cwd root. Do not recursively scan, install dependencies, download modules, or execute project commands.
- Supported profiles are Go, npm, pnpm, Yarn, and Rust.
- A mixed project family or conflicting Node package-manager markers disables feedback instead of guessing.
- A missing Node `test` script disables feedback but may still expose a detected build command.
- Keep planners and clients behind the existing transport/runtime boundary.
- After rebuilding `bin/zellij-agent`, immediately copy it to `~/.config/custom-cli`.

---

### Task 1: Add the root project detector

**Files:**

- Create: `internal/work/project.go`
- Create: `internal/work/project_test.go`

**Interfaces:**

- Produces: `DetectProject(root string) (ProjectDetection, error)`.
- Produces: `ProjectDetection{Profile, Markers, TestCommand, BuildCommand, FeedbackEnabled, DisabledReason}`.
- Consumes only root marker metadata and `package.json` content.

- [ ] **Step 1: Write table-driven failing tests for Go and Rust**

Create temporary roots containing `go.mod`, `go.work`, or `Cargo.toml`. Assert:

```go
ProjectDetection{
    Profile:         ProjectProfileGo,
    Markers:         []string{"go.mod"},
    TestCommand:     []string{"go", "test", "./..."},
    BuildCommand:    []string{"go", "build", "./..."},
    FeedbackEnabled: true,
}
```

and the Rust equivalents `cargo test` / `cargo check`. A root containing both `go.work` and `go.mod` remains one Go family and reports both markers in stable order.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/work -run '^TestDetectProject(Go|Rust)' -count=1
```

Expected: build failure because `DetectProject` and profile types do not exist.

- [ ] **Step 3: Implement marker collection and Go/Rust results**

Define:

```go
type ProjectProfile string

const (
    ProjectProfileUnknown ProjectProfile = "unknown"
    ProjectProfileGo      ProjectProfile = "go"
    ProjectProfileNPM     ProjectProfile = "npm"
    ProjectProfilePNPM    ProjectProfile = "pnpm"
    ProjectProfileYarn    ProjectProfile = "yarn"
    ProjectProfileRust    ProjectProfile = "rust"
)

type ProjectDetection struct {
    Profile         ProjectProfile
    Markers         []string
    TestCommand     []string
    BuildCommand    []string
    FeedbackEnabled bool
    DisabledReason  string
}
```

Use `os.Stat(filepath.Join(root, marker))`; ignore only `os.ErrNotExist`, return other filesystem errors, and sort markers before returning.

- [ ] **Step 4: Verify Go/Rust tests GREEN**

Run the focused command from Step 2. Expected: PASS.

- [ ] **Step 5: Write failing Node profile tests**

Cover:

- `package.json` with `test` and `build`, no alternate lockfile → npm commands.
- `pnpm-lock.yaml` → pnpm commands.
- `yarn.lock` → Yarn commands.
- no `test` script → `FeedbackEnabled=false`, empty test command, actionable reason.
- malformed `package.json` → detected Node marker, feedback disabled, no fatal detector error.
- two different package-manager lock families → feedback disabled.

- [ ] **Step 6: Run Node tests and verify RED**

```bash
go test ./internal/work -run '^TestDetectProjectNode' -count=1
```

Expected: Node cases fail because package parsing and manager selection are missing.

- [ ] **Step 7: Implement Node parsing and command selection**

Decode only:

```go
var manifest struct {
    Scripts map[string]string `json:"scripts"`
}
```

Choose one manager family from npm (`package-lock.json` or `npm-shrinkwrap.json`), pnpm, and Yarn. Build commands exist only when the corresponding script is non-empty. Do not execute any package script.

- [ ] **Step 8: Add and implement unknown/mixed-family tests**

An empty root returns `Profile=unknown`, feedback disabled, and an override-oriented reason. Roots containing markers from more than one family return all markers and a mixed-project reason without choosing commands.

- [ ] **Step 9: Run detector tests GREEN**

```bash
gofmt -w internal/work/project.go internal/work/project_test.go
go test ./internal/work -run '^TestDetectProject' -count=1
```

Expected: PASS.

---

### Task 2: Render detected commands in work panes

**Files:**

- Modify: `internal/work/work.go`
- Modify: `internal/work/work_test.go`

**Interfaces:**

- Consumes: `PlanRequest.Project ProjectDetection`.
- Produces: a test pane that suggests or runs only the resolved `TestCommand`.
- Produces: a notes pane that prints profile, markers, test/build commands, and disabled reason.

- [ ] **Step 1: Write a failing Go-plan test**

Pass a Go detection to `BuildPlan`. Assert `--auto-test=false` prints `Suggested test command: go test ./...`; `--auto-test=true` executes the shell-quoted resolved argv once and reports its exit status.

- [ ] **Step 2: Write failing pnpm and disabled-plan tests**

Assert pnpm renders `pnpm test` / `pnpm build`, while unknown or missing-test results render `Feedback disabled: <reason>` and never execute a hard-coded Go command, including when `AutoTest=true`.

- [ ] **Step 3: Run plan tests and verify RED**

```bash
go test ./internal/work -run '^TestBuildPlan.*(Project|Detected|Disabled|AutoTest)' -count=1
```

Expected: failure because `PlanRequest` does not accept the detection and scripts remain hard-coded.

- [ ] **Step 4: Implement detection-aware pane scripts**

Add `Project ProjectDetection` to `PlanRequest`. Replace `testScript(bool)` with `testScript(bool, ProjectDetection)`. Add a shell-rendering helper that quotes each argv element with the existing `shellQuote` and a display helper that joins arguments for readable preflight output.

Extend `notesScript` to print:

```text
Profile: go
Markers: go.mod
Test command: go test ./...
Build command: go build ./...
Feedback: enabled
```

or the exact disabled reason. Keep all existing useful control commands.

- [ ] **Step 5: Format and verify plan tests GREEN**

```bash
gofmt -w internal/work/work.go internal/work/work_test.go
go test ./internal/work -count=1
```

Expected: PASS.

---

### Task 3: Wire detection into the work CLI

**Files:**

- Modify: `internal/cli/work/work.go`
- Modify: `internal/cli/work/work_test.go`
- Modify: `docs/next-steps-todolist.md`
- Modify: `docs/manual-smoke-test.md`

**Interfaces:**

- Consumes: resolved absolute cwd and `work.DetectProject`.
- Produces: submitted/dry-run payloads with project-aware test and notes panes.
- Preserves: existing CLI flags and transport envelope.

- [ ] **Step 1: Write failing CLI dry-run tests**

Create temporary Go, pnpm, Rust, and empty roots. Invoke `Run --dry-run`, decode the envelope, and assert the selected test pane script and notes pane metadata. A mixed root must still return exit code `0` and create a feedback-disabled workspace.

- [ ] **Step 2: Run CLI tests and verify RED**

```bash
go test ./internal/cli/work -run '^TestRun.*Project' -count=1
```

Expected: failure because the CLI does not invoke the detector.

- [ ] **Step 3: Detect after cwd resolution and pass the result**

Call:

```go
project, err := workplan.DetectProject(cwd)
```

Return exit code `1` only for unexpected filesystem access errors. Pass `Project: project` to `BuildPlan`. Do not execute the selected command in the CLI.

- [ ] **Step 4: Update user documentation**

Check the two Phase A todo items for project detection and default command selection. Add manual dry-run examples for Go, Node/pnpm, Rust, and a mixed root, instructing the user to inspect the test and notes pane commands in JSON.

- [ ] **Step 5: Run focused and full verification**

```bash
gofmt -w internal/work internal/cli/work
go test ./internal/work ./internal/cli/work ./cmd/zellij-agent -count=1
go test ./... -count=1
./scripts/test-race-core.sh
git diff --check
```

Expected: all commands pass.

- [ ] **Step 6: Build and register the unified binary**

```bash
go build -p 1 -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
shasum -a 256 bin/zellij-agent ~/.config/custom-cli/zellij-agent
```

Expected: build/copy succeeds and hashes match. Leave all source and documentation changes uncommitted for user review.
