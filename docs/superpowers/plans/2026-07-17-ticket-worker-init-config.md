# Ticket Worker Init Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `zellij-agent ticket-worker init` create and preserve a strict project-local worker config containing capacity, polling cadence, and a coding-agent prompt template.

**Architecture:** Add a focused config unit under `internal/ticketworker` that owns YAML persistence and validation. The existing project initializer composes database, `.gitignore`, and config initialization; the CLI only reports the resulting artifact paths.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, Go `text/template`, standard `testing` package.

## Global Constraints

- Config path is exactly `.zellij-agent/worker/config.yaml` under the discovered Git root.
- Existing config files are never overwritten; there is no `--force` flag.
- Default values are `version: 1`, `max_workers: 3`, and `poll_interval: 30s`.
- The prompt contract exposes `ID`, `Title`, `Summary`, `SpecPath`, and `PlanPath`.
- Do not restore worker panes, manager behavior, completion commands, or direct Zellij calls.
- Rebuild and atomically register `bin/zellij-agent` after tests pass.

---

### Task 1: Project Config Model and Persistence

**Files:**
- Create: `internal/ticketworker/config.go`
- Create: `internal/ticketworker/config_test.go`

**Interfaces:**
- Produces: `ConfigPath(root string) string`
- Produces: `LoadConfig(root string) (Config, error)`
- Produces: `EnsureConfig(root string) (path string, created bool, err error)`
- Produces: `Config{Version int, MaxWorkers int, PollInterval time.Duration, PromptTemplate string}`

- [ ] **Step 1: Write failing path, defaults, and persistence tests**

Add tests that assert the exact config path, exact generated YAML, successful loading, default application when optional fields are omitted, preservation on a second `EnsureConfig`, and recreation after deletion. The expected generated YAML is:

```go
const want = "version: 1\nmax_workers: 3\npoll_interval: 30s\nprompt_template: |\n  티켓 #{{ .ID }}을 구현해줘.\n\n  제목: {{ .Title }}\n  요약: {{ .Summary }}\n  설계: {{ .SpecPath }}\n  구현 계획: {{ .PlanPath }}\n"
```

The preservation test must replace the generated contents with `"custom prompt\n"`, call `EnsureConfig` again, expect `created == false`, and assert the custom bytes are unchanged.

- [ ] **Step 2: Write failing strict-validation tests**

Use table tests with complete YAML bodies and require `LoadConfig` to reject each of these cases: unsupported/missing version, unknown field, explicit zero/negative `max_workers`, malformed/zero/negative `poll_interval`, empty/whitespace prompt, and malformed template syntax such as `prompt_template: '{{'`. Include a valid template using every documented field.

- [ ] **Step 3: Run config tests and verify failure**

Run: `go test ./internal/ticketworker -run 'Test(Config|LoadConfig|EnsureConfig)' -count=1`

Expected: FAIL because `ConfigPath`, `LoadConfig`, and `EnsureConfig` do not exist.

- [ ] **Step 4: Implement the config model, strict loader, and create-if-missing writer**

Create `config.go` with these concrete elements:

```go
const (
    configVersion       = 1
    defaultMaxWorkers   = 3
    defaultPollInterval = 30 * time.Second
    configTemplate      = "version: 1\nmax_workers: 3\npoll_interval: 30s\nprompt_template: |\n  티켓 #{{ .ID }}을 구현해줘.\n\n  제목: {{ .Title }}\n  요약: {{ .Summary }}\n  설계: {{ .SpecPath }}\n  구현 계획: {{ .PlanPath }}\n"
)

type Config struct {
    Version        int
    MaxWorkers     int
    PollInterval   time.Duration
    PromptTemplate string
}

type diskConfig struct {
    Version        int    `yaml:"version"`
    MaxWorkers     *int   `yaml:"max_workers"`
    PollInterval   string `yaml:"poll_interval"`
    PromptTemplate string `yaml:"prompt_template"`
}
```

`LoadConfig` must use `yaml.Decoder.KnownFields(true)`, apply defaults only when optional fields are omitted, parse durations with `time.ParseDuration`, trim only for validation, and parse the original prompt string with `template.New("ticket-prompt").Option("missingkey=error").Parse(...)`.

`EnsureConfig` must create the parent with mode `0755`, use `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)`, treat `fs.ErrExist` as success with `created == false`, and remove a partially written file after write or close failure.

- [ ] **Step 5: Format and run focused tests**

Run: `gofmt -w internal/ticketworker/config.go internal/ticketworker/config_test.go && go test ./internal/ticketworker -run 'Test(Config|LoadConfig|EnsureConfig)' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the config unit**

```bash
git add internal/ticketworker/config.go internal/ticketworker/config_test.go
git commit -m "feat: add ticket worker prompt config"
```

---

### Task 2: Wire Config Creation into Init

**Files:**
- Modify: `internal/ticketworker/repository.go`
- Modify: `internal/ticketworker/repository_test.go`
- Modify: `internal/cli/ticketworker/ticketworker.go`
- Modify: `internal/cli/ticketworker/ticketworker_test.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Consumes: `EnsureConfig(root string) (string, bool, error)` and `ConfigPath(root string) string`
- Preserves: `InitializeProject(ctx context.Context, root string, now func() time.Time) error`

- [ ] **Step 1: Extend failing package initialization tests**

Update `TestInitializeProjectIsIdempotentAndUpdatesGitignoreOnce` to assert that config exists and loads after init. Before the second init, overwrite the prompt config with a valid custom YAML and assert it remains byte-for-byte unchanged afterward.

Add `TestInitializeProjectConfigFailurePreservesDatabase`: precreate `.zellij-agent/worker` as a regular file, call `InitializeProject`, require a non-nil config initialization error, and assert both `tickets.db` and the `.gitignore` entry still exist.

- [ ] **Step 2: Extend failing CLI and unified dispatch tests**

Update CLI init coverage to require stdout lines with both:

```text
initialized ticket-worker database: <database-path>
initialized ticket-worker config: <config-path>
```

Update `TestRunDispatchesTicketWorkerInit` to stat `ticketworker.ConfigPath(root)`, call `ticketworker.LoadConfig(root)`, and require the default values. Add a repeated-init assertion that writes a valid custom prompt config and confirms it is preserved.

- [ ] **Step 3: Run focused tests and verify failure**

Run: `go test ./internal/ticketworker ./internal/cli/ticketworker ./cmd/zellij-agent -run 'Test.*Init|TestInitializeProject' -count=1`

Expected: FAIL because project initialization does not create config and CLI output omits its path.

- [ ] **Step 4: Compose config initialization and update output**

After `ensureGitignore(root)` succeeds in `InitializeProject`, call:

```go
if _, _, err := EnsureConfig(root); err != nil {
    return fmt.Errorf("initialize ticket-worker config: %w", err)
}
return nil
```

After successful `InitializeProject`, make `run init` print:

```go
fmt.Fprintf(stdout, "initialized ticket-worker database: %s\n", ticketworker.DatabasePath(root))
fmt.Fprintf(stdout, "initialized ticket-worker config: %s\n", ticketworker.ConfigPath(root))
```

- [ ] **Step 5: Format and run affected tests**

Run: `gofmt -w internal/ticketworker/repository.go internal/ticketworker/repository_test.go internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go cmd/zellij-agent/main_test.go && go test ./internal/ticketworker ./internal/cli/ticketworker ./cmd/zellij-agent -count=1`

Expected: PASS.

- [ ] **Step 6: Commit init integration**

```bash
git add internal/ticketworker/repository.go internal/ticketworker/repository_test.go internal/cli/ticketworker/ticketworker.go internal/cli/ticketworker/ticketworker_test.go cmd/zellij-agent/main_test.go
git commit -m "feat: create ticket worker config on init"
```

---

### Task 3: Documentation and Final Verification

**Files:**
- Modify: `README.md`
- Verify: all Go packages
- Build: `bin/zellij-agent`
- Register: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: the final init behavior and config schema from Tasks 1 and 2.
- Produces: user documentation and a registered unified binary.

- [ ] **Step 1: Update README ticket-worker initialization documentation**

Document that init creates `.zellij-agent/worker/config.yaml`, preserves edits on repeated runs, and regenerates defaults only after deletion. Include the default YAML and list the five prompt template fields: `ID`, `Title`, `Summary`, `SpecPath`, and `PlanPath`.

- [ ] **Step 2: Run documentation and repository checks**

Run: `rg -n 'prompt_template|max_workers|poll_interval|SpecPath|PlanPath' README.md internal/ticketworker && git diff --check`

Expected: all config terms appear in the intended docs/tests/code and `git diff --check` reports no errors.

- [ ] **Step 3: Run the complete test suite**

Run: `go test ./...`

Expected: PASS for every package.

- [ ] **Step 4: Build and atomically register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
cmp bin/zellij-agent ~/.config/custom-cli/zellij-agent
```

Expected: every command exits `0`; `cmp` produces no output.

- [ ] **Step 5: Smoke init in a temporary Git project**

Create a temporary directory with a `.git` directory, run the registered `zellij-agent ticket-worker init` from it, and verify the database, `.gitignore` entry, config path, and `prompt_template` text. Run init a second time after changing the prompt and confirm the custom prompt remains unchanged.

- [ ] **Step 6: Commit documentation**

```bash
git add README.md
git commit -m "docs: document ticket worker prompt config"
```
