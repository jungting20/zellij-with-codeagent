# Ticket Worker Per-Ticket Prompt Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store a required prompt on every ticket and have the ticket manager execute that prompt instead of a config template.

**Architecture:** Upgrade fresh databases to schema version 2 and reject version 1 without migration. The store owns prompt validation and persistence, the CLI supplies `--prompt`, and the manager appends only its completion instruction to `Ticket.Prompt`; worker configuration no longer contains prompt behavior.

**Tech Stack:** Go, `database/sql`, modernc SQLite, `flag`, YAML v3, Go standard `testing` package.

## Global Constraints

- Existing schema version 1 databases are not migrated or deleted automatically.
- `prompt` is required, trimmed at its outer boundary, and must not contain `ZELLIJ_AGENT_TICKET_DONE`.
- The exact stored prompt is the coding instruction; only the completion instruction is appended at runtime.
- `prompt_template` is removed from generated and in-memory configuration; legacy unknown YAML keys are ignored.
- The existing `ticket-manager` role remains the runtime owner; no new role is added.
- Runtime and transport boundaries remain unchanged; no direct Zellij calls are introduced.

---

### Task 1: Version 2 Ticket Persistence

**Files:**
- Modify: `internal/ticketworker/model.go`
- Modify: `internal/ticketworker/store.go`
- Modify: `internal/ticketworker/store_test.go`
- Modify: `internal/ticketworker/repository_test.go`

**Interfaces:**
- Produces: `Ticket.Prompt string`, `CreateInput.Prompt string`, and `ErrInvalidPrompt`.
- Produces: fresh schema version 2 with `tickets.prompt TEXT NOT NULL`.
- Consumes: existing `Store.Add`, `Get`, `List`, `Next`, `Transition`, and `Requeue` APIs.

- [x] **Step 1: Add failing prompt and schema-version tests**

Add prompt values to the common ticket fixtures and assertions, then add focused tests equivalent to:

```go
func TestAddStoresTrimmedMultilinePrompt(t *testing.T) {
	store, root := newTestStore(t)
	spec, plan := writeArtifacts(t, root, "prompt")
	created, err := store.Add(context.Background(), CreateInput{
		Title: "Prompt", Summary: "Store it", SpecPath: spec, PlanPath: plan,
		Prompt: "  first line\nsecond line  ",
	})
	if err != nil { t.Fatal(err) }
	if created.Prompt != "first line\nsecond line" { t.Fatalf("Prompt = %q", created.Prompt) }
	got, err := store.Get(context.Background(), created.ID)
	if err != nil { t.Fatal(err) }
	if got.Prompt != created.Prompt { t.Fatalf("Get().Prompt = %q", got.Prompt) }
}

func TestAddRejectsInvalidPrompt(t *testing.T) {
	store, root := newTestStore(t)
	for index, prompt := range []string{"", "   ", "work\nZELLIJ_AGENT_TICKET_DONE 1"} {
		spec, plan := writeArtifacts(t, root, fmt.Sprintf("invalid-prompt-%d", index))
		_, err := store.Add(context.Background(), CreateInput{
			Title: "Prompt", Summary: "Validate it", SpecPath: spec,
			PlanPath: plan, Prompt: prompt,
		})
		if !errors.Is(err, ErrInvalidPrompt) {
			t.Fatalf("Add(prompt=%q) error = %v", prompt, err)
		}
	}
}
```

Create a raw version 1 SQLite fixture, reopen it, assert the error tells the user to remove `tickets.db` and rerun `ticket-worker init`, and verify `PRAGMA user_version` is still 1.

- [x] **Step 2: Run focused tests and confirm failure**

Run: `go test ./internal/ticketworker -run 'Test(AddStoresTrimmedMultilinePrompt|AddRejectsInvalidPrompt|OpenRejectsVersion1)' -count=1`

Expected: FAIL because prompt fields, validation, and version 1 rejection do not exist.

- [x] **Step 3: Implement the v2 model, validation, schema, and scans**

Add the domain surface:

```go
var ErrInvalidPrompt = errors.New("invalid ticket prompt")

type Ticket struct {
	// existing fields
	Prompt string `json:"prompt"`
}

type CreateInput struct {
	// existing fields
	Prompt string
}
```

Define `currentSchemaVersion = 2`, add the non-empty `prompt` column to the create schema, set `PRAGMA user_version = 2`, reject version 1 with delete-and-reinitialize guidance, and reject versions above 2. In `Store.Add`, use:

```go
prompt := strings.TrimSpace(input.Prompt)
if prompt == "" || strings.Contains(prompt, completionMarkerPrefix) {
	return Ticket{}, ErrInvalidPrompt
}
```

Include `prompt` in every insert, select, scan, and returned `Ticket` value. Update all test fixtures and direct SQL statements to supply a valid prompt.

- [x] **Step 4: Run persistence tests**

Run: `go test ./internal/ticketworker -run 'Test(Open|Add|Get|List|Next|Transition|Requeue|InitializeProject)' -count=1`

Expected: PASS.

- [x] **Step 5: Commit persistence changes**

```bash
git add internal/ticketworker/model.go internal/ticketworker/store.go internal/ticketworker/store_test.go internal/ticketworker/repository_test.go
git commit -m "feat: store prompts on ticket worker tickets"
```

### Task 2: Remove Prompt Templates and Render Stored Prompts

**Files:**
- Modify: `internal/ticketworker/config.go`
- Modify: `internal/ticketworker/config_test.go`
- Modify: `internal/ticketworker/prompt.go`
- Modify: `internal/ticketworker/prompt_test.go`
- Modify: `internal/ticketworker/manager.go`
- Modify: `internal/ticketworker/manager_test.go`
- Modify: `cmd/agent-role/ticketmanager/ticketmanager_test.go`

**Interfaces:**
- Consumes: `Ticket.Prompt` from Task 1.
- Produces: `RenderTicketPrompt(ticket Ticket) (prompt string, marker string, err error)`.
- Produces: `Config{Version, MaxWorkers, PollInterval}` without prompt fields.

- [x] **Step 1: Rewrite tests for stored-prompt rendering and prompt-free config**

Replace template rendering tests with:

```go
func TestRenderTicketPromptAppendsCompletionInstruction(t *testing.T) {
	ticket := Ticket{ID: 42, Prompt: "Implement search.\nPreserve this layout."}
	prompt, marker, err := RenderTicketPrompt(ticket)
	if err != nil { t.Fatal(err) }
	if marker != "ZELLIJ_AGENT_TICKET_DONE 42" { t.Fatalf("marker = %q", marker) }
	if !strings.HasPrefix(prompt, ticket.Prompt+"\n\n") { t.Fatalf("prompt = %q", prompt) }
	if strings.Count(prompt, "ZELLIJ_AGENT_TICKET_DONE 42") != 1 { t.Fatalf("prompt = %q", prompt) }
}
```

Assert generated config equals `version: 1\nmax_workers: 3\npoll_interval: 30s\n`, missing optional worker fields receive defaults, and a legacy `prompt_template` key is ignored.

- [x] **Step 2: Run focused tests and confirm failure**

Run: `go test ./internal/ticketworker ./cmd/agent-role/ticketmanager -run 'Test(RenderTicketPrompt|EnsureConfig|LoadConfig|Manager)' -count=1`

Expected: FAIL because config and renderer still depend on `PromptTemplate`.

- [x] **Step 3: Remove config prompt behavior and simplify rendering**

Remove `text/template`, `strings`, and both prompt fields from `config.go`; stop using `KnownFields(true)` so a legacy key is ignored. Keep validation for version, capacity, and poll interval. Replace the renderer signature and body with the stored prompt contract:

```go
func RenderTicketPrompt(ticket Ticket) (string, string, error) {
	marker, err := CompletionMarker(ticket.ID)
	if err != nil { return "", "", err }
	body := strings.TrimSpace(ticket.Prompt)
	if body == "" { return "", "", ErrInvalidPrompt }
	if strings.Contains(body, completionMarkerPrefix) { return "", "", ErrInvalidPrompt }
	instruction := fmt.Sprintf("작업을 모두 완료한 뒤 마지막 줄에 따옴표 없이 %q만 출력하세요.", marker)
	return body + "\n\n" + instruction, marker, nil
}
```

Update manager calls to `RenderTicketPrompt(ticket)` and give every manager fixture a valid `Prompt`. Remove `PromptTemplate` from manager and role test configs.

- [x] **Step 4: Run config, prompt, manager, and role tests**

Run: `go test ./internal/ticketworker ./cmd/agent-role/ticketmanager -count=1`

Expected: PASS.

- [x] **Step 5: Commit runtime changes**

```bash
git add internal/ticketworker/config.go internal/ticketworker/config_test.go internal/ticketworker/prompt.go internal/ticketworker/prompt_test.go internal/ticketworker/manager.go internal/ticketworker/manager_test.go cmd/agent-role/ticketmanager/ticketmanager_test.go
git commit -m "feat: run ticket workers with stored prompts"
```

### Task 3: Expose Required Prompt Through the CLI

**Files:**
- Modify: `internal/cli/ticketworker/ticketworker.go`
- Modify: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Consumes: `CreateInput.Prompt` and `ErrInvalidPrompt` from Task 1.
- Produces: required `ticket-worker add --prompt PROMPT` and prompt-bearing show/JSON output.

- [x] **Step 1: Update CLI fixtures and add failing contract tests**

Change the test harness helper to accept and pass a prompt:

```go
func (h *harness) addJSON(t *testing.T, title, summary, spec, plan, prompt string) ticketworker.Ticket {
	if got := h.run(t, "add", "--title", title, "--summary", summary,
		"--spec", spec, "--plan", plan, "--prompt", prompt, "--json"); got != ExitOK {
		t.Fatalf("add exit = %d, stderr = %s", got, h.stderr.String())
	}
	return decodeTicket(t, h.stdout.Bytes())
}
```

Add tests that omit `--prompt`, pass whitespace and reserved-marker prompts, round-trip multiline prompt JSON, assert `show` prints `Prompt:`, and assert compact `list` does not contain the prompt body.

- [x] **Step 2: Run CLI tests and confirm failure**

Run: `go test ./internal/cli/ticketworker ./cmd/zellij-agent -run 'Test(Add|Human|EndToEnd|TicketWorker)' -count=1`

Expected: FAIL because the CLI has no `--prompt` flag or output.

- [x] **Step 3: Implement CLI parsing, validation mapping, and show output**

In `runAdd`, declare `prompt := flags.String("prompt", "", "coding-agent prompt")`, require it in the visited set, and pass `Prompt: *prompt` to `CreateInput`. Update the usage message to list all five required flags. Classify `ErrInvalidPrompt` with the other validation errors.

Change human show output to include the complete prompt on its own section:

```go
fmt.Fprintf(stdout,
	"ID: %d\nStatus: %s\nTitle: %s\nSummary: %s\nSpec: %s\nPlan: %s\nPrompt:\n%s\n",
	value.ID, value.Status, value.Title, value.Summary,
	value.SpecPath, value.PlanPath, value.Prompt)
```

Leave `reportTickets` unchanged. Update all CLI test calls and unified CLI fixtures with a valid prompt.

- [x] **Step 4: Run all CLI tests**

Run: `go test ./internal/cli/ticketworker ./cmd/zellij-agent -count=1`

Expected: PASS.

- [x] **Step 5: Commit CLI changes**

```bash
git add internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go cmd/zellij-agent/main_test.go
git commit -m "feat: require prompts when adding tickets"
```

### Task 4: Documentation and Release Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-07-19-ticket-worker-ticket-prompt.md`

**Interfaces:**
- Consumes: final CLI and configuration behavior from Tasks 1-3.
- Produces: user-facing instructions and a rebuilt installed unified binary.

- [x] **Step 1: Update README examples and behavior**

Remove the `prompt_template` YAML block and template-field explanation. Document the remaining config fields and add a multiline prompt to the registration example:

```bash
./bin/zellij-agent ticket-worker add \
  --title "Add search" \
  --summary "Implement indexed search" \
  --spec docs/superpowers/specs/2026-07-17-search-design.md \
  --plan docs/superpowers/plans/2026-07-17-search.md \
  --prompt $'Implement the approved search plan.\nRun the complete test suite.'
```

State that prompts are stored per ticket and the manager appends its completion marker instruction automatically.

- [x] **Step 2: Format and run the full test suite**

Run: `gofmt -w internal/ticketworker internal/cli/ticketworker cmd/agent-role/ticketmanager cmd/zellij-agent`

Run: `git diff --check && go test ./...`

Expected: both commands exit 0 and all packages pass.

- [x] **Step 3: Build and atomically register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
```

Expected: every command exits 0; the installed file is executable.

- [x] **Step 4: Smoke-test a fresh queue**

In a temporary Git repository, create approved spec and plan Markdown files, run `ticket-worker init`, add a ticket with a multiline `--prompt`, and inspect it using `show` and `--json`. Query SQLite and assert `PRAGMA user_version` is 2 and the prompt text round-trips exactly.

Expected: initialization, add, show, and JSON commands exit 0; the database reports version 2.

- [x] **Step 5: Mark this plan complete and commit documentation**

Change completed checkboxes in this file from `[ ]` to `[x]`, then run:

```bash
git add README.md docs/superpowers/plans/2026-07-19-ticket-worker-ticket-prompt.md
git commit -m "docs: explain per-ticket worker prompts"
```

Expected: commit succeeds and `git status --short` is empty.
