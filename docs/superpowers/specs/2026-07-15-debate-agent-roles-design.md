# Debate Agent Roles Design

## Objective

Add three independently executable repository roles that wrap the installed `agy`, Cursor `agent`, and `codex` CLIs with fixed debate responsibilities:

- `debate-proposer`: proposal and exploration, backed by `agy`
- `debate-critic`: criticism and red-team analysis, backed by Cursor `agent`
- `debate-judge`: adjudication and final design, backed by `codex`

This change establishes a stable role boundary before refactoring `debate-background`. It does not change planner output or the existing debate implementation.

## CLI Contract

Each role is available through both unified role entrypoints:

```bash
zellij-agent role debate-proposer [options] <path> [prompt...]
zellij-agent role debate-critic [options] <path> [prompt...]
zellij-agent role debate-judge [options] <path> [prompt...]

agent-role debate-proposer [options] <path> [prompt...]
agent-role debate-critic [options] <path> [prompt...]
agent-role debate-judge [options] <path> [prompt...]
```

`<path>` is required and may name either a file or directory inside a Git repository. The role resolves it to the repository root and runs the provider CLI there.

The task prompt is formed from all remaining positional arguments joined with spaces. If no prompt argument is present, the role reads the task prompt from stdin. An empty prompt is a validation error.

All roles accept:

```text
--output-format text|json
```

`text` is the default and writes only the normalized final response. `json` writes one JSON object using the common result schema. Provider progress and diagnostics do not leak into stdout; failures are reported on stderr and by a non-zero exit code.

## Common Result Schema

Successful JSON output uses this stable envelope:

```json
{
  "schema_version": "debate-role/v1",
  "role": "debate-proposer",
  "engine": "agy",
  "status": "success",
  "content": "normalized final response"
}
```

The fields are:

- `schema_version`: always `debate-role/v1`
- `role`: one of `debate-proposer`, `debate-critic`, or `debate-judge`
- `engine`: one of `agy`, `agent`, or `codex`
- `status`: `success` for an emitted result
- `content`: provider-independent final response text

The first version intentionally excludes token counts, session identifiers, timing, and provider-specific metadata because the three CLIs do not expose those fields consistently. Failure output is not encoded as a success-shaped JSON object: the role preserves a non-zero process status and writes a concise error to stderr.

## Architecture

### Role catalog and dispatch

`internal/roles/roles.go` gains three role constants and user-facing `RoleSpec` entries. `internal/cli/role/role.go` dispatches each name to its package under `cmd/agent-role/`.

The role packages are:

- `cmd/agent-role/debateproposer`
- `cmd/agent-role/debatecritic`
- `cmd/agent-role/debatejudge`

Each package exposes `Run(args []string) int` and supplies only its role name, engine name, embedded prompt, and provider command adapter to a shared runner.

### Shared runner

A focused shared package under `internal/debaterole` owns behavior common to all providers:

- argument parsing and validation
- repository-root resolution
- stdin fallback
- system-prompt and task-prompt composition
- child-process execution and exit-code preservation
- text and common-JSON rendering

Provider adapters remain separate because each CLI has a different invocation and output contract. The shared runner consumes a provider interface that prepares and executes one non-interactive request and returns normalized response text.

### System prompts

Each role package stores its approved Korean system prompt in a separate embedded text file. Keeping prompts out of Go string literals makes them directly reviewable and editable without escaping changes.

The composed provider input has two explicit sections:

```text
<<<SYSTEM_ROLE_BEGIN>>>
<approved role prompt>
<<<SYSTEM_ROLE_END>>>

<<<DEBATE_INPUT_BEGIN>>>
<caller-supplied task prompt>
<<<DEBATE_INPUT_END>>>
```

These markers make the role instruction and debate input unambiguous even though the provider CLIs do not all expose a dedicated system-message option.

## Provider Behavior

### Proposer: `agy`

The proposer runs `agy` non-interactively in planning mode:

```bash
agy --mode plan --print <composed-prompt>
```

The adapter captures clean print-mode stdout as the final content. It does not depend on `agy`'s hidden JSON flags, keeping the role compatible with the documented print contract.

The embedded system prompt is:

```text
당신은 토론의 제안자이자 탐색자다.

주어진 문제를 독립적으로 분석하고 실행 가능한 해결안을 제시하라.

요구사항:

1. 문제의 목표와 제약조건을 먼저 정리한다.
2. 숨겨진 전제나 불확실한 조건을 식별한다.
3. 서로 성격이 다른 해결안 2~3개를 제시한다.
4. 각 해결안의 장점, 단점, 비용, 위험을 비교한다.
5. 마지막에는 가장 추천하는 하나의 초안을 선택한다.
6. 근거가 부족한 내용은 사실처럼 단정하지 말고 가정으로 표시한다.

다른 에이전트가 반박할 수 있도록 판단 근거와 취약점을 숨기지 말라.

출력 형식:

* 문제 정의
* 주요 전제
* 후보안
* 비교
* 최초 권고안
* 불확실한 부분
```

### Critic: Cursor `agent`

The critic runs Cursor Agent non-interactively in read-only question-and-answer mode and requests its single-object JSON output:

```bash
agent --print --mode ask --output-format json --trust <composed-prompt>
```

The adapter decodes the native result object and extracts its `result` string. A non-success result, malformed JSON, or missing result text is an adapter failure.

The embedded system prompt is:

```text
당신은 토론의 비판자이자 레드팀이다.

제안자의 결론을 지지하거나 요약하는 것이 목적이 아니다. 제안이 실제 환경에서 실패할 수 있는 이유를 찾아내는 것이 목적이다.

다음 관점에서 검토하라:

1. 잘못되었거나 검증되지 않은 전제
2. 논리적 비약과 내부 모순
3. 누락된 요구사항과 이해관계자
4. 예외 상황과 실패 시나리오
5. 보안, 비용, 운영, 유지보수 위험
6. 현실적으로 실행하기 어려운 부분
7. 더 단순하거나 효과적인 대안

규칙:

* 반박에는 반드시 이유나 구체적인 반례를 붙인다.
* 표현이나 문체가 아니라 내용과 의사결정을 검토한다.
* 억지로 반대하지 않는다.
* 타당한 부분은 인정하되 검증이 필요한 부분과 구분한다.
* 치명적 문제와 사소한 문제를 분리한다.
* 가능하면 반박에 대응하는 수정안도 제시한다.

출력 형식:

* 제안에서 타당한 부분
* 치명적인 문제
* 중요한 누락
* 실패 시나리오
* 반례
* 수정 제안
* 제안자에게 묻고 싶은 핵심 질문
```

### Judge: `codex`

The judge runs Codex non-interactively with a read-only sandbox and no interactive approval prompts:

```bash
codex exec --sandbox read-only --ask-for-approval never --cd <repository-root> -
```

The composed prompt is delivered on stdin. Codex progress remains on stderr while final stdout becomes normalized content.

The embedded system prompt is:

```text
당신은 토론의 심판이자 최종 설계자다.

제안자의 최초안과 비판자의 반박을 독립적으로 평가하여 최종안을 작성하라.

중요한 원칙:

1. 다수결이나 표현의 자신감으로 판단하지 않는다.
2. 구체적인 근거, 논리적 일관성, 실행 가능성을 기준으로 판단한다.
3. 제안자와 비판자 모두 틀릴 수 있다고 가정한다.
4. 사실, 추론, 가정, 의견을 구분한다.
5. 비판자의 지적을 무조건 반영하지 않는다.
6. 핵심 정보가 부족하더라도 가능한 범위에서 조건부 결론을 내린다.
7. 단순히 두 답변을 요약하지 말고 개선된 최종안을 새로 작성한다.

평가 기준:

* 문제 적합성
* 근거의 품질
* 논리적 일관성
* 실행 가능성
* 비용과 복잡도
* 실패 위험
* 확장성과 유지보수성

출력 형식:

1. 핵심 쟁점
2. 제안자 주장 평가
3. 비판자 주장 평가
4. 채택한 주장과 기각한 주장
5. 최종 권고안
6. 실행 단계
7. 남아 있는 위험과 검증 방법
8. 결론의 신뢰도: 높음 / 중간 / 낮음
```

## Error Handling

- Missing role path, inaccessible paths, paths outside Git repositories, invalid output formats, and empty prompts return a usage or validation error without launching a provider.
- Missing provider executables name the unavailable binary.
- Child exit codes are preserved when available.
- Provider stderr is retained for failure diagnostics but is not mixed into successful stdout.
- Cursor JSON decoding errors identify the invalid provider response without printing untrusted raw output into the common JSON envelope.
- Empty successful stdout is treated as an error for all providers.

## Testing

Tests follow the repository's standard `testing` package and use fake executables on a temporary `PATH`.

Coverage includes:

- all three roles appear in the role catalog with exact usage and arguments
- CLI dispatch reaches all three packages
- file and directory paths resolve to the Git repository root
- positional and stdin prompts use the same composition markers
- each provider receives its required non-interactive and read-only flags
- the Cursor adapter extracts `result` from native JSON
- `text` prints only normalized content
- `json` emits the exact `debate-role/v1` envelope
- invalid usage, empty prompts, malformed Cursor JSON, missing executables, and child failures return non-zero
- child exit codes are preserved

Verification includes focused package tests, `go test ./...`, building both role-capable binaries, listing the role catalog, and updating the external role summary required by the repository's role workflow.

## Compatibility and Deferred Work

Existing roles and their CLI contracts remain unchanged. The new roles are manual commands only; planner-generated panes are not modified.

`debate-background` continues to invoke providers directly in this change. A later refactor can replace those direct commands with the three stable role CLIs and consume `debate-role/v1` without reimplementing provider-specific parsing.
