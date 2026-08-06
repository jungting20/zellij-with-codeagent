# Zellij 기반 Loop Project Runner 설계

## 목표

새 Agent Skill `loop-project-zellij-runner`를 만든다. 이 스킬은 기존
`loop-project-runner`의 승인, 상태 머신, 마일스톤 격리, append-only 체크포인트,
독립 검증과 복구 계약을 유지하면서 모든 실행 에이전트를 사용자가 볼 수 있는
Zellij pane에서 실행한다.

오케스트레이터는 마일스톤 worker와 verifier를 모두 직접 생성, 추적, 메시징,
검증, 종료한다. worker는 verifier를 직접 생성하지 않는다. 첫 버전은 Codex만
지원하며 verifier는 프롬프트 지시가 아니라 Codex sandbox로 실제 읽기 전용을
강제한다.

## 확정된 사용자 흐름

```text
ORCHESTRATOR pane
  -> fresh M1 MILESTONE_WORKER pane
       -> VERIFY_REQUEST message
  -> fresh read-only VERIFICATION_AGENT pane
       -> VERIFY_RESULT message
  -> recorded verdict를 M1 worker에 전달
       -> APPROVE: DONE
       -> REJECT: DEBUG 후 새 verifier 요청
       -> UNCERTAIN: VERIFICATION_PENDING
  -> verifier pane 종료
  -> M1 경계에서 worker pane 종료
  -> 저장소 상태 재검증
  -> fresh M2 MILESTONE_WORKER pane
```

모든 pane은 오케스트레이터와 같은 Zellij tab에 생성한다. `ctl message`의
same-tab 계약을 만족하고 사용자가 한 화면에서 오케스트레이터, worker, verifier를
볼 수 있게 하기 위해서다.

## 범위

다음을 포함한다.

- Codex 전용 `loop-project-zellij-runner` 스킬
- worker와 verifier를 위한 기본 agent role
- `zellij-agent agent start codex`의 명시적 access mode
- Codex verifier의 실제 read-only sandbox
- 같은 tab의 관리 pane 사이 `ctl message` 기반 제어 신호
- PLAN, CURRENT, 체크포인트를 기준으로 하는 재진입과 메시지 유실 복구
- verifier와 worker의 정확한 pane 종료 및 사후 검증
- 기존 loop runtime validator와 append-only evidence 계약의 동등성 검증

다음은 첫 버전에서 제외한다.

- Claude, Gemini, Cursor 지원
- 여러 마일스톤의 병렬 실행
- daemon 재시작 후 실행 중 Codex 프로세스 자동 재연결
- 일반적인 분산 작업 큐 또는 범용 workflow engine
- 기존 `loop-project-runner`의 동작 변경
- verifier 결과를 근거로 한 인간 승인 추론

## 접근 방식 비교

### 1. 스킬 프롬프트만 변경

기존 `agent start codex`를 그대로 사용하고 verifier에게 파일을 수정하지 말라고
지시한다. 구현량은 가장 적지만 현재 Codex profile이
`--dangerously-bypass-approvals-and-sandbox`를 자동 추가하므로 읽기 전용을 기술적으로
보장하지 못한다. 기존 runner와 같은 독립 검증 계약을 충족하지 않는다.

### 2. 기존 계약을 복제하고 Zellij dispatch로 교체

기존 runner의 runtime, logging, validator 계약과 스크립트를 새 스킬에 포함하고
worker/verifier dispatch 부분만 Zellij pane 프로토콜로 바꾼다. 동시에
`zellij-agent`에 read-only Codex access mode와 역할을 추가한다.

원본과 새 스킬이 시간이 지나며 달라질 위험이 있지만, 원본 스킬을 변경하지 않고
새 실행 모델을 독립적으로 검증할 수 있다. 계약 파일의 parity 검사로 의도하지 않은
차이를 탐지한다.

### 3. 공통 loop engine으로 원본 스킬까지 재구조화

상태 머신과 증거 계약을 공통 패키지로 추출하고 hidden sub-agent와 Zellij pane을
dispatch adapter로 분리한다. 장기 중복은 가장 적지만 기존 runner의 신뢰 경계를
동시에 변경하고 작업 범위가 크게 늘어난다.

첫 버전은 **2번**을 선택한다. read-only 보장을 지키면서 기존 runner에 회귀 위험을
주지 않는 최소 범위다.

## 기본 역할

저장소의 기능 추가 규칙에 따라 구현은 기본 역할부터 시작한다.

### `loop-project-worker`

```text
zellij-agent role loop-project-worker \
  --repository PATH \
  --runner-skill PATH \
  --orchestrator-pane PANE_ID
```

- 현재 pane을 `zellij-agent agent start codex`로 등록한다.
- Codex를 write-capable access mode로 실행한다.
- 고정 worker bootstrap contract를 최초 prompt로 전달한다.
- assignment를 받기 전에는 G0, BUILD, DEBUG 또는 상태 변경을 시작하지 않는다.
- verifier를 생성하지 않고 `VERIFY_REQUEST`만 보낸다.

### `loop-project-verifier`

```text
zellij-agent role loop-project-verifier \
  --repository PATH \
  --runner-skill PATH \
  --orchestrator-pane PANE_ID
```

- 현재 pane을 read-only Codex agent로 등록한다.
- 고정 verifier bootstrap contract를 최초 prompt로 전달한다.
- `VERIFY_ASSIGNMENT`를 받기 전에는 검증을 시작하지 않는다.
- 저장소 수정, Git mutation, 상태 문서 변경, 수정 구현을 금지한다.
- 원본 검증 결과를 pane에 출력하고 짧은 `VERIFY_RESULT` 신호만 보낸다.

두 역할의 인자 검증과 Codex command 구성은 role package가 소유한다. role CLI
dispatcher는 역할 선택만 수행한다. 역할은 planner 기본 출력에는 추가하지 않는다.

## Codex access mode

현재 coding-agent profile은 모든 Codex 실행에 permission bypass 인자를 추가한다.
요청과 저장된 agent record에 다음 access mode를 도입한다.

```text
FULL_ACCESS
READ_ONLY
```

기존 호출의 기본값은 `FULL_ACCESS`로 유지하여 호환성을 보존한다.

```text
FULL_ACCESS command:
  codex --dangerously-bypass-approvals-and-sandbox ...

READ_ONLY command:
  codex --sandbox read-only --ask-for-approval never ...
```

두 모드를 한 command에 함께 넣지 않는다. `READ_ONLY`는 첫 버전에서 Codex에만
허용하며 다른 agent kind와 함께 요청하면 명시적으로 거부한다.

CLI는 다음 형태를 제공한다.

```text
zellij-agent agent start codex --access full -- ...
zellij-agent agent start codex --access read-only -- ...
```

runtime status와 agent 목록에서 access mode와 실제 command를 확인할 수 있어야 한다.
오케스트레이터는 verifier STARTED evidence를 append하기 전에 생성된 pane이
`READ_ONLY`인지 검사한다.

## Pane 생성과 등록

`agent start`는 실행된 현재 pane을 claim하는 구조이므로, 오케스트레이터는 역할을
호스트할 pane만 Zellij에 생성한다.

```text
zellij action new-pane --name loop-worker-M1 --cwd REPOSITORY -- \
  zellij-agent role loop-project-worker ...
```

이는 관리 agent를 현재 pane에서 시작하기 위한 허용된 host-pane 생성 경로다.
pane 등록 이후의 input, message, snapshot, status, focus와 cleanup은 모두
`zellij-agent` runtime boundary를 사용한다.

오케스트레이터는 생성 명령이 반환한 physical pane ID를 진단용으로만 사용한다.
`ctl status`에서 physical ID에 대응하는 logical pane/agent ID를 찾고 이후 모든
제어에는 logical ID를 사용한다.

## 두 단계 bootstrap

logical agent ID는 `agent start` 등록 후 결정되므로 최초 prompt에 완전한 assignment를
넣지 않는다.

### 1단계: 고정 bootstrap

role은 Codex를 다음 의미의 최초 prompt로 시작한다.

```text
당신은 지정된 LOOP 역할이다.
role contract를 디스크에서 읽고 LOOP_ASSIGNMENT를 기다려라.
assignment 전에는 저장소 상태를 변경하지 마라.
후속 신호는 제공된 logical pane ID로 ctl message를 사용하라.
```

### 2단계: 동적 assignment

오케스트레이터가 runtime에서 logical ID를 확인한 뒤 `ctl message`로 전달한다.

```text
LOOP_ASSIGNMENT
protocol_version: 1
project_id: <id>
milestone_id: M1
orchestrator_pane_id: agent-2
worker_pane_id: agent-7
repository: /absolute/project
runner_skill: /absolute/skill/SKILL.md
observed_state: READY
```

메시지 전달 전에는 agent state 또는 snapshot으로 bootstrap prompt 처리가 끝났는지
확인한다. `ctl message`는 trailing newline을 자동 추가하여 paste 뒤 Enter를
전송한다. `message_sent` 이벤트와 대상 snapshot으로 제출 여부를 검증한다.

## 제어 메시지 계약

메시지는 pane을 깨우고 다음 동작 위치를 알려주는 bounded control envelope다.
공식 상태나 검증 증거를 메시지에만 저장하지 않는다.

```text
LOOP_SIGNAL
protocol_version: 1
signal_id: <unique-id>
type: VERIFY_REQUEST | VERIFY_RESULT | MILESTONE_RESULT | ACK
project_id: <id>
milestone_id: <id>
run_id: <id-or-NONE>
sender_pane_id: <logical-id>
checkpoint: <path-or-NONE>
next_action: <bounded action>
```

원본 verifier 출력, 전체 command output, prompt, 구현 추론은 control envelope에
넣지 않는다. `signal_id`와 `run_id`로 중복을 멱등 처리한다. 메시지가 유실되면
PLAN, CURRENT, 체크포인트와 pane 상태에서 다음 동작을 복구한다.

## 상태와 증거의 소유권

| 주체 | 코드 | 체크포인트 | PLAN/CURRENT |
|---|---|---|---|
| ORCHESTRATOR | 수정 금지 | verifier dispatch와 raw result | reload, validation, routing |
| MILESTONE_WORKER | 마일스톤 범위 내 수정 | G0, BUILD, DEBUG, completion | activation, stage, verdict transition |
| VERIFICATION_AGENT | 수정 금지 | 수정 금지 | 수정 금지 |

worker와 오케스트레이터가 체크포인트를 동시에 append하지 않도록 VERIFY handshake를
쓰기 barrier로 사용한다.

## 공식 상태 프로토콜

### Worker activation과 구현

worker는 assignment를 받은 후 runtime을 검증한다. `READY`면 기존 runner의
idempotent activation 순서대로 체크포인트 snapshot, PLAN `ACTIVE`, CURRENT `G0`를
적용한다. `ACTIVE` 또는 `VERIFICATION_PENDING`은 재활성화하지 않는다.

G0, BUILD, DEBUG safe unit마다 worker가 다음을 수행한다.

1. `MILESTONE_WORKER` STARTED event를 guarded append한다.
2. `APPEND_OK` 뒤 해당 safe unit만 실행한다.
3. command, exit code, observed result와 changed files를 FINISHED로 append한다.
4. `RUNTIME_VALID`를 확인한다.
5. 필요한 CURRENT stage transition을 적용하고 다시 검증한다.

VERIFY 준비가 끝나면 공식 상태는 다음을 만족해야 한다.

```text
PLAN.<M>: ACTIVE
CURRENT.project: ACTIVE
CURRENT.active_milestone: <M>
CURRENT.stage: VERIFY
checkpoint: open worker run 없음
```

worker는 이 상태를 검증한 뒤 checkpoint 쓰기를 멈추고 `VERIFY_REQUEST`를 보낸다.
verdict가 기록되어 돌아올 때까지 code, checkpoint, PLAN, CURRENT를 변경하지 않는다.

### Verifier dispatch

오케스트레이터는 `VERIFY_REQUEST`를 신뢰하지 않고 bounded reload를 수행한다.

1. PLAN, CURRENT, checkpoint target과 stage 일치 확인
2. open worker run이 없는지 확인
3. 마지막 self-check evidence bundle 확인
4. 변경 목록과 checkpoint 일치 확인
5. `validate_runtime.py`의 `RUNTIME_VALID` 확인

검증을 시작할 수 있으면 fresh verifier host pane을 만들고 bootstrap 완료를 기다린다.
logical verifier ID와 read-only access mode를 확인한 다음, 실제 assignment 전에
다음 STARTED event를 오케스트레이터가 append한다.

```text
AGENT_RUN_STARTED
- run_id: <verification-run-id>
- agent_role: VERIFICATION_AGENT
- milestone_id: <M>
- stage: VERIFY
- attempt: <N>
- started_at: <timestamp>
- input_references: <bounded paths>
- fresh_context: true
- access_mode: READ_ONLY
- code_changes: FORBIDDEN
- verifier_context_id: <logical-pane-id>
```

`APPEND_OK` 뒤에만 verifier에게 `VERIFY_ASSIGNMENT`를 보낸다. STARTED-only verifier
run이 열린 동안 worker는 계속 쓰기 barrier를 유지한다.

### Verifier execution과 결과 캡처

verifier는 bounded input package를 디스크에서 다시 읽고 지정된 read-only command를
직접 실행한다. 결과는 기존 five-section raw output contract로 pane에 출력한다.
완료 후 원본 출력 전체가 아니라 run ID와 verdict만 `VERIFY_RESULT`로 보낸다.

오케스트레이터는 다음을 증명한다.

- signal sender가 등록된 verifier logical ID와 일치
- STARTED run ID와 result run ID가 일치
- command가 read-only access mode로 실행됨
- 검증 전후 repository diff가 동일하고 changed files가 없음
- raw command, exit code, observed result가 FINISHED bundle과 일대일 대응
- raw verdict와 returned verdict가 일치

그 뒤 하나의 append payload에 matching FINISHED, complete raw output,
`VERIFIER_RAW_OUTPUT_END`를 기록한다. `APPEND_OK`와 `RUNTIME_VALID` 뒤 verifier pane을
exact logical ID로 종료하고 status에서 제거됐는지 확인한다.

verifier evidence가 완전히 기록되기 전에는 PLAN을 `DONE`으로 변경하지 않는다.

### Verdict routing

오케스트레이터는 evidence append와 runtime validation이 끝난 뒤에만 recorded verdict를
worker에 보낸다.

- `APPROVE`: worker가 동일 evidence chain을 다시 확인하고 completion transition을
  append한다. PLAN을 `DONE`, CURRENT를 no-active `DONE` handoff로 바꾸고 검증한다.
- `REJECT`: PLAN은 `ACTIVE`를 유지한다. worker가 required fix를 append하고 CURRENT를
  `DEBUG`로 전환한다. 수정 후 새 run ID와 새 verifier pane을 요청한다.
- `UNCERTAIN`: worker가 누락 정보나 권한을 append하고 PLAN과 CURRENT를
  `VERIFICATION_PENDING`으로 전환한다.

worker는 `DONE`, `BLOCKED`, `VERIFICATION_PENDING` 또는 human boundary에서
`MILESTONE_RESULT`를 보낸다. 오케스트레이터가 저장소를 독립적으로 reload하고
`RUNTIME_VALID`를 확인한 뒤 worker pane을 종료한다. successor는 반드시 새 worker
pane에서 시작한다.

## Pane 종료 정책

- verifier pane: FINISHED, raw output, boundary append와 runtime validation 후 종료
- worker pane: 담당 마일스톤 경계와 orchestrator reload validation 후 종료
- 실패한 bootstrap pane: 실제 assignment가 시작되지 않았음을 확인한 뒤 종료
- cleanup: 이번 invocation이 기록한 exact logical IDs만 사용
- session과 tab: 유지하며 자동 종료하지 않음

종료 전 full snapshot을 bounded diagnostic artifact로 캡처할 수 있지만, checkpoint의
명령·exit code·관찰 결과를 대체하지 않는다.

## 장애와 복구

- 메시지 유실: CURRENT stage, open run과 pane 상태에서 재구성하고 동일 signal을
  멱등 재전송한다.
- 중복 메시지: `signal_id`와 `run_id`가 이미 처리됐으면 상태를 다시 변경하지 않는다.
- verifier pane 소실: 실제 프로세스와 diff를 조사해 matching `INTERRUPTED` 또는
  `TIMED_OUT` FINISHED를 append하고 새 verifier를 사용한다.
- worker pane 소실: open worker run을 조사하고 완료가 증명되지 않으면
  `INTERRUPTED`로 닫은 뒤 같은 마일스톤을 fresh worker로 복구한다.
- append 실패: PLAN/CURRENT를 변경하거나 결과 메시지를 전달하지 않고 중단한다.
- verifier write 감지: verdict를 승인 근거로 사용하지 않고 failed run으로 기록한다.
- 오케스트레이터 pane이 관리 대상이 아니거나 same-tab messaging이 불가능함:
  공식 상태를 보존하고 실행을 시작하지 않는다.
- daemon health 실패: 자동 복구가 실패하면 pane을 만들지 않고 blocker를 보고한다.

## 새 스킬 구조

```text
~/.agents/skills/loop-project-zellij-runner/
├── SKILL.md
├── scripts/
│   ├── append_checkpoint.py
│   └── validate_runtime.py
└── references/
    ├── runtime-contracts.md
    ├── execution-logging.md
    ├── milestone-dispatch.md
    ├── verifier-dispatch.md
    ├── pane-protocol.md
    └── recovery.md
```

runtime과 logging 계약은 기존 runner와 의미상 동일하게 유지한다. pane dispatch와
ownership이 달라지는 문서는 독립적으로 작성한다. 테스트는 원본 계약과 복사된
계약의 의도하지 않은 차이, validator fixture 결과, control message 예제를 확인한다.

## 테스트 전략

### Role과 access mode

- role catalog에 `loop-project-worker`, `loop-project-verifier`가 포함됨
- role 인자 validation과 Codex bootstrap prompt 구성
- 기존 agent start의 기본 FULL_ACCESS command가 유지됨
- read-only Codex command에는 bypass가 없고 sandbox/approval 인자가 정확함
- Codex 외 agent의 read-only 요청이 거부됨
- transport와 agent record에서 access mode가 보존됨

### Pane와 메시지

- bootstrap 뒤 logical ID를 확인하고 assignment를 전달함
- `ctl message`가 본문 paste 뒤 Enter를 전송함
- `message_sent` 이벤트와 snapshot으로 제출을 증명함
- 다른 tab, 잘못된 logical ID와 준비 전 입력을 안전하게 거부함
- exact worker/verifier pane만 cleanup함

### 상태와 증거

- worker가 VERIFY state와 evidence를 저장한 뒤에만 요청함
- verifier STARTED가 assignment보다 먼저 append됨
- verifier 실행 중 worker checkpoint write가 없음
- FINISHED/raw output/boundary가 한 evidence chain으로 기록됨
- APPROVE만 DONE으로 전환함
- REJECT는 DEBUG, UNCERTAIN은 VERIFICATION_PENDING으로 전환함
- 중복 또는 유실 신호에서 state transition이 중복되지 않음

### 통합 검증

- `go test ./...`
- unified binary build와 atomic local registration
- 관련 skill fixture/eval 실행
- 실제 Zellij에서 worker와 verifier pane 가시성 확인
- verifier의 write 시도가 sandbox에서 실패하는지 확인
- APPROVE, REJECT, UNCERTAIN과 pane 소실 복구 smoke flow

## 성공 기준

- 사용자가 worker와 verifier의 Codex TUI를 모두 Zellij에서 볼 수 있다.
- 오케스트레이터만 두 종류의 pane을 생성한다.
- worker와 verifier는 각각 fresh pane/context를 사용한다.
- verifier는 실제 read-only sandbox에서 실행되고 저장소 변경이 없다.
- `ctl message`가 Enter까지 포함해 역할 간 control signal을 전달한다.
- 메시지가 유실되어도 PLAN, CURRENT, 체크포인트에서 실행을 복구할 수 있다.
- 동일 verifier run의 STARTED, FINISHED, raw APPROVE 없이 DONE이 될 수 없다.
- 마일스톤과 verifier 경계에서 task-owned pane이 자동 종료된다.
- 기존 `loop-project-runner`와 기존 coding-agent 기본 실행이 회귀하지 않는다.
