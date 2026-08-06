# Zellij 기반 Loop Project Runner 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 기존 loop-project-runner의 상태·증거 계약을 유지하면서 Codex worker와 실제 read-only verifier를 사용자가 볼 수 있는 Zellij pane으로 실행하는 `loop-project-zellij-runner` 스킬을 만든다.

**Architecture:** `zellij-agent`에는 Codex access mode와 두 기본 역할을 추가하고, 새 스킬은 오케스트레이터가 같은 tab의 worker/verifier pane을 직접 생성하도록 한다. 역할 간 `ctl message`는 실행 제어만 담당하며 PLAN, CURRENT, append-only checkpoint가 공식 상태와 증거의 유일한 기준이다.

**Tech Stack:** Go 1.x 표준 라이브러리와 `testing`, Zellij CLI, `zellij-agent` Unix-socket runtime, Codex CLI, Python 3 unittest/pytest-compatible tests, Markdown Agent Skill.

## Global Constraints

- 첫 버전은 Codex만 지원한다.
- 오케스트레이터만 worker와 verifier pane을 생성한다.
- 모든 역할 pane은 오케스트레이터와 같은 Zellij tab에 생성한다.
- verifier command는 `codex --sandbox read-only --ask-for-approval never`를 사용하고 permission-bypass 인자를 포함하지 않는다.
- 기존 `agent start` 호출의 기본 동작은 full access로 유지한다.
- `ctl message`는 trailing newline과 Enter까지 전달하고 `message_sent` 이벤트와 snapshot으로 확인한다.
- pane 메시지는 bounded control signal이며 PLAN, CURRENT, checkpoint를 대체하지 않는다.
- checkpoint는 `append_checkpoint.py`로만 append하며 verifier STARTED/FINISHED/raw output은 한 run ID로 연결한다.
- verifier pane은 evidence 기록과 runtime validation 후 닫고 worker pane은 마일스톤 경계 검증 후 닫는다.
- task-owned exact logical pane ID만 cleanup하고 Zellij session/tab은 종료하지 않는다.
- 기본 역할을 먼저 추가한다는 저장소 규칙을 지킨다.
- Go 파일은 `gofmt`하고 관련 테스트와 `go test ./...`를 실행한다.
- 커밋 메시지는 반드시 한글로 작성한다.

---

## 파일 구조

### 저장소 코드

- `cmd/agent-role/loopprojectagent/loopprojectagent.go`: worker/verifier 역할 인자 검증, bootstrap prompt와 `zellij-agent agent start` command 구성
- `cmd/agent-role/loopprojectagent/loopprojectagent_test.go`: 두 역할의 command, prompt, 오류와 child exit-code 검증
- `internal/roles/roles.go`: `loop-project-worker`, `loop-project-verifier` catalog metadata
- `internal/cli/role/role.go`: 두 role의 얇은 dispatch
- `internal/codingagent/access.go`: access mode parsing과 canonical values
- `internal/codingagent/profile.go`: managed agent command를 access mode별로 생성
- `internal/codingagent/types.go`: agent record에 access mode 보존
- `internal/codingagent/service.go`: access 검증, command 생성, pane claim
- `internal/transport/types.go`: access mode JSON request/response 변환
- `internal/transport/errors.go`: 잘못된 access mode를 `bad_request`로 변환
- `internal/cli/agent/agent.go`: `agent start --access full|read-only`
- `internal/agentdashboard/view.go`: agent access mode 표시
- `docs/zellij-agent-quickstart.md`: read-only start와 loop 역할 사용법
- `/Users/in05908_mac/.config/pi/docs/agent-roles.md`: 외부 role 요약 동기화

### 스킬 소스와 설치본

- `.agents/skills/loop-project-zellij-runner/`: 저장소에서 버전 관리하는 canonical skill source
- `/Users/in05908_mac/.agents/skills/loop-project-zellij-runner/`: 검증 후 설치되는 byte-identical skill
- `.agents/skills/loop-project-zellij-runner/SKILL.md`: orchestration state machine
- `.agents/skills/loop-project-zellij-runner/references/pane-protocol.md`: bootstrap, assignment, signal, ACK, cleanup 계약
- `.agents/skills/loop-project-zellij-runner/references/milestone-dispatch.md`: visible worker pane dispatch
- `.agents/skills/loop-project-zellij-runner/references/verifier-dispatch.md`: read-only verifier pane dispatch
- `.agents/skills/loop-project-zellij-runner/references/runtime-contracts.md`: 기존 공식 상태 계약
- `.agents/skills/loop-project-zellij-runner/references/execution-logging.md`: 기존 append-only evidence 계약
- `.agents/skills/loop-project-zellij-runner/scripts/`: validator와 append helper
- `.agents/skills/loop-project-zellij-runner/tests/`: 계약, validator, pane protocol tests
- `.agents/skills/loop-project-zellij-runner/evals/evals.json`: READY, REJECT, recovery 시나리오

---

### Task 1: Loop project 기본 역할 추가

**Files:**
- Create: `cmd/agent-role/loopprojectagent/loopprojectagent.go`
- Create: `cmd/agent-role/loopprojectagent/loopprojectagent_test.go`
- Modify: `internal/roles/roles.go`
- Modify: `internal/roles/roles_test.go`
- Modify: `internal/cli/role/role.go`
- Modify: `internal/cli/role/role_test.go`
- Modify: `/Users/in05908_mac/.config/pi/docs/agent-roles.md`

**Interfaces:**
- Consumes: `zellij-agent agent start codex --cwd DIR --access MODE -- PROMPT`
- Produces: `loopprojectagent.RunWorker(args []string) int`, `loopprojectagent.RunVerifier(args []string) int`
- Produces: role constants `roles.RoleLoopProjectWorker`, `roles.RoleLoopProjectVerifier`

- [ ] **Step 1: role catalog의 실패 테스트 작성**

`internal/roles/roles_test.go`에 다음 기대를 추가한다.

```go
for _, name := range []string{RoleLoopProjectWorker, RoleLoopProjectVerifier} {
	if _, ok := Lookup(name); !ok {
		t.Fatalf("Lookup(%q) not found", name)
	}
}
```

각 spec의 usage는 정확히 다음이어야 한다.

```text
loop-project-worker --repository PATH --runner-skill PATH --orchestrator-pane PANE_ID
loop-project-verifier --repository PATH --runner-skill PATH --orchestrator-pane PANE_ID
```

- [ ] **Step 2: catalog 테스트가 실패하는지 확인**

Run: `go test ./internal/roles -run 'Test.*LoopProject' -v`

Expected: `RoleLoopProjectWorker`와 `RoleLoopProjectVerifier`가 정의되지 않아 build FAIL.

- [ ] **Step 3: role package의 실패 테스트 작성**

`cmd/agent-role/loopprojectagent/loopprojectagent_test.go`에서 temp Git repository와 fake `zellij-agent` executable을 만들고 다음 command를 검증한다.

```go
wantWorker := []string{
	"zellij-agent", "agent", "start", "codex",
	"--cwd", repo,
	"--access", "full",
	"--", workerBootstrap,
}
wantVerifier := []string{
	"zellij-agent", "agent", "start", "codex",
	"--cwd", repo,
	"--access", "read-only",
	"--", verifierBootstrap,
}
```

bootstrap에는 role, absolute repository, absolute runner skill, orchestrator logical pane ID, assignment 전 쓰기 금지와 `ctl message` 사용 지시가 포함되어야 한다. verifier bootstrap에는 `code_changes: FORBIDDEN`도 포함한다.

- [ ] **Step 4: role package 최소 구현**

다음 공개 경계를 구현한다.

```go
type Mode string

const (
	ModeWorker   Mode = "worker"
	ModeVerifier Mode = "verifier"
)

func RunWorker(args []string) int   { return run(ModeWorker, args) }
func RunVerifier(args []string) int { return run(ModeVerifier, args) }

func prepare(mode Mode, args []string) (*exec.Cmd, error)
func bootstrapPrompt(mode Mode, repository, runnerSkill, orchestratorPane string) string
```

`prepare`는 세 필수 flag를 요구하고 repository의 `.git`과 runner skill의 `SKILL.md`를 검사한다. worker는 `--access full`, verifier는 `--access read-only`를 사용한다. child process에는 현재 stdin/stdout/stderr를 연결하고 exit code를 보존한다.

- [ ] **Step 5: catalog와 role dispatch 구현**

`internal/roles/roles.go`에 두 constant와 spec을 추가하고 `internal/cli/role/role.go`에 다음 dispatch를 추가한다.

```go
case roles.RoleLoopProjectWorker:
	return loopprojectagent.RunWorker(args[1:])
case roles.RoleLoopProjectVerifier:
	return loopprojectagent.RunVerifier(args[1:])
```

`internal/cli/role/role_test.go`는 fake executable을 통해 두 case가 role package까지 전달되는지 확인한다.

- [ ] **Step 6: role 테스트 실행**

Run: `gofmt -w cmd/agent-role/loopprojectagent internal/roles internal/cli/role`

Run: `go test ./cmd/agent-role/loopprojectagent ./internal/roles ./internal/cli/role -v`

Expected: PASS.

- [ ] **Step 7: 외부 role 문서 동기화**

`/Users/in05908_mac/.config/pi/docs/agent-roles.md`에 두 usage, Codex 전용 조건, 필수 인자, Zellij/daemon 요구사항과 verifier read-only 의미를 추가한다.

Run: `rg -n 'loop-project-(worker|verifier)' /Users/in05908_mac/.config/pi/docs/agent-roles.md`

Expected: 두 역할이 각각 한 개의 role section에 나타남.

- [ ] **Step 8: 기본 역할 커밋**

```bash
git add cmd/agent-role/loopprojectagent internal/roles internal/cli/role
git commit -m "feat: 루프 프로젝트 기본 역할 추가"
```

---

### Task 2: Coding-agent access mode 도메인 구현

**Files:**
- Create: `internal/codingagent/access.go`
- Create: `internal/codingagent/access_test.go`
- Modify: `internal/codingagent/profile.go`
- Modify: `internal/codingagent/profile_test.go`
- Modify: `internal/codingagent/types.go`
- Modify: `internal/codingagent/store.go`
- Modify: `internal/codingagent/store_test.go`
- Modify: `internal/codingagent/service.go`
- Modify: `internal/codingagent/service_test.go`

**Interfaces:**
- Produces: `type AccessMode string`
- Produces: `AccessFull`, `AccessReadOnly`, `ParseAccessMode(string) (AccessMode, error)`
- Produces: `Profile.BuildManagedCommand(AccessMode, []string) ([]string, error)`
- Changes: `codingagent.StartAgentRequest.AccessMode`, `codingagent.Record.AccessMode`

- [ ] **Step 1: access parsing과 command의 실패 테스트 작성**

```go
func TestParseAccessModeDefaultsToFull(t *testing.T) {
	got, err := ParseAccessMode("")
	if err != nil || got != AccessFull {
		t.Fatalf("ParseAccessMode() = %q, %v", got, err)
	}
}

func TestBuildManagedCommandReadOnlyCodex(t *testing.T) {
	profile, _ := LookupProfile(KindCodex)
	got, err := profile.BuildManagedCommand(AccessReadOnly, []string{"review this repository"})
	want := []string{"codex", "--sandbox", "read-only", "--ask-for-approval", "never", "review this repository"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("command=%#v err=%v", got, err)
	}
}
```

추가 case는 unknown access 거부, non-Codex read-only 거부, full access가 기존 bypass command를 보존하는지 확인한다.

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/codingagent -run 'Test(ParseAccess|BuildManagedCommand)' -v`

Expected: 새 type과 method가 없어 build FAIL.

- [ ] **Step 3: access type과 managed command 구현**

`internal/codingagent/access.go`에 다음 canonical contract를 구현한다.

```go
type AccessMode string

const (
	AccessFull     AccessMode = "full"
	AccessReadOnly AccessMode = "read-only"
)

var ErrInvalidAccessMode = errors.New("invalid coding agent access mode")

func ParseAccessMode(value string) (AccessMode, error)
```

빈 문자열은 backward-compatible `AccessFull`로 정규화한다. `BuildManagedCommand`는 full에서 기존 bypass 인자를 사용하고, Codex read-only에서만 sandbox/approval 인자를 사용한다. 기존 `BuildCommand(bool, []string)`는 `coding-agent --yolo` 호환을 위해 유지한다.

- [ ] **Step 4: record/store 실패 테스트와 구현**

`Record`에 `AccessMode AccessMode`를 추가한다. `validateRecord`는 빈 값을 full로 허용하되 unknown 값은 `ErrInvalidRecord`와 `ErrInvalidAccessMode`를 함께 식별할 수 있는 오류로 거부한다.

Run: `go test ./internal/codingagent -run 'TestMemoryStore.*Access' -v`

Expected after implementation: valid full/read-only records PASS, unknown mode rejection PASS.

- [ ] **Step 5: service access mode 실패 테스트 작성**

`Service.StartAgent`에 대해 다음을 검증한다.

```text
AccessMode empty     -> record full, existing bypass command
AccessMode full      -> record full, existing bypass command
AccessMode read-only -> Codex read-only command, no bypass token
read-only Gemini     -> registration/monitor/runtime side effect 없이 오류
unknown mode         -> registration/monitor/runtime side effect 없이 오류
```

- [ ] **Step 6: service 구현과 테스트**

`StartAgent`가 CWD와 source pane side effect 전에 access mode를 parse하고 command를 생성하게 한다. 생성된 canonical mode를 `Record.AccessMode`에 저장하고 동일 command를 `ClaimPaneRequest.Command`에 전달한다.

Run: `gofmt -w internal/codingagent`

Run: `go test ./internal/codingagent -v`

Expected: PASS.

- [ ] **Step 7: access 도메인 커밋**

```bash
git add internal/codingagent
git commit -m "feat: 코딩 에이전트 접근 모드 추가"
```

---

### Task 3: Access mode transport, CLI와 dashboard 연결

**Files:**
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/types_test.go`
- Modify: `internal/transport/errors.go`
- Modify: `internal/transport/errors_test.go`
- Modify: `internal/transport/server_test.go`
- Modify: `internal/cli/agent/agent.go`
- Modify: `internal/cli/agent/agent_test.go`
- Modify: `internal/agentdashboard/view.go`
- Modify: `internal/agentdashboard/view_test.go`
- Modify: `docs/zellij-agent-quickstart.md`

**Interfaces:**
- Consumes: `codingagent.AccessMode`
- Produces: request JSON `access`, agent response JSON `access`
- Produces: CLI `zellij-agent agent start codex --access full|read-only`

- [ ] **Step 1: transport round-trip 실패 테스트 확장**

`TestAgentStartRequestRoundTripPreservesSourceAndArguments`의 JSON에
`"access":"read-only"`를 추가하고 converted request와 encoded response 모두 값을
보존하는지 검사한다. `TestAgentResponseConversionPreservesRecordTimestampsAndPane`은
`Record.AccessMode == AccessReadOnly`가 `Agent.Access == "read-only"`로 변환되는지
검사한다.

- [ ] **Step 2: transport 구현**

```go
type StartAgentRequest struct {
	Access string `json:"access,omitempty"`
	// existing fields
}

type Agent struct {
	Access string `json:"access"`
	// existing fields
}
```

`ToCodingAgent`, `StartAgentRequestFromCodingAgent`, `AgentFromCodingAgent`에서 access를
손실 없이 변환한다. `transport.ErrorFor`는 `ErrInvalidAccessMode`를 HTTP 400
`bad_request`로 변환한다.

- [ ] **Step 3: transport 테스트 실행**

Run: `gofmt -w internal/transport`

Run: `go test ./internal/transport -run 'TestAgent|TestError' -v`

Expected: PASS.

- [ ] **Step 4: CLI 실패 테스트 작성**

`TestRunStartSendsValidatedRequest`와 별도 case로 다음을 검증한다.

```go
args := []string{"start", "codex", "--access", "read-only"}
wantAccess := "read-only"
```

default case는 request access가 `full`이어야 한다. `--access other`, 값 누락과
`gemini --access read-only`는 agent process 실행 전에 오류가 나야 한다.

- [ ] **Step 5: CLI option과 help 구현**

`startOptions`에 `access string`을 추가하고 기본값을 `full`로 둔다.
`parseStartOptions`는 `--access value`와 `--access=value`를 처리하고
`codingagent.ParseAccessMode`로 검증한다. request의 `Access`에 canonical string을
넣는다. help에는 다음 두 예를 포함한다.

```text
zellij-agent agent start codex --access full -- "Implement M1"
zellij-agent agent start codex --access read-only -- "Verify M1"
```

- [ ] **Step 6: dashboard에 access 표시**

header를 `STATE  AGENT  ACCESS  PROJECT  SINCE`로 바꾸고 full은 `full`, read-only는
`read-only`로 표시한다. 기존 empty fixture는 `full`로 렌더링해 backward compatibility를
유지한다. width 계산과 view snapshot 기대값을 갱신한다.

- [ ] **Step 7: CLI/dashboard 문서와 테스트**

`docs/zellij-agent-quickstart.md`에 read-only command, Codex-only 제한, 종료 시 managed
pane cleanup을 기록한다.

Run: `gofmt -w internal/cli/agent internal/agentdashboard`

Run: `go test ./internal/cli/agent ./internal/agentdashboard ./internal/transport -v`

Expected: PASS.

- [ ] **Step 8: 연결 기능 커밋**

```bash
git add internal/transport internal/cli/agent internal/agentdashboard docs/zellij-agent-quickstart.md
git commit -m "feat: 읽기 전용 에이전트 실행 경로 연결"
```

---

### Task 4: 새 스킬의 상태·증거 기반 구성 생성

**Files:**
- Create: `.agents/skills/loop-project-zellij-runner/SKILL.md`
- Create: `.agents/skills/loop-project-zellij-runner/references/runtime-contracts.md`
- Create: `.agents/skills/loop-project-zellij-runner/references/execution-logging.md`
- Create: `.agents/skills/loop-project-zellij-runner/scripts/__init__.py`
- Create: `.agents/skills/loop-project-zellij-runner/scripts/append_checkpoint.py`
- Create: `.agents/skills/loop-project-zellij-runner/scripts/validate_runtime.py`
- Create: `.agents/skills/loop-project-zellij-runner/tests/__init__.py`
- Create: `.agents/skills/loop-project-zellij-runner/tests/fixtures.py`
- Create: `.agents/skills/loop-project-zellij-runner/tests/test_append_checkpoint.py`
- Create: `.agents/skills/loop-project-zellij-runner/tests/test_validate_runtime.py`
- Create: `.agents/skills/loop-project-zellij-runner/tests/test_contract_parity.py`

**Interfaces:**
- Consumes: authoritative source `/Users/in05908_mac/.agents/skills/loop-project-runner`
- Produces: new canonical skill source rooted at `.agents/skills/loop-project-zellij-runner`

- [ ] **Step 1: validator와 logging parity 테스트 작성**

`test_contract_parity.py`는 source와 new skill에서 다음 파일의 SHA-256이 같음을
검사한다.

```python
UNCHANGED_FILES = (
    "references/runtime-contracts.md",
    "references/execution-logging.md",
    "scripts/append_checkpoint.py",
    "scripts/validate_runtime.py",
)
```

이 목록은 official state vocabulary, append transaction과 validator 의미가 visible
dispatch 때문에 바뀌지 않도록 고정한다.

- [ ] **Step 2: parity 테스트 실패 확인**

Run: `python3 -m unittest discover -s .agents/skills/loop-project-zellij-runner/tests -v`

Expected: 대상 파일이 없어 FAIL.

- [ ] **Step 3: unchanged contract와 script를 byte-identical하게 생성**

source의 위 네 파일과 기존 validator fixture/tests를 새 skill의 대응 경로에 정확히
복제한다. 복제 직후 hash를 검사한다.

Run:

```bash
shasum -a 256 \
  /Users/in05908_mac/.agents/skills/loop-project-runner/references/runtime-contracts.md \
  .agents/skills/loop-project-zellij-runner/references/runtime-contracts.md
shasum -a 256 \
  /Users/in05908_mac/.agents/skills/loop-project-runner/references/execution-logging.md \
  .agents/skills/loop-project-zellij-runner/references/execution-logging.md
```

Expected: 각 source/target hash pair가 동일함.

- [ ] **Step 4: SKILL.md 상태 머신 뼈대 작성**

frontmatter는 다음으로 시작한다.

```yaml
---
name: loop-project-zellij-runner
description: Use when an approved loop-engineering project must run, continue, resume, or recover through visible Codex worker and verifier panes managed by zellij-agent in Zellij, while preserving PLAN, CURRENT, checkpoint, G0, BUILD, DEBUG, VERIFY, BLOCKED, and VERIFICATION_PENDING contracts.
compatibility: Requires Codex CLI, Zellij, zellij-agent daemon, filesystem, shell, and a managed orchestrator pane; worker and verifier panes run sequentially in the orchestrator tab.
---
```

본문에는 ORCHESTRATOR가 G0/BUILD/DEBUG/VERIFY를 직접 수행하지 않는 경계, worker별 fresh
pane, verifier별 fresh read-only pane, 저장소 문서 handoff와 공식 상태 vocabulary를
명시한다. dispatch 세부 내용은 다음 task의 reference로 연결한다.

- [ ] **Step 5: 기존 validator test 실행**

Run: `python3 -m unittest discover -s .agents/skills/loop-project-zellij-runner/tests -v`

Expected: copied validator, append helper와 parity tests PASS.

- [ ] **Step 6: 상태·증거 기반 커밋**

```bash
git add .agents/skills/loop-project-zellij-runner
git commit -m "feat: Zellij 루프 실행 스킬 기반 추가"
```

---

### Task 5: Visible pane dispatch와 제어 프로토콜 완성

**Files:**
- Create: `.agents/skills/loop-project-zellij-runner/references/pane-protocol.md`
- Create: `.agents/skills/loop-project-zellij-runner/references/milestone-dispatch.md`
- Create: `.agents/skills/loop-project-zellij-runner/references/verifier-dispatch.md`
- Create: `.agents/skills/loop-project-zellij-runner/references/recovery.md`
- Create: `.agents/skills/loop-project-zellij-runner/tests/test_pane_protocol.py`
- Create: `.agents/skills/loop-project-zellij-runner/tests/test_context_efficiency_contract.py`
- Create: `.agents/skills/loop-project-zellij-runner/evals/evals.json`
- Modify: `.agents/skills/loop-project-zellij-runner/SKILL.md`

**Interfaces:**
- Consumes: `zellij-agent role loop-project-worker`, `zellij-agent role loop-project-verifier`
- Consumes: `zellij-agent ctl message|status|snapshot|events|cleanup`
- Produces: `LOOP_ASSIGNMENT`, `VERIFY_REQUEST`, `VERIFY_RESULT`, `MILESTONE_RESULT`, `ACK`

- [ ] **Step 1: pane protocol 실패 테스트 작성**

`test_pane_protocol.py`는 reference text에서 다음 필수 계약을 검사한다.

```python
required = {
    "same_tab": "same Zellij tab",
    "enter": "trailing newline",
    "message_event": "message_sent",
    "source_of_truth": "PLAN, CURRENT, and checkpoint",
    "worker_barrier": "worker write barrier",
    "read_only": "--sandbox read-only --ask-for-approval never",
    "exact_cleanup": "exact logical pane ID",
}
```

또한 verifier dispatch 문서에서 `--dangerously-bypass-approvals-and-sandbox`가 나타나면
실패하고, worker가 verifier role을 실행하는 command가 나타나도 실패한다.

- [ ] **Step 2: pane-protocol.md 작성**

다음 exact envelope를 정의한다.

```text
LOOP_SIGNAL
protocol_version: 1
signal_id: <unique stable id>
type: VERIFY_REQUEST | VERIFY_RESULT | MILESTONE_RESULT | ACK
project_id: <project id>
milestone_id: <milestone id>
run_id: <run id or NONE>
sender_pane_id: <logical pane id>
checkpoint: <path or NONE>
next_action: <one bounded action>
```

문서에는 bootstrap-ready 확인, two-step assignment, `ctl message` 자동 Enter,
`message_sent`와 snapshot 검증, signal 멱등성, disk-first recovery, exact cleanup을
구체적인 command 예와 함께 기록한다.

- [ ] **Step 3: milestone-dispatch.md 작성**

오케스트레이터가 다음 순서로 worker를 관리하게 한다.

```text
health/status/session precheck
-> current orchestrator logical ID resolve
-> same-tab host pane create
-> role bootstrap wait
-> logical worker ID resolve
-> LOOP_ASSIGNMENT send and verify
-> wait for bounded signal
-> repository bounded reload and validate
-> milestone boundary snapshot and exact cleanup
```

worker prompt에는 repository, skill path, milestone ID, observed state와 write authority만
전달한다. conversation history, 이전 worker transcript와 긴 document excerpt는 전달하지
않는다.

- [ ] **Step 4: verifier-dispatch.md 작성**

오케스트레이터가 verifier host pane을 bootstrap 상태로 만든 후 logical ID와 access
mode를 확인하고, checkpoint에 STARTED를 먼저 append한 뒤에만 assignment를 보내게
한다. FINISHED/raw output/`VERIFIER_RAW_OUTPUT_END` append와 `RUNTIME_VALID` 후 exact
cleanup한다. worker는 VERIFY 동안 code/checkpoint/PLAN/CURRENT를 쓰지 않는다.

- [ ] **Step 5: recovery.md와 SKILL.md orchestration loop 완성**

`SKILL.md`의 resolver는 다음 case를 그대로 유지한다.

```text
ACTIVE | BLOCKED | VERIFICATION_PENDING | READY | PREPARED | DRAFT | ALL_DONE
```

recovery에는 lost message, duplicate signal, STARTED-only worker/verifier, disappeared pane,
daemon health failure와 unmanaged orchestrator를 포함한다. 어떤 경우에도 inline
implementation이나 self-verification으로 fallback하지 않는다.

- [ ] **Step 6: eval prompts 작성**

`evals/evals.json`에 다음 세 test를 저장한다.

```json
{
  "skill_name": "loop-project-zellij-runner",
  "evals": [
    {
      "id": 1,
      "prompt": "승인된 READY M1 프로젝트를 visible Zellij worker와 verifier로 실행해줘.",
      "expected_output": "오케스트레이터가 worker를 만들고 VERIFY에서 별도 read-only verifier를 만든 뒤 증거가 기록된 경우에만 DONE 처리한다.",
      "files": []
    },
    {
      "id": 2,
      "prompt": "ACTIVE M2가 VERIFY에서 REJECT된 프로젝트를 계속 실행해줘.",
      "expected_output": "기존 verifier를 닫고 worker를 DEBUG로 돌려 수정한 뒤 새로운 verifier pane과 run ID를 사용한다.",
      "files": []
    },
    {
      "id": 3,
      "prompt": "verifier pane이 STARTED 기록만 남기고 사라진 루프 프로젝트를 복구해줘.",
      "expected_output": "실제 pane과 diff를 조사하고 INTERRUPTED 증거를 기록한 뒤 fresh read-only verifier로 복구한다.",
      "files": []
    }
  ]
}
```

- [ ] **Step 7: skill tests 실행**

Run: `python3 -m unittest discover -s .agents/skills/loop-project-zellij-runner/tests -v`

Expected: contract, validator, append helper와 pane protocol tests PASS.

Run: `go test ./internal/runtime -run 'TestSendMessage(RoutesWithinSameTab|RejectsDifferentTabs)' -v`

Expected: same-tab message는 trailing newline이 포함된 입력으로 전달되고 different-tab
message는 거부됨.

- [ ] **Step 8: visible runner 스킬 커밋**

```bash
git add .agents/skills/loop-project-zellij-runner
git commit -m "feat: Zellij pane 루프 실행 프로토콜 추가"
```

---

### Task 6: 전체 검증, binary 등록과 skill 설치

**Files:**
- Modify only if verification reveals an in-scope defect: files owned by Tasks 1-5
- Install: `bin/zellij-agent`
- Install: `/Users/in05908_mac/.config/custom-cli/zellij-agent`
- Install: `/Users/in05908_mac/.agents/skills/loop-project-zellij-runner/`

**Interfaces:**
- Consumes: all Tasks 1-5 outputs
- Produces: locally registered unified binary and installed skill

- [ ] **Step 1: formatting과 focused test 재실행**

Run: `gofmt -w cmd/agent-role/loopprojectagent internal/roles internal/cli/role internal/codingagent internal/transport internal/cli/agent internal/agentdashboard`

Run:

```bash
go test ./cmd/agent-role/loopprojectagent \
  ./internal/roles \
  ./internal/cli/role \
  ./internal/codingagent \
  ./internal/transport \
  ./internal/cli/agent \
  ./internal/agentdashboard -v
```

Expected: PASS.

- [ ] **Step 2: 전체 Go와 skill test 실행**

Run: `go test ./...`

Expected: PASS.

Run: `python3 -m unittest discover -s .agents/skills/loop-project-zellij-runner/tests -v`

Expected: PASS.

- [ ] **Step 3: unified binary build**

Run: `go build -o bin/zellij-agent ./cmd/zellij-agent`

Expected: exit 0 and executable `bin/zellij-agent`.

- [ ] **Step 4: custom-cli에 atomic 등록**

Run:

```bash
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
```

Run: `/Users/in05908_mac/.config/custom-cli/zellij-agent agent start --help`

Expected: help에 `--access`와 `read-only`가 나타남.

- [ ] **Step 5: canonical skill을 사용자 skill 경로에 설치**

기존 target이 없음을 확인한다. 존재하면 source/target diff를 먼저 보고 이번 작업이
만든 target일 때만 교체한다. 새 설치는 temp sibling에 복제한 뒤 directory rename으로
완료한다.

```text
source: /Users/in05908_mac/zellij-with-codeagent/.agents/skills/loop-project-zellij-runner
target: /Users/in05908_mac/.agents/skills/loop-project-zellij-runner
temporary target: /Users/in05908_mac/.agents/skills/.loop-project-zellij-runner.new
```

Run: source와 target의 `find ... -type f -exec shasum -a 256` 정렬 결과 비교.

Expected: 모든 installed file hash가 canonical source와 동일함.

- [ ] **Step 6: bounded Zellij smoke precheck**

Run:

```bash
zellij-agent ctl health
zellij list-sessions
zellij-agent ctl status
```

Expected: daemon healthy, 현재 active session 존재, 현재 orchestrator physical pane에
대응하는 logical managed agent가 정확히 하나 존재.

조건이 충족되지 않으면 official repository state를 변경하지 않고 smoke를 건너뛴
이유를 최종 보고한다.

- [ ] **Step 7: task-owned pane smoke**

active session에서 worker role pane 하나를 생성하고 bootstrap/assignment message의
`message_sent` 이벤트와 snapshot을 확인한다. 종료 후 exact worker logical ID만 cleanup한다.

verifier role pane을 생성하고 runtime command에 다음 인자만 있는지 확인한다.

```text
--sandbox read-only --ask-for-approval never
```

verifier에게 repository write 시도를 요청하고 sandbox failure를 snapshot으로 확인한다.
그 뒤 exact verifier logical ID만 cleanup하고 status에서 두 task-owned ID가 사라졌는지
검증한다. 기존 관리 pane은 종료하지 않는다.

- [ ] **Step 8: 최종 diff와 문서 검증**

Run:

```bash
git status --short
git diff --check
git log -6 --oneline
./bin/zellij-agent role roles | rg 'loop-project-(worker|verifier)'
rg -n 'loop-project-(worker|verifier)' /Users/in05908_mac/.config/pi/docs/agent-roles.md
```

Expected: 의도한 파일만 변경, whitespace 오류 없음, 두 역할과 한국어 커밋 확인.

검증 중 수정이 필요했다면 관련 focused/full tests를 다시 실행하고 다음 메시지로
커밋한다.

```bash
git commit -m "fix: Zellij 루프 실행 검증 문제 수정"
```
