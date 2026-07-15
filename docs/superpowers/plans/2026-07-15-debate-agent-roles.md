# Debate Agent Roles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add independently executable proposer, critic, and judge roles backed by `agy`, Cursor `agent`, and `codex`, with fixed system prompts and a shared `debate-role/v1` output envelope.

**Architecture:** A new `internal/debaterole` package owns argument parsing, repository resolution, prompt composition, exit handling, and rendering. Three packages under `cmd/agent-role/` embed the approved prompts and adapt provider-specific non-interactive output into normalized response text. Existing role metadata and dispatch expose the packages through both role entrypoints without changing planner or debate behavior.

**Tech Stack:** Go 1.26; standard-library `flag`, `encoding/json`, `embed`, `os/exec`, and `testing`.

## Global Constraints

- Role names are exactly `debate-proposer`, `debate-critic`, and `debate-judge`.
- System prompts are stored verbatim in separate embedded text files.
- Provider commands are non-interactive and read-only: `agy --mode plan --print`, Cursor `agent --print --mode ask`, and `codex exec --sandbox read-only --ask-for-approval never`.
- Prompt input comes from positional arguments after `<path>`, or stdin when no prompt argument is present.
- `--output-format` accepts only `text` and `json`; `text` is the default.
- JSON success output has exactly `schema_version`, `role`, `engine`, `status`, and `content`, with version `debate-role/v1`.
- Failures write to stderr, return non-zero, and never emit a success-shaped JSON object.
- Existing planner and `debate-background` behavior are out of scope.
- After rebuilding `bin/zellij-agent`, immediately copy it to `~/.config/custom-cli`.

---

### Task 1: Shared debate-role command contract

**Files:**
- Create: `internal/debaterole/role.go`
- Create: `internal/debaterole/role_test.go`

**Interfaces:**
- Produces: `Provider.Run(context.Context, ProviderRequest) (string, error)`
- Produces: `ProviderFunc`, `ProviderRequest`, `Config`, `Run`, and `ComposePrompt`
- Consumed by: all three provider roles

- [ ] **Step 1: Write failing tests for positional input, stdin fallback, and exact output**

Create tests using a recording `ProviderFunc` and a temporary directory containing `.git`. The test table must cover:

```go
tests := []struct {
	name         string
	args         []string
	stdin        string
	format       string
	wantInput    string
	wantOutput   string
}{
	{
		name:       "positional text",
		args:       []string{repo, "analyze", "this"},
		wantInput:  "analyze this",
		wantOutput: "answer\n",
	},
	{
		name:       "stdin json",
		args:       []string{"--output-format", "json", repo},
		stdin:      "proposal from stdin\n",
		wantInput:  "proposal from stdin",
		wantOutput: "{\"schema_version\":\"debate-role/v1\",\"role\":\"debate-proposer\",\"engine\":\"agy\",\"status\":\"success\",\"content\":\"answer\"}\n",
	},
}
```

For every case assert that the provider receives the resolved repository root and a prompt containing exactly these section markers:

```text
<<<SYSTEM_ROLE_BEGIN>>>
SYSTEM
<<<SYSTEM_ROLE_END>>>

<<<DEBATE_INPUT_BEGIN>>>
<input>
<<<DEBATE_INPUT_END>>>
```

Add validation tests for missing path, a path outside Git, invalid `--output-format yaml`, empty positional/stdin prompt, nil provider, and empty provider response. Add an exit-code test that runs a temporary shell executable ending in `exit 7`, wraps its `*exec.ExitError` from the provider, and expects `Run(...) == 7`.

- [ ] **Step 2: Run the tests and verify RED**

```bash
go test ./internal/debaterole -v
```

Expected: package build failure because the shared types and functions do not exist.

- [ ] **Step 3: Implement the minimal shared runner**

Create `role.go` with these exact public types:

```go
const SchemaVersion = "debate-role/v1"

type ProviderRequest struct {
	Repository string
	Prompt     string
}

type Provider interface {
	Run(context.Context, ProviderRequest) (string, error)
}

type ProviderFunc func(context.Context, ProviderRequest) (string, error)

func (fn ProviderFunc) Run(ctx context.Context, req ProviderRequest) (string, error) {
	return fn(ctx, req)
}

type Config struct {
	Role         string
	Engine       string
	SystemPrompt string
	Provider     Provider
}
```

Implement `Run` with a `flag.FlagSet`, validate `text|json`, require a path, resolve the Git root, join remaining positional arguments, fall back to `io.ReadAll(stdin)`, and reject an empty input before calling the provider. Normalize only trailing CR/LF from the provider response.

The JSON renderer must encode this struct with `json.NewEncoder(stdout)`:

```go
type Result struct {
	SchemaVersion string `json:"schema_version"`
	Role          string `json:"role"`
	Engine        string `json:"engine"`
	Status        string `json:"status"`
	Content       string `json:"content"`
}
```

Implement prompt composition exactly as:

```go
func ComposePrompt(systemPrompt, input string) string {
	return "<<<SYSTEM_ROLE_BEGIN>>>\n" + strings.TrimSpace(systemPrompt) +
		"\n<<<SYSTEM_ROLE_END>>>\n\n<<<DEBATE_INPUT_BEGIN>>>\n" + strings.TrimSpace(input) +
		"\n<<<DEBATE_INPUT_END>>>\n"
}
```

Implement repository resolution using `filepath.Abs`, `os.Stat`, and upward `.git` discovery, matching the behavior of the existing `coding-agent` role. Implement exit preservation with `errors.As(err, *exec.ExitError)`; all non-process errors return `1), while flag/usage validation returns `2`.

- [ ] **Step 4: Run shared tests and verify GREEN**

```bash
gofmt -w internal/debaterole/role.go internal/debaterole/role_test.go
go test ./internal/debaterole -v
```

Expected: all shared runner tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/debaterole
git commit -m "feat: add shared debate role runner"
```

---

### Task 2: `agy` proposer role

**Files:**
- Create: `cmd/agent-role/debateproposer/debateproposer.go`
- Create: `cmd/agent-role/debateproposer/debateproposer_test.go`
- Create: `cmd/agent-role/debateproposer/system_prompt.txt`

**Interfaces:**
- Consumes: `debaterole.Config`, `ProviderRequest`, and `Run`
- Produces: `func Run(args []string) int`
- Runs: `agy --mode plan --print <composed-prompt>`

- [ ] **Step 1: Write failing provider tests**

Use a fake `agy` on a temporary `PATH`. The script writes `$PWD` and each argument to paths supplied by `TEST_CWD_FILE` and `TEST_ARGS_FILE`, then prints `proposer answer`. Call package-local `runWithIO` with `--output-format json`, a temporary Git repository, and `test problem`.

Assert:

```go
wantArgs := []string{"--mode", "plan", "--print", expectedComposedPrompt}
wantJSON := map[string]any{
	"schema_version": "debate-role/v1",
	"role": "debate-proposer",
	"engine": "agy",
	"status": "success",
	"content": "proposer answer",
}
```

Add cases for missing `agy` and a fake executable that writes `provider diagnostic` to stderr and exits `9`. The latter must return `9`, print no stdout, and include both `agy failed` and the diagnostic on stderr.

- [ ] **Step 2: Run proposer tests and verify RED**

```bash
go test ./cmd/agent-role/debateproposer -v
```

Expected: package missing/build failure.

- [ ] **Step 3: Add the proposer prompt verbatim**

Create `system_prompt.txt` with the approved text from the design document, starting `당신은 토론의 제안자이자 탐색자다.` and ending `* 불확실한 부분`. Compare it byte-for-byte with the design before proceeding.

- [ ] **Step 4: Implement the proposer adapter**

Embed the prompt and implement this provider core:

```go
//go:embed system_prompt.txt
var systemPrompt string

type agyProvider struct{}

func (agyProvider) Run(ctx context.Context, req debaterole.ProviderRequest) (string, error) {
	path, err := exec.LookPath("agy")
	if err != nil {
		return "", fmt.Errorf("agy executable not found on PATH")
	}
	cmd := exec.CommandContext(ctx, path, "--mode", "plan", "--print", req.Prompt)
	cmd.Dir = req.Repository
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("agy failed: %w%s", err, diagnostic(stderr.String()))
	}
	return stdout.String(), nil
}
```

Expose:

```go
func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return debaterole.Run(args, stdin, stdout, stderr, debaterole.Config{
		Role: "debate-proposer", Engine: "agy", SystemPrompt: systemPrompt, Provider: agyProvider{},
	})
}
```

The local `diagnostic` helper returns an empty string for blank stderr and otherwise `": " + strings.TrimSpace(stderr)`.

- [ ] **Step 5: Verify GREEN and commit**

```bash
gofmt -w cmd/agent-role/debateproposer/*.go
go test ./cmd/agent-role/debateproposer -v
git add cmd/agent-role/debateproposer
git commit -m "feat: add debate proposer role"
```

Expected: all proposer tests pass before the commit.

---

### Task 3: Cursor critic role

**Files:**
- Create: `cmd/agent-role/debatecritic/debatecritic.go`
- Create: `cmd/agent-role/debatecritic/debatecritic_test.go`
- Create: `cmd/agent-role/debatecritic/system_prompt.txt`

**Interfaces:**
- Produces: `func Run(args []string) int`
- Runs: `agent --print --mode ask --output-format json --trust <composed-prompt>`
- Extracts: the native JSON `result` string

- [ ] **Step 1: Write failing critic tests**

Use a fake `agent` that records args/cwd and prints:

```json
{"type":"result","subtype":"success","is_error":false,"result":"critic answer"}
```

Assert text stdout is exactly `critic answer\n` and args are exactly:

```go
[]string{"--print", "--mode", "ask", "--output-format", "json", "--trust", expectedComposedPrompt}
```

Add table cases whose fake stdout is each of:

```text
{"type":"result","subtype":"error","is_error":true,"result":"failed"}
{"type":"result","subtype":"success","is_error":false,"result":""}
not-json
```

Every invalid case must return non-zero, emit no stdout, and identify the Cursor result problem on stderr. Also test missing executable and preservation of a fake exit code `10`.

- [ ] **Step 2: Run critic tests and verify RED**

```bash
go test ./cmd/agent-role/debatecritic -v
```

Expected: package missing/build failure.

- [ ] **Step 3: Add the critic prompt verbatim**

Create `system_prompt.txt` with the approved text beginning `당신은 토론의 비판자이자 레드팀이다.` and ending `* 제안자에게 묻고 싶은 핵심 질문`.

- [ ] **Step 4: Implement the Cursor adapter**

Use the same `Run`, `runWithIO`, embedding, and diagnostic structure as Task 2, with role `debate-critic` and engine `agent`. Decode:

```go
type cursorResult struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}
```

The provider must require `Type == "result"`, `Subtype == "success"`, `IsError == false`, and non-empty `Result`. JSON decoding uses `json.Unmarshal(stdout.Bytes(), &result)` and reports `decode agent JSON result` on malformed data.

- [ ] **Step 5: Verify GREEN and commit**

```bash
gofmt -w cmd/agent-role/debatecritic/*.go
go test ./cmd/agent-role/debatecritic -v
git add cmd/agent-role/debatecritic
git commit -m "feat: add debate critic role"
```

Expected: all critic adapter and validation tests pass before commit.

---

### Task 4: Codex judge role

**Files:**
- Create: `cmd/agent-role/debatejudge/debatejudge.go`
- Create: `cmd/agent-role/debatejudge/debatejudge_test.go`
- Create: `cmd/agent-role/debatejudge/system_prompt.txt`

**Interfaces:**
- Produces: `func Run(args []string) int`
- Runs: `codex exec --sandbox read-only --ask-for-approval never --cd <repo> -`
- Delivers: composed prompt on provider stdin

- [ ] **Step 1: Write failing judge tests**

Use a fake `codex` that records args, cwd, and stdin, then prints `judge answer`. Invoke `runWithIO` using task input from stdin and common JSON output.

Assert exact args:

```go
[]string{"exec", "--sandbox", "read-only", "--ask-for-approval", "never", "--cd", repo, "-"}
```

Assert recorded stdin contains the approved judge prompt inside `SYSTEM_ROLE` markers and the caller input inside `DEBATE_INPUT` markers. Assert envelope role `debate-judge`, engine `codex`, and content `judge answer`. Add missing-executable and exit-`11` cases.

- [ ] **Step 2: Run judge tests and verify RED**

```bash
go test ./cmd/agent-role/debatejudge -v
```

Expected: package missing/build failure.

- [ ] **Step 3: Add the judge prompt verbatim**

Create `system_prompt.txt` with the approved text beginning `당신은 토론의 심판이자 최종 설계자다.` and ending `8. 결론의 신뢰도: 높음 / 중간 / 낮음`.

- [ ] **Step 4: Implement the Codex adapter**

Use the same role wrapper structure with role `debate-judge` and engine `codex`. The provider core is:

```go
func (codexProvider) Run(ctx context.Context, req debaterole.ProviderRequest) (string, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex executable not found on PATH")
	}
	cmd := exec.CommandContext(ctx, path,
		"exec", "--sandbox", "read-only", "--ask-for-approval", "never", "--cd", req.Repository, "-",
	)
	cmd.Dir = req.Repository
	cmd.Stdin = strings.NewReader(req.Prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex failed: %w%s", err, diagnostic(stderr.String()))
	}
	return stdout.String(), nil
}
```

- [ ] **Step 5: Verify GREEN and commit**

```bash
gofmt -w cmd/agent-role/debatejudge/*.go
go test ./cmd/agent-role/debatejudge -v
git add cmd/agent-role/debatejudge
git commit -m "feat: add debate judge role"
```

Expected: all judge tests pass before commit.

---

### Task 5: Catalog, dispatch, docs, and full verification

**Files:**
- Modify: `internal/roles/roles.go`
- Modify: `internal/roles/roles_test.go`
- Modify: `internal/cli/role/role.go`
- Modify: `internal/cli/role/role_test.go`
- Modify: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

**Interfaces:**
- Produces: `RoleDebateProposer`, `RoleDebateCritic`, and `RoleDebateJudge`
- Produces: dispatch through both `agent-role` and `zellij-agent role`

- [ ] **Step 1: Write failing catalog tests**

Extend the catalog coverage and assert this table:

```go
tests := []struct{ name, usage string }{
	{RoleDebateProposer, "debate-proposer [options] <path> [prompt...]"},
	{RoleDebateCritic, "debate-critic [options] <path> [prompt...]"},
	{RoleDebateJudge, "debate-judge [options] <path> [prompt...]"},
}
```

For every spec require a required `path`, optional `prompt`, and optional `--output-format` argument.

- [ ] **Step 2: Write failing dispatch tests**

Add one test per role in `internal/cli/role/role_test.go`. Each creates a temporary Git repository, puts the matching fake provider on `PATH`, pipes a non-empty prompt into `os.Stdin`, and calls:

```go
Run([]string{"debate-proposer", repo})
Run([]string{"debate-critic", repo})
Run([]string{"debate-judge", repo})
```

Fake outputs are plain proposer text, Cursor success JSON, and plain judge text. Every call must return `0`.

- [ ] **Step 3: Run focused tests and verify RED**

```bash
go test ./internal/roles ./internal/cli/role -run 'Test.*Debate(Proposer|Critic|Judge)' -v
```

Expected: build failure because catalog constants and dispatch cases do not exist.

- [ ] **Step 4: Add catalog metadata**

Add:

```go
RoleDebateProposer = "debate-proposer"
RoleDebateCritic   = "debate-critic"
RoleDebateJudge    = "debate-judge"
```

Add three `RoleSpec` values with the exact usages above and:

```go
Arguments: []ArgumentSpec{
	{Name: "path", Required: true, Description: "File or directory path inside the repository to analyze."},
	{Name: "prompt", Required: false, Description: "Debate input; reads stdin when omitted."},
	{Name: "--output-format", Required: false, Description: "Output format: text or json. Defaults to text."},
},
```

Descriptions must state respectively: proposes/explores with `agy`, red-teams with Cursor Agent, and judges/finalizes with Codex.

- [ ] **Step 5: Wire dispatch**

Import the three packages and add:

```go
case roles.RoleDebateProposer:
	return debateproposer.Run(args[1:])
case roles.RoleDebateCritic:
	return debatecritic.Run(args[1:])
case roles.RoleDebateJudge:
	return debatejudge.Run(args[1:])
```

- [ ] **Step 6: Verify focused integration GREEN**

```bash
gofmt -w internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go
go test ./internal/roles ./internal/cli/role -v
```

Expected: all existing and new catalog/dispatch tests pass.

- [ ] **Step 7: Update the external role summary**

Add three summary rows and detail sections to `/Users/in05908_mac/.config/pi/docs/agent-roles.md`. Each detail section must state the exact usage, provider, responsibility, stdin fallback, `--output-format text|json`, read-only flags, provider executable requirement, and the five `debate-role/v1` fields.

- [ ] **Step 8: Run full verification and register the unified binary**

```bash
go test ./...
go build -o bin/agent-role ./cmd/agent-role
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
./bin/agent-role roles
./bin/agent-role roles --json
rg -n 'debate-proposer|debate-critic|debate-judge|debate-role/v1' /Users/in05908_mac/.config/pi/docs/agent-roles.md
```

Expected: all commands exit `0`; both listings contain all three exact usages; external docs contain all roles and schema version.

- [ ] **Step 9: Inspect scope and commit integration**

```bash
git diff --check
git status --short
git diff --stat
```

Verify no planner or `internal/debate` files changed. Then:

```bash
git add internal/roles/roles.go internal/roles/roles_test.go internal/cli/role/role.go internal/cli/role/role_test.go
git commit -m "feat: register debate agent roles"
```

The external role summary is verified but remains outside this repository commit.
