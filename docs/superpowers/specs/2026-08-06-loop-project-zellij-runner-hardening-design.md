# Loop Project Zellij Runner 보안·복구 강화 설계

## 배경과 목표

최종 branch review와 live smoke에서 두 가지 load-bearing 결함이 확인됐다.

1. read-only verifier sandbox는 `/tmp/agentd.sock` 접근을 차단하므로 verifier가 필수 `ctl message` 결과 signal을 보낼 수 없다.
2. read-only managed command가 caller-controlled extra arguments를 안전 플래그 뒤에 그대로 붙여 permission-bypass option을 다시 주입할 수 있다.

이번 강화 작업은 read-only 격리를 약화하지 않고 verifier 결과를 host가 수집하게 만들며, read-only 입력을 typed prompt로 제한한다. 동시에 `VERIFICATION_PENDING` 재진입, daemon capability upgrade, pane identity 재사용 안전성, 문서·테스트의 남은 merge blocker를 해결한다.

## 검토한 접근

### 선택: host-side verifier relay와 typed prompt

Verifier는 daemon socket에 접근하지 않고 pane stdout에 하나의 구조화된 결과 block을 출력한다. 오케스트레이터는 host 측 `ctl snapshot`을 bounded polling하여 block을 수집·검증하고 checkpoint에 기록한다. read-only agent에는 option passthrough 대신 하나의 typed positional prompt만 전달한다.

이 방식은 read-only sandbox를 그대로 유지하고 기존 orchestrator/worker 메시지 경계를 보존한다. 결과 수집 책임도 evidence를 기록하는 오케스트레이터에 모인다.

### 기각: verifier sandbox에서 daemon socket 허용

Socket 접근을 허용하면 verifier가 message 이외의 runtime mutation API에도 접근할 수 있어 read-only 격리의 의미가 약해진다. API별 별도 proxy를 만들더라도 이번 기능보다 큰 권한 시스템이 필요하므로 선택하지 않는다.

### 기각: verifier를 없애고 오케스트레이터가 직접 검증

이는 fresh read-only verifier와 역할 분리라는 핵심 계약을 위반한다. Worker self-verification과 같은 신뢰 경계 문제도 다시 만든다.

## 아키텍처

### 1. Typed read-only launch contract

Domain request는 다음 두 입력을 구분한다.

- `ExtraArgs []string`: full-access profile의 기존 호환 경로
- `Prompt string`: read-only Codex의 유일한 caller-controlled command payload

Read-only request는 `ExtraArgs`가 비어 있어야 한다. CLI에서 `--access read-only` 뒤의 `--` payload는 0개 또는 정확히 1개의 positional prompt만 허용하며, `-`로 시작하는 값은 거부한다. Transport도 `prompt`와 `arguments`를 분리하고 service가 runtime/register/monitor side effect 전에 다음을 검증한다.

- Codex 이외 kind 거부
- read-only `arguments` 거부
- option-like prompt 거부
- permission bypass, config override, additional writable directory를 command에 추가할 경로 부재

`BuildManagedCommand`는 read-only에서 고정 안전 flags와 optional prompt만 조합한다. Full-access의 기존 `BuildCommand` 및 arbitrary arguments 동작은 유지한다.

### 2. Backward-compatible daemon capability negotiation

Default/explicit full access는 transport JSON의 `access`를 생략한다. 빈 값은 server domain에서 계속 canonical `full`로 정규화된다. 따라서 access field를 모르는 구 daemon도 기존 full start를 처리할 수 있다.

Health response는 capability 문자열 목록을 제공하며 새 daemon은 `agent_access_read_only_v1`을 광고한다. CLI는 read-only 요청 전에 health를 조회한다.

- capability 존재: `access: read-only`와 typed `prompt`를 전송
- capability 부재: agent start request를 보내지 않고 "installed CLI and running daemon differ; drain/restart daemon" 오류 반환
- full: capability 조회 없이 legacy-compatible request 전송

Quickstart와 배포 절차는 daemon이 memory-backed registry를 소유한다는 사실, drain/restart 필요 조건, restart 뒤 기존 logical ID가 무효라는 점을 명시한다.

### 3. Host-side verifier result relay

Assignment delivery는 기존과 같이 orchestrator가 verifier logical ID로 `ctl message`를 보내고 `message_sent` 및 target snapshot을 확인한다. 결과 방향만 stdout relay로 바꾼다.

Verifier는 정확히 한 번 다음 fenced payload를 pane에 출력한다.

```text
LOOP_VERIFY_RESULT_BEGIN
protocol_version: 1
project_id: <project id>
milestone_id: <milestone id>
run_id: <run id>
verdict: APPROVE | REJECT | UNCERTAIN
next_action: <one bounded action>
LOOP_VERIFY_RESULT_END
```

오케스트레이터는 bounded snapshot polling으로 end marker를 기다린다. 정확히 한 block만 허용하며 required key, assignment identity, run ID와 verdict vocabulary를 검증한다. Pane snapshot의 logical ID가 sender identity다. Verifier bootstrap에서는 `ctl message` 지시를 제거하고 stdout block만 결과 채널이라고 명시한다.

검증 후 오케스트레이터가 동일 snapshot의 complete five-section raw result와 marker를 capture하고 다음 순서로 기록한다.

1. matching `AGENT_RUN_FINISHED`
2. complete raw five-section output
3. exactly one `VERIFIER_RAW_OUTPUT_END`
4. `APPEND_OK`
5. `validate_runtime.py`의 `RUNTIME_VALID`
6. worker에게 host-side bounded verdict signal 전달
7. exact verifier cleanup

Marker가 누락·중복·malformed이거나 pane이 사라지면 verdict를 제조하지 않고 observed `INTERRUPTED`/`TIMED_OUT` evidence만 기록한다.

### 4. `VERIFICATION_PENDING` 실행 경로

`ACTIVE/VERIFY`는 owning worker의 `VERIFY_REQUEST`에서 시작한다. `VERIFICATION_PENDING`은 이전 verifier가 UNCERTAIN을 반환했거나 verifier 실행이 중단됐지만 durable verification inputs가 준비된 상태다.

재진입 시 오케스트레이터는 worker signal을 요구하지 않는다. PLAN, CURRENT, checkpoint, diff와 required inputs를 reload하고 다음을 만족할 때만 fresh verifier를 만든다.

- milestone identity 일치
- open worker/verifier run 없음
- worker write barrier 유지
- verification input bundle 존재
- pre-run `RUNTIME_VALID`

입력이 부족하면 상태를 보존하고 필요한 external/human action을 보고한다. Fresh verifier verdict는 APPROVE면 DONE eligibility, REJECT면 ACTIVE/DEBUG, UNCERTAIN이면 새 `VERIFICATION_PENDING` evidence로 전이한다.

### 5. Restart-safe pane ownership

Logical ID는 daemon restart 뒤 재사용될 수 있으므로 cleanup identity가 아니다. 각 pane claim/create에 opaque random `ownership_token`을 생성해 runtime record, transport status와 creation/claim response에 보존한다.

Cleanup은 task-owned pane에 대해 `(logical_id, ownership_token)` compare-and-close를 사용한다. Token mismatch는 close하지 않고 skipped conflict로 반환한다. Skill은 추가로 session, tab, physical pane ID, role, project/run marker를 inventory와 대조하지만 token이 최종 mutation guard다.

Restart 전 기록에 token이 없거나 현재 token과 다르면 old logical ID를 cleanup하지 않는다. Pane은 ambiguous orphan으로 보고하고 fresh role을 만든다. Broad role/task/session cleanup과 raw physical cleanup은 계속 금지한다.

Legacy CLI의 token 없는 cleanup은 일반 운영 호환을 위해 유지하되 새 loop skill은 반드시 token-qualified cleanup만 사용한다. Help와 tests가 이 차이를 명시한다.

### 6. 문서 compatibility와 남은 merge blocker

- Byte-identical `runtime-contracts.md`가 가리키는 `references/agent-dispatch.md` compatibility shim을 추가한다. Shim은 milestone/verifier dispatch 문서로만 연결한다.
- `agent start` help의 bypass 설명은 full access에만 적용된다고 수정한다.
- Worker 문서의 verifier-role command 금지 test는 bash fenced blocks를 추출해 `$`, backtick, `run` 접두를 포함한 실행형 표현을 거부한다.
- 모든 command 예시의 logical ID는 `<orchestrator-logical-id>`, `<worker-logical-id>`, `<verifier-logical-id>`와 matching ownership token placeholder를 사용한다.

## 오류 처리와 안전성

- Read-only hostile argument는 daemon call 전 CLI와 service 양쪽에서 거부한다.
- Capability mismatch는 stale daemon을 자동 restart하지 않는다. 기존 pane drain과 명시적 maintenance가 필요하다.
- Verifier result marker 오류는 approval evidence가 아니다.
- Cleanup token mismatch는 unrelated pane을 보존하는 성공적인 safety outcome이며 retry로 범위를 넓히지 않는다.
- Orchestrator만 checkpoint를 append하고 PLAN/CURRENT를 전이한다. Verifier는 repository와 daemon socket에 write authority가 없다.

## 테스트 전략

### Go RED→GREEN

- read-only bypass/config/add-dir/다중 arg/option-like prompt가 side effect 전에 거부됨
- safe zero/one prompt가 exact argv로 생성됨
- full request JSON은 access를 생략하고 legacy server fixture와 호환됨
- read-only는 capability 없는 daemon에서 StartAgent call 없이 실패함
- health capability round-trip
- cleanup ownership token match는 close, mismatch는 skip하고 physical pane 보존
- status/claim/cleanup transport가 token을 보존함

### Skill RED→GREEN

- verifier bootstrap/reference에 outbound `ctl message` 결과 지시가 없음
- exact stdout marker block과 host relay evidence order
- malformed/duplicate/missing marker recovery
- `VERIFICATION_PENDING` fresh verifier entry와 verdict transition eval
- recovery가 stale logical ID만으로 cleanup하지 않고 token mismatch를 orphan으로 보존
- compatibility link 존재
- fenced worker command test와 logical ID/token placeholder exactness

### 전체와 live 검증

- focused Go packages, `go test ./...`
- full skill unittest와 JSON eval validation
- rebuilt binary atomic registration 후 daemon capability 확인
- same-tab worker message smoke
- read-only verifier write denial, stdout result marker capture, FINISHED/raw/`RUNTIME_VALID`, worker relay와 token-qualified exact cleanup의 전체 cycle
- probe/source/PLAN/CURRENT/checkpoint와 task-owned pane cleanup 상태 확인

## 완료 기준

- 두 Critical finding과 모든 Important finding이 regression test로 재현된 뒤 GREEN이다.
- 최종 verifier evidence lifecycle이 read-only sandbox에서 daemon socket 접근 없이 완료된다.
- Hostile read-only arguments로 permission bypass를 주입할 수 없다.
- Restart 후 logical ID 재사용이 unrelated cleanup으로 이어지지 않는다.
- 최종 scoped review가 모든 finding을 ADDRESSED로 판정하고 새 Critical/Important breakage가 없다.
