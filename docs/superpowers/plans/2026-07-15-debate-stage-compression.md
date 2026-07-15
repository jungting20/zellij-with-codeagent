# Debate Stage Compression Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound all three debate role outputs and role-to-role handoffs so `debate-background` never feeds an unbounded proposer and critic transcript to the judge.

**Architecture:** Add optional Unicode-aware content compaction to the shared `debaterole` runner, opt the three debate roles into exact 2,000/2,000/3,000 character limits, and reinforce those limits in embedded and orchestration prompts. Extend internal progress events with compacted character counts and raise the default per-role timeout to three minutes without changing either public JSON schema.

**Tech Stack:** Go standard library (`unicode/utf8`, `flag`, `testing`), embedded text prompts, existing `debate-role/v1` and `debate-background/v1` contracts.

## Global Constraints

- Proposer and critic `content` must never exceed 2,000 Unicode code points; judge `content` must never exceed 3,000.
- Compaction preserves the beginning and end in a 70/30 split and inserts `[출력 길이 제한으로 중간 내용 생략]`.
- A zero content limit preserves the shared runner's legacy newline-trimming behavior.
- Blank provider content remains an error.
- `debate-role/v1` and `debate-background/v1` JSON fields do not change.
- Default `--agent-timeout` is `3m`; overall default remains `10m`; explicit overrides remain valid.
- Progress remains on stderr; JSON stdout remains exactly one document.
- Do not add retries, another model call, configurable role order, or topic truncation.
- After rebuilding `bin/zellij-agent`, immediately copy it to `~/.config/custom-cli`.

---

### Task 1: Add Unicode-aware compaction to the shared role runner

**Files:**
- Modify: `internal/debaterole/role.go`
- Test: `internal/debaterole/role_test.go`

**Interfaces:**
- Consumes: existing `Run(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg Config) int`.
- Produces: `Config.MaxContentChars int` and `compactContent(content string, maxChars int) string`.
- Preserves: zero-limit behavior uses `strings.TrimRight(content, "\r\n")`.

- [ ] **Step 1: Write failing compaction tests**

Add `strings` and `unicode/utf8` to `role_test.go`, then add:

```go
func TestCompactContentPreservesContentWithinLimit(t *testing.T) {
	got := compactContent("  concise answer\r\n", 100)
	if got != "concise answer" {
		t.Fatalf("compactContent() = %q, want concise answer", got)
	}
}

func TestCompactContentBoundsUnicodeAndPreservesBothEnds(t *testing.T) {
	const maxChars = 80
	input := "시작-" + strings.Repeat("가", 100) + "-최종결론"
	got := compactContent(input, maxChars)
	if count := utf8.RuneCountInString(got); count > maxChars {
		t.Fatalf("rune count = %d, want <= %d; content=%q", count, maxChars, got)
	}
	if !strings.HasPrefix(got, "시작-") || !strings.HasSuffix(got, "-최종결론") {
		t.Fatalf("compactContent() = %q, want beginning and end", got)
	}
	if !strings.Contains(got, "[출력 길이 제한으로 중간 내용 생략]") {
		t.Fatalf("compactContent() = %q, want omission marker", got)
	}
}

func TestCompactContentWithZeroLimitPreservesLegacyWhitespace(t *testing.T) {
	got := compactContent("  answer  \r\n", 0)
	if got != "  answer  " {
		t.Fatalf("compactContent() = %q, want legacy whitespace", got)
	}
}
```

Add this case to the `TestRunValidation` table:

```go
{
	name:     "whitespace-only provider response",
	args:     []string{repo, "prompt"},
	provider: successfulProvider(" \t\r\n"),
	wantCode: 1,
},
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/debaterole -run 'TestCompactContent|TestRunValidation' -count=1`

Expected: build failure because `compactContent` does not exist.

- [ ] **Step 3: Implement the minimal shared compactor**

Extend `Config`:

```go
type Config struct {
	Role            string
	Engine          string
	SystemPrompt    string
	Provider        Provider
	MaxContentChars int
}
```

Replace response trimming and validation in `Run` with:

```go
content = compactContent(content, cfg.MaxContentChars)
if strings.TrimSpace(content) == "" {
	fmt.Fprintln(stderr, "Error: provider returned an empty response")
	return 1
}
```

Add below `Run`:

```go
const contentOmissionMarker = "\n\n[출력 길이 제한으로 중간 내용 생략]\n\n"

func compactContent(content string, maxChars int) string {
	if maxChars <= 0 {
		return strings.TrimRight(content, "\r\n")
	}
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	marker := []rune(contentOmissionMarker)
	remaining := maxChars - len(marker)
	if remaining <= 0 {
		return string(marker[:maxChars])
	}
	headCount := remaining * 70 / 100
	tailCount := remaining - headCount
	result := strings.TrimSpace(string(runes[:headCount])) + contentOmissionMarker +
		strings.TrimSpace(string(runes[len(runes)-tailCount:]))
	if resultRunes := []rune(result); len(resultRunes) > maxChars {
		result = string(resultRunes[:maxChars])
	}
	return result
}
```

- [ ] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w internal/debaterole/role.go internal/debaterole/role_test.go
go test ./internal/debaterole -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/debaterole/role.go internal/debaterole/role_test.go
git commit -m "feat: bound debate role content"
```

---

### Task 2: Opt all standalone debate roles into concise contracts

**Files:**
- Modify/Test: `cmd/agent-role/debateproposer/debateproposer.go`, `system_prompt.txt`, `debateproposer_test.go`
- Modify/Test: `cmd/agent-role/debatecritic/debatecritic.go`, `system_prompt.txt`, `debatecritic_test.go`
- Modify/Test: `cmd/agent-role/debatejudge/debatejudge.go`, `system_prompt.txt`, `debatejudge_test.go`
- Modify outside repository: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

**Interfaces:**
- Consumes: `debaterole.Config.MaxContentChars` from Task 1.
- Produces: package constants `maxContentChars` with values 2000, 2000, and 3000, plus `roleConfig(provider debaterole.Provider) debaterole.Config` in each role package.
- Preserves: provider commands, repository pinning, prompt delivery, and role JSON schema.

- [ ] **Step 1: Write failing limit and prompt assertions**

Add these package-level configuration tests:

```go
// debateproposer_test.go
func TestRoleConfigSetsContentLimit(t *testing.T) {
	cfg := roleConfig(agyProvider{})
	if cfg.MaxContentChars != 2000 {
		t.Fatalf("MaxContentChars = %d, want 2000", cfg.MaxContentChars)
	}
}

// debatecritic_test.go
func TestRoleConfigSetsContentLimit(t *testing.T) {
	cfg := roleConfig(agentProvider{})
	if cfg.MaxContentChars != 2000 {
		t.Fatalf("MaxContentChars = %d, want 2000", cfg.MaxContentChars)
	}
}

// debatejudge_test.go
func TestRoleConfigSetsContentLimit(t *testing.T) {
	cfg := roleConfig(codexProvider{})
	if cfg.MaxContentChars != 3000 {
		t.Fatalf("MaxContentChars = %d, want 3000", cfg.MaxContentChars)
	}
}
```

Extend each `expectedSystemPrompt` with its exact block:

Proposer:

```text

간결한 출력 규칙:

* 전체 출력은 2,000자 이내로 작성한다.
* 후보안은 2~3개, 각 후보안은 최대 2개 항목으로 제한한다.
* 구체적인 근거는 전체 최대 5개만 남긴다.
* 주제 반복, 도구 로그, 탐색 과정, 긴 파일 목록은 생략한다.
* 위 출력 형식의 6개 섹션은 모두 유지한다.
```

Critic:

```text

간결한 출력 규칙:

* 전체 출력은 2,000자 이내로 작성한다.
* 타당한 부분, 치명적인 문제, 중요한 누락, 수정 제안, 핵심 질문은 각각 최대 3개로 제한한다.
* 실패 시나리오와 반례는 각각 최대 2개로 제한한다.
* 의사결정을 바꾸는 문제를 우선하고 제안 원문을 길게 반복하지 않는다.
* 도구 로그, 탐색 과정, 긴 파일 목록은 생략한다.
* 위 출력 형식의 7개 섹션은 모두 유지한다.
```

Judge:

```text

간결한 출력 규칙:

* 전체 출력은 3,000자 이내로 작성한다.
* 각 출력 섹션은 최대 3개 항목으로 제한한다.
* 최종 권고안과 실행 단계는 바로 실행할 수 있게 작성한다.
* 채택·기각 판단에 필요한 근거만 남기고 제안과 비판을 길게 반복하지 않는다.
* 도구 로그, 탐색 과정, 긴 파일 목록은 생략한다.
* 위 출력 형식의 8개 섹션은 모두 유지한다.
```

- [ ] **Step 2: Run role tests and verify RED**

Run: `go test ./cmd/agent-role/debateproposer ./cmd/agent-role/debatecritic ./cmd/agent-role/debatejudge -count=1`

Expected: build failure because `roleConfig` does not exist.

- [ ] **Step 3: Configure limits and prompts**

Add `const maxContentChars = 2000` near the embedded prompt in proposer and critic, and `const maxContentChars = 3000` in judge. Move each current config literal into its exact helper:

```go
// cmd/agent-role/debateproposer/debateproposer.go
func roleConfig(provider debaterole.Provider) debaterole.Config {
	return debaterole.Config{
		Role:            "debate-proposer",
		Engine:          "agy",
		SystemPrompt:    systemPrompt,
		Provider:        provider,
		MaxContentChars: maxContentChars,
	}
}

// cmd/agent-role/debatecritic/debatecritic.go
func roleConfig(provider debaterole.Provider) debaterole.Config {
	return debaterole.Config{
		Role:            "debate-critic",
		Engine:          "agent",
		SystemPrompt:    systemPrompt,
		Provider:        provider,
		MaxContentChars: maxContentChars,
	}
}

// cmd/agent-role/debatejudge/debatejudge.go
func roleConfig(provider debaterole.Provider) debaterole.Config {
	return debaterole.Config{
		Role:            "debate-judge",
		Engine:          "codex",
		SystemPrompt:    systemPrompt,
		Provider:        provider,
		MaxContentChars: maxContentChars,
	}
}
```

Change the three `runWithIO` calls to pass `roleConfig(agyProvider{})`, `roleConfig(agentProvider{})`, and `roleConfig(codexProvider{})`, respectively.

Append the exact Step 1 blocks to the respective `system_prompt.txt` files without changing the user's original prompt text.

- [ ] **Step 4: Update the external role summary**

Add these runtime guarantees to the existing entries in `/Users/in05908_mac/.config/pi/docs/agent-roles.md`:

```text
- debate-proposer: output is compacted to at most 2,000 Unicode characters.
- debate-critic: output is compacted to at most 2,000 Unicode characters.
- debate-judge: output is compacted to at most 3,000 Unicode characters.
```

- [ ] **Step 5: Format and verify GREEN**

Run:

```bash
gofmt -w cmd/agent-role/debateproposer/debateproposer.go cmd/agent-role/debateproposer/debateproposer_test.go cmd/agent-role/debatecritic/debatecritic.go cmd/agent-role/debatecritic/debatecritic_test.go cmd/agent-role/debatejudge/debatejudge.go cmd/agent-role/debatejudge/debatejudge_test.go
go test ./cmd/agent-role/debateproposer ./cmd/agent-role/debatecritic ./cmd/agent-role/debatejudge -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit repository-owned changes**

```bash
git add cmd/agent-role/debateproposer cmd/agent-role/debatecritic cmd/agent-role/debatejudge
git commit -m "feat: compress debate role responses"
```

The external role summary is not part of the repository commit.

---

### Task 3: Bound orchestration prompts and report compacted sizes

**Files:**
- Modify: `internal/backgrounddebate/prompts.go`
- Create: `internal/backgrounddebate/prompts_test.go`
- Modify: `internal/backgrounddebate/model.go`
- Modify/Test: `internal/backgrounddebate/orchestrator.go`, `orchestrator_test.go`

**Interfaces:**
- Consumes: bounded `debaterole.Result.Content` from role commands.
- Produces: `ProgressEvent.ContentChars int`, populated only for completed events.
- Preserves: role order, labelled prompt sections, and result JSON structs.

- [ ] **Step 1: Write failing prompt tests**

Create `internal/backgrounddebate/prompts_test.go`:

```go
package backgrounddebate

import (
	"strings"
	"testing"
)

func TestStagePromptsRequireConciseOutput(t *testing.T) {
	tests := []struct {
		name, prompt, wantLimit, wantRepeat string
	}{
		{name: "proposer", prompt: proposerPrompt("topic", "prior"), wantLimit: "2,000 characters"},
		{name: "critic", prompt: criticPrompt("topic", "proposal"), wantLimit: "2,000 characters", wantRepeat: "Do not quote the proposal at length."},
		{name: "judge", prompt: judgePrompt("topic", "proposal", "critique"), wantLimit: "3,000 characters", wantRepeat: "Do not restate the proposal or critique."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.prompt, tt.wantLimit) {
				t.Fatalf("prompt = %q, want %q", tt.prompt, tt.wantLimit)
			}
			if tt.wantRepeat != "" && !strings.Contains(tt.prompt, tt.wantRepeat) {
				t.Fatalf("prompt = %q, want %q", tt.prompt, tt.wantRepeat)
			}
		})
	}
}
```

- [ ] **Step 2: Write a failing progress-size test**

Add to `orchestrator_test.go`:

```go
func TestRunReportsCompletedContentCharacterCounts(t *testing.T) {
	runner := &recordingRunner{}
	var events []ProgressEvent
	result := Run(context.Background(), runner, Options{
		Topic: "topic", Repository: "/repo", Rounds: 1, AgentTimeout: time.Second,
		Progress: func(event ProgressEvent) { events = append(events, event) },
	})
	if result.Status != StatusSuccess {
		t.Fatalf("Run() status = %q, want success", result.Status)
	}
	var completed []ProgressEvent
	for _, event := range events {
		if event.Status == "completed" {
			completed = append(completed, event)
		}
	}
	want := []ProgressEvent{
		{Round: 1, Rounds: 1, Role: Proposer.Name, Status: "completed", ContentChars: len([]rune("proposal-1"))},
		{Round: 1, Rounds: 1, Role: Critic.Name, Status: "completed", ContentChars: len([]rune("critique-1"))},
		{Round: 1, Rounds: 1, Role: Judge.Name, Status: "completed", ContentChars: len([]rune("judgment-1"))},
	}
	if !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed events = %#v, want %#v", completed, want)
	}
}
```

- [ ] **Step 3: Run tests and verify RED**

Run: `go test ./internal/backgrounddebate -run 'TestStagePromptsRequireConciseOutput|TestRunReportsCompletedContentCharacterCounts' -count=1`

Expected: prompt assertions fail and `ProgressEvent.ContentChars` is undefined.

- [ ] **Step 4: Add exact stage instructions**

Retain every existing section and append these lines to the respective formatted prompts:

```text
Return no more than 2,000 characters.
```

```text
Return no more than 2,000 characters. Do not quote the proposal at length.
```

```text
Return no more than 3,000 characters. Do not restate the proposal or critique.
```

- [ ] **Step 5: Add and populate `ContentChars`**

Change `ProgressEvent` in `model.go`:

```go
type ProgressEvent struct {
	Round        int
	Rounds       int
	Role         string
	Status       string
	ContentChars int
}
```

Import `unicode/utf8` in `orchestrator.go` and replace the successful event with:

```go
progress(opts.Progress, ProgressEvent{
	Round: round, Rounds: opts.Rounds, Role: role.Name, Status: "completed",
	ContentChars: utf8.RuneCountInString(roleResult.Content),
})
```

- [ ] **Step 6: Format and verify GREEN**

```bash
gofmt -w internal/backgrounddebate/prompts.go internal/backgrounddebate/prompts_test.go internal/backgrounddebate/model.go internal/backgrounddebate/orchestrator.go internal/backgrounddebate/orchestrator_test.go
go test ./internal/backgrounddebate -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/backgrounddebate
git commit -m "feat: bound background debate handoffs"
```

---

### Task 4: Raise the default timeout and expose sizes on stderr

**Files:**
- Modify/Test: `internal/cli/debatebackground/debatebackground.go`, `debatebackground_test.go`

**Interfaces:**
- Consumes: `ProgressEvent.ContentChars` from Task 3.
- Produces: default `AgentTimeout` of `3*time.Minute` and completed lines with `content_chars=<n>`.
- Preserves: explicit timeout flags, overall `10m`, stdout rendering, persistence, and exit codes.

- [ ] **Step 1: Write failing CLI assertions**

In `TestRunJSONKeepsStdoutStructuredAndProgressOnStderr`, add:

```go
for _, req := range runner.requests {
	if req.Timeout != 3*time.Minute {
		t.Fatalf("role %s timeout = %v, want 3m", req.Role.Name, req.Timeout)
	}
}
for _, want := range []string{
	"role=debate-proposer status=completed content_chars=15",
	"role=debate-critic status=completed content_chars=13",
	"role=debate-judge status=completed content_chars=12",
} {
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, missing %q", stderr.String(), want)
	}
}
```

Add `"--agent-timeout duration       Per-role timeout (default 3m)."` to the help test's expected strings.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/cli/debatebackground -run 'TestRunJSONKeepsStdoutStructuredAndProgressOnStderr|TestRunHelpDocumentsCompatibilityAndOutputFormat' -count=1`

Expected: current timeout is `2m0s`, help says `2m`, and completed lines lack `content_chars`.

- [ ] **Step 3: Implement timeout and progress changes**

Change the flag default:

```go
agentTimeout := fs.Duration("agent-timeout", 3*time.Minute, "per-role response timeout")
```

Change help:

```go
fmt.Fprintln(w, "  --agent-timeout duration       Per-role timeout (default 3m).")
```

Replace the role-event print in `progressWriter` with:

```go
if event.Status == "completed" {
	fmt.Fprintf(stderr, "[debate progress] round=%d/%d role=%s status=%s content_chars=%d\n",
		event.Round, event.Rounds, event.Role, event.Status, event.ContentChars)
} else {
	fmt.Fprintf(stderr, "[debate progress] round=%d/%d role=%s status=%s\n",
		event.Round, event.Rounds, event.Role, event.Status)
}
```

Keep round-level started/completed messages unchanged.

- [ ] **Step 4: Format and verify GREEN**

```bash
gofmt -w internal/cli/debatebackground/debatebackground.go internal/cli/debatebackground/debatebackground_test.go
go test ./internal/cli/debatebackground -count=1
```

Expected: PASS and JSON cleanliness remains passing.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/debatebackground/debatebackground.go internal/cli/debatebackground/debatebackground_test.go
git commit -m "fix: allow concise debate roles three minutes"
```

---

### Task 5: Run regression verification and install the binary

**Files:**
- Verify: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`
- Build: `bin/zellij-agent`
- Install: `/Users/in05908_mac/.config/custom-cli`

**Interfaces:**
- Consumes: Tasks 1 through 4.
- Produces: tested code and byte-identical built/installed binaries.

- [ ] **Step 1: Run focused tests together**

Run: `go test ./internal/debaterole ./cmd/agent-role/debateproposer ./cmd/agent-role/debatecritic ./cmd/agent-role/debatejudge ./internal/backgrounddebate ./internal/cli/debatebackground -count=1`

Expected: all packages PASS.

- [ ] **Step 2: Run the full suite**

Run: `go test ./...`

Expected: PASS for every package.

- [ ] **Step 3: Verify formatting and diff hygiene**

```bash
gofmt -w internal/debaterole/role.go internal/debaterole/role_test.go cmd/agent-role/debateproposer/debateproposer.go cmd/agent-role/debateproposer/debateproposer_test.go cmd/agent-role/debatecritic/debatecritic.go cmd/agent-role/debatecritic/debatecritic_test.go cmd/agent-role/debatejudge/debatejudge.go cmd/agent-role/debatejudge/debatejudge_test.go internal/backgrounddebate/prompts.go internal/backgrounddebate/prompts_test.go internal/backgrounddebate/model.go internal/backgrounddebate/orchestrator.go internal/backgrounddebate/orchestrator_test.go internal/cli/debatebackground/debatebackground.go internal/cli/debatebackground/debatebackground_test.go
git diff --check
```

Expected: `git diff --check` prints nothing.

- [ ] **Step 4: Build and immediately install**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
cmp bin/zellij-agent ~/.config/custom-cli
```

Expected: build succeeds and `cmp` exits 0 without output.

- [ ] **Step 5: Verify help and external documentation**

```bash
./bin/zellij-agent debate-background --help
rg -n "2,000 Unicode|3,000 Unicode|debate-proposer|debate-critic|debate-judge" /Users/in05908_mac/.config/pi/docs/agent-roles.md
```

Expected: help reports default `3m`; the external document contains all three roles and their 2,000/2,000/3,000 Unicode-character limits.

- [ ] **Step 6: Confirm no unintended changes**

```bash
git status --short
git log -5 --oneline
```

Expected: repository changes correspond only to the approved compression design and implementation tasks.
