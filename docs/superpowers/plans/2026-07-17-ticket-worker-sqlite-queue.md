# Ticket Worker SQLite Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the `ra-ticket` SQLite queue into `zellij-agent ticket-worker` with explicit, idempotent per-project initialization.

**Architecture:** `internal/ticketworker` owns repository discovery, initialization, the ticket model, and SQLite persistence. `internal/cli/ticketworker` exposes the imported commands inside the unified binary; only `init` creates `.zellij-agent/ticket-worker/tickets.db`, while all other commands require it to exist.

**Tech Stack:** Go 1.26, `database/sql`, `modernc.org/sqlite` v1.53.0, Go standard `flag` and `testing` packages.

## Global Constraints

- Preserve the source ticket model, schema version 1, FIFO claim transaction, state transitions, JSON keys, human output, and exit codes `0` through `7`.
- Store the database at `<project-root>/.zellij-agent/ticket-worker/tickets.db`.
- Only `ticket-worker init` may create the database or its parent directory.
- `init` must preserve existing data and add `.zellij-agent/ticket-worker/` to `.gitignore` exactly once.
- Commands run from nested directories must use the nearest ancestor containing `.git`.
- This phase must not add a pane pool, manager, completion watcher, or coding-agent launcher.

---

### Task 1: Port repository discovery and explicit initialization

**Files:**
- Create: `internal/ticketworker/repository.go`
- Create: `internal/ticketworker/repository_test.go`
- Create: `internal/ticketworker/model.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `FindRoot(start string) (string, error)`, `DatabasePath(root string) string`, `InitializeProject(ctx context.Context, root string, now func() time.Time) error`, `OpenExisting(ctx context.Context, root string, now func() time.Time) (*Store, error)`.
- Produces: `Ticket`, `CreateInput`, `Status`, `Action`, and imported domain errors.

- [ ] **Step 1: Port repository and model tests, then add initialization tests**

Copy the `ra-ticket` root-discovery tests and assert:

```go
if got, want := DatabasePath(root), filepath.Join(root, ".zellij-agent", "ticket-worker", "tickets.db"); got != want {
    t.Fatalf("DatabasePath() = %q, want %q", got, want)
}
```

Add tests that call `InitializeProject` twice, verify `PRAGMA user_version = 1`, preserve a row inserted between calls, and verify `.gitignore` contains exactly one line equal to `.zellij-agent/ticket-worker/`. Add a test where `OpenExisting` returns `ErrNotInitialized` and leaves the DB path absent.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/ticketworker -run 'Test(FindRoot|DatabasePath|InitializeProject|OpenExisting)' -count=1`

Expected: FAIL because the package and functions do not exist.

- [ ] **Step 3: Port the model and implement repository initialization**

Port `internal/ticket/model.go` unchanged except for package name. Port root discovery, change `DatabasePath` to the new path, define `ErrNotInitialized`, and implement `.gitignore` insertion using line-exact matching. `InitializeProject` creates the parent directory, opens/initializes the Store, closes it, then updates `.gitignore`. `OpenExisting` performs `os.Stat` before opening so SQLite cannot create a missing DB.

- [ ] **Step 4: Add the SQLite dependency and run focused tests**

Run: `go get modernc.org/sqlite@v1.53.0`

Run: `gofmt -w internal/ticketworker && go test ./internal/ticketworker -run 'Test(FindRoot|DatabasePath|InitializeProject|OpenExisting)' -count=1`

Expected: PASS.

### Task 2: Port the SQLite Store

**Files:**
- Create: `internal/ticketworker/store.go`
- Create: `internal/ticketworker/store_test.go`

**Interfaces:**
- Consumes: model types from Task 1 and `openStore(ctx, root, databasePath, now) (*Store, error)` used by initialization/opening wrappers.
- Produces: `(*Store).Add`, `Get`, `List`, `Next`, `Transition`, and `Close` with the original signatures.

- [ ] **Step 1: Port all Store tests from `ra-ticket`**

Copy `/Users/hwangjungho/myprojuct/romance-agent/tools/ra-ticket/internal/ticket/store_test.go`, change the package to `ticketworker`, and change test helpers to initialize/open the new database path. Preserve schema, artifacts, FIFO, concurrent claim, transition, and timestamp assertions.

- [ ] **Step 2: Run Store tests and verify failure**

Run: `go test ./internal/ticketworker -run 'Test(Open|Add|Get|List|Next|Transition|Concurrent)' -count=1`

Expected: FAIL because Store methods are absent.

- [ ] **Step 3: Port Store implementation**

Copy the source Store implementation, change its package, rename the low-level `Open` function to `openStore`, and leave schema SQL, `busy_timeout = 5000`, `SetMaxOpenConns(1)`, artifact validation, `BEGIN IMMEDIATE`, scan helpers, and transition map semantically unchanged.

- [ ] **Step 4: Format and run Store tests**

Run: `gofmt -w internal/ticketworker && go test ./internal/ticketworker -count=1`

Expected: PASS.

### Task 3: Port and integrate the public CLI

**Files:**
- Modify: `internal/cli/ticketworker/ticketworker.go`
- Modify: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `cmd/zellij-agent/main.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Consumes: `ticketworker.FindRoot`, `InitializeProject`, `OpenExisting`, Store methods, and domain errors.
- Produces: `Run(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies Dependencies) int` with `Dependencies{StartDirectory string, Now func() time.Time}`.

- [ ] **Step 1: Port CLI tests and add init/help integration tests**

Copy the `ra-ticket` CLI tests, update imports and DB setup to call `ticket-worker init`, and preserve all add/list/next/show/transition/output/error assertions. Add tests that help lists all nine commands, `init` is idempotent, and a pre-init `list` returns validation exit code without creating a DB.

Update the unified CLI test to create a temporary `.git` project and exercise `ticket-worker init`; assert the DB and `.gitignore` entry exist.

- [ ] **Step 2: Run focused CLI tests and verify failure**

Run: `go test ./internal/cli/ticketworker ./cmd/zellij-agent -run 'Test.*TicketWorker|Test(Add|Next|EndToEnd|Lifecycle|Usage|NotFound|Human|Output)' -count=1`

Expected: FAIL because the placeholder does not dispatch queue commands.

- [ ] **Step 3: Port CLI implementation and wire main**

Port the source argument parsing, command handlers, output helpers, error classification, and exit constants. Add `init` and help dispatch before `OpenExisting`; all ticket commands find the Git root and open an existing DB. Map `ErrNotInitialized` to validation and print its required message.

Change top-level dispatch to call:

```go
ticketworkercli.Run(context.Background(), args[1:], stdout, stderr, ticketworkercli.Dependencies{
    StartDirectory: workingDirectory(),
    Now: time.Now,
})
```

Use an injectable working-directory helper if necessary for deterministic tests. Update top-level help to describe a project-local SQLite ticket queue.

- [ ] **Step 4: Format and run focused CLI tests**

Run: `gofmt -w internal/cli/ticketworker cmd/zellij-agent && go test ./internal/cli/ticketworker ./cmd/zellij-agent -count=1`

Expected: PASS.

### Task 4: Document, verify, build, smoke-test, and install

**Files:**
- Modify: `README.md`
- Build: `bin/zellij-agent`
- Install: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: the complete queue CLI.
- Produces: documented behavior and an installed unified binary.

- [ ] **Step 1: Document the ticket queue**

Add a README section covering `init`, all queue commands, the DB path, idempotent `.gitignore` behavior, artifact requirements, JSON mode, and the explicit non-goal that no worker pool starts in this phase.

- [ ] **Step 2: Run full repository verification**

Run: `gofmt -w internal/ticketworker internal/cli/ticketworker cmd/zellij-agent && git diff --check && go test ./...`

Expected: PASS with no diff-check output.

- [ ] **Step 3: Build and run an isolated lifecycle smoke test**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent`.

Create a temporary Git project with approved spec/plan files. Run `init`, `add --json`, `list --json`, `next --json`, and `done 1 --json`. Verify the DB is under `.zellij-agent/ticket-worker/`, `.gitignore` contains one ignore entry, and final status is `done`.

- [ ] **Step 4: Install atomically and verify the installed command**

Run the repository-mandated `cp`, `chmod 755`, and `mv -f` sequence through `~/.config/custom-cli/.zellij-agent.new`. Run the installed `ticket-worker --help` and verify all commands are listed.

- [ ] **Step 5: Commit and review final status**

Run: `git diff --check && git status --short`.

Commit the implementation and documentation with `feat: add ticket worker sqlite queue`, then verify `git status --short` is empty.
