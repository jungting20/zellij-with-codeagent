# Coding Agent Role Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `agent-role coding-agent <path>` to launch Codex in the repository containing a target path.

**Architecture:** Add role metadata in `internal/roles`, dispatch in `internal/cli/role`, and a focused implementation package in `cmd/agent-role/codingagent`. Keep validation and command construction testable without running interactive Codex.

**Tech Stack:** Go standard library, existing `agent-role` CLI conventions, `go test`.

---

### Task 1: Role Catalog Metadata

**Files:**
- Modify: `internal/roles/roles.go`
- Modify: `internal/roles/roles_test.go`

- [ ] **Step 1: Write the failing catalog test**

Add `RoleCodingAgent` to the list in `TestAllIncludesRoleDescriptions`, then assert the required path argument:

```go
spec, ok := Lookup(RoleCodingAgent)
if !ok {
	t.Fatalf("Lookup(%q) not found", RoleCodingAgent)
}
if len(spec.Arguments) != 1 || spec.Arguments[0].Name != "path" || !spec.Arguments[0].Required {
	t.Fatalf("Lookup(%q) arguments = %#v, want required path argument", RoleCodingAgent, spec.Arguments)
}
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `go test ./internal/roles`

Expected: FAIL because `RoleCodingAgent` is undefined.

- [ ] **Step 3: Add role metadata**

Add:

```go
RoleCodingAgent = "coding-agent"
```

and a `RoleSpec`:

```go
{
	Name:        RoleCodingAgent,
	Usage:       "coding-agent <path>",
	Description: "Runs Codex coding agent in the repository containing the target path.",
	Arguments: []ArgumentSpec{
		{Name: "path", Required: true, Description: "File or directory path inside the repository where Codex should run."},
	},
}
```

- [ ] **Step 4: Run the focused test to verify it passes**

Run: `go test ./internal/roles`

Expected: PASS.

### Task 2: Coding Agent Role Package

**Files:**
- Create: `cmd/agent-role/codingagent/codingagent.go`
- Create: `cmd/agent-role/codingagent/codingagent_test.go`

- [ ] **Step 1: Write validation and command-construction tests**

Create tests that:

```go
func TestPrepareRejectsMissingPath(t *testing.T)
func TestPrepareRejectsPathOutsideRepository(t *testing.T)
func TestPrepareRejectsDirectoryWithoutGit(t *testing.T)
func TestPrepareBuildsCodexCommandInRepository(t *testing.T)
func TestPrepareBuildsCodexCommandFromFileInsideRepository(t *testing.T)
```

The success test creates a temporary repository directory with `.git`, adds a fake executable `codex` to a temporary `PATH`, calls `prepare([]string{repo})`, and asserts `cmd.Path` is the fake codex path and `cmd.Dir` is the repo.

- [ ] **Step 2: Run the focused package test to verify it fails**

Run: `go test ./cmd/agent-role/codingagent`

Expected: FAIL because the package implementation does not exist.

- [ ] **Step 3: Implement the package**

Implement:

```go
func Run(args []string) int
func prepare(args []string) (*exec.Cmd, error)
```

`prepare` validates one path argument, resolves it to an absolute path, requires an existing file or directory inside a git repository, resolves `codex` with `exec.LookPath`, and returns `exec.Command(codexPath)` with `Dir` set to the discovered repository root. `Run` wires stdin, stdout, stderr, runs the command, and returns process exit status.

- [ ] **Step 4: Run package tests**

Run: `go test ./cmd/agent-role/codingagent`

Expected: PASS.

### Task 3: CLI Dispatch

**Files:**
- Modify: `internal/cli/role/role.go`

- [ ] **Step 1: Add dispatch**

Import `cmd/agent-role/codingagent` and add a switch case:

```go
case roles.RoleCodingAgent:
	return codingagent.Run(args[1:])
```

- [ ] **Step 2: Run CLI and role tests**

Run: `go test ./internal/cli/role ./internal/roles ./cmd/agent-role/codingagent`

Expected: PASS.

### Task 4: Full Verification

**Files:**
- Verify all changed Go packages.

- [ ] **Step 1: Run all tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Build agent-role**

Run: `go build -o bin/agent-role ./cmd/agent-role`

Expected: PASS and `bin/agent-role` exists.
