# Ticket Worker List Prompt Column Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Append each ticket's fully escaped coding-agent prompt as the fifth field in plain-text `ticket-worker list` output.

**Architecture:** Keep the store and JSON contracts unchanged. Add one CLI-local field escaper and apply it only when `reportTickets` renders non-JSON rows, preserving the existing one-ticket-per-line tab-separated format.

**Tech Stack:** Go standard library (`strings`, `testing`), existing ticket-worker CLI harness, Go toolchain.

## Global Constraints

- Plain-text list rows contain `ID`, `Status`, `Title`, `PlanPath`, and `Prompt`, in that order, separated by tabs.
- Escape backslash, newline, tab, and carriage return as `\\`, `\n`, `\t`, and `\r` inside the prompt field.
- Preserve all other characters, including Unicode.
- Do not change JSON list output, detailed single-ticket output, filtering, ordering, empty-list output, storage, transport, runtime, or Zellij behavior.
- Follow TDD: observe the focused regression test fail before editing production code.

---

### Task 1: Render the Escaped Prompt Column

**Files:**
- Modify: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `internal/cli/ticketworker/ticketworker.go:372-390`

**Interfaces:**
- Consumes: `ticketworker.Ticket.Prompt string` and the existing `reportTickets(stdout, stderr io.Writer, jsonOutput bool, values []ticketworker.Ticket) int` renderer.
- Produces: `escapeListField(value string) string`, used only by plain-text list rendering.

- [ ] **Step 1: Write the failing plain-text list test**

Add this focused test near `TestHumanOutputContract`:

```go
func TestHumanListIncludesEscapedPromptColumn(t *testing.T) {
	h := newHarness(t)
	spec, plan := h.artifacts(t, "escaped-list")
	prompt := "첫째\\literal\n둘째\t셋째\r끝"
	if got := h.run(t, "add", "--title", "Prompt title", "--summary", "Prompt summary", "--spec", spec, "--plan", plan, "--prompt", prompt, "--json"); got != ExitOK {
		t.Fatalf("add exit = %d, stderr = %s", got, h.stderr.String())
	}

	if got := h.run(t, "list"); got != ExitOK {
		t.Fatalf("list exit = %d, stderr = %s", got, h.stderr.String())
	}
	if got, want := h.stdout.String(), "1\tready\tPrompt title\tdocs/superpowers/plans/escaped-list.md\t첫째\\\\literal\\n둘째\\t셋째\\r끝\n"; got != want {
		t.Fatalf("list output = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test ./internal/cli/ticketworker -run '^TestHumanListIncludesEscapedPromptColumn$' -count=1
```

Expected: `FAIL`; the actual row ends after `docs/superpowers/plans/escaped-list.md` because the prompt column is missing.

- [ ] **Step 3: Implement the minimal field escaping and output change**

Add the helper before `reportTickets`:

```go
func escapeListField(value string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	).Replace(value)
}
```

Change the plain-text row write in `reportTickets` to:

```go
if _, err := fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\n", value.ID, value.Status, value.Title, value.PlanPath, escapeListField(value.Prompt)); err != nil {
	return reportError(stderr, false, fmt.Errorf("write output: %w", err))
}
```

- [ ] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go
go test ./internal/cli/ticketworker -run '^TestHumanListIncludesEscapedPromptColumn$' -count=1
go test ./internal/cli/ticketworker -count=1
```

Expected: all commands succeed and both test commands report `ok`.

- [ ] **Step 5: Run the repository regression suite**

Run:

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Build and atomically register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
cmp -s bin/zellij-agent ~/.config/custom-cli/zellij-agent
```

Expected: every command exits with status 0; `cmp` confirms that the registered binary matches the build artifact.

- [ ] **Step 7: Review the final diff and commit**

Run:

```bash
git diff --check
git diff -- internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go
git add internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go
git commit -m "feat: show prompts in ticket worker list"
```

Expected: `git diff --check` is silent, the diff contains only the prompt-column implementation and regression test, and the commit succeeds.
