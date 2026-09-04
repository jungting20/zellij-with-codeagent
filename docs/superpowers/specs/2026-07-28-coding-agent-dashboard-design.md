# 코딩 에이전트 전용 대시보드 설계

## 목표

`zellij-agent`를 통해 Codex, Claude Code, Gemini, Cursor Agent를 현재 Zellij tab의 새 pane에서 실행하고, daemon이 각 에이전트의 작업 상태를 지속적으로 감지한다. 별도 TUI dashboard는 관리 중인 코딩 에이전트만 표시하며, 선택한 에이전트의 pane으로 focus를 이동한다.

첫 버전의 핵심 사용 흐름은 다음과 같다.

```text
agent start CLI
  -> 현재 tab에 coding-agent pane 생성
  -> daemon이 상태를 감지하고 저장
  -> agent dashboard가 상태 목록 표시
  -> Enter를 누르면 대상 session/pane으로 focus 이동
```

## 범위

### 포함

- 이 프로젝트를 통해 새로 실행한 코딩 에이전트만 관리
- Codex, Claude Code, Gemini, Cursor Agent 지원
- 에이전트별 권한 우회 옵션을 포함한 기본 실행 명령
- 현재 Zellij tab의 새 pane에서 실행
- daemon 소유의 `idle`, `working`, `blocked`, `unknown` 상태 감지
- 코딩 에이전트 전용 dashboard
- 선택한 agent pane으로 focus 이동
- 선택한 agent를 목록 상단에 pin/unpin
- 상태 변경 이벤트 발행

### 제외

- 기존 임의 pane에서 실행 중인 에이전트 자동 발견
- daemon 재시작 후 기존 agent 복구
- macOS 알림, 음성 알림, 외부 hook 실행
- dashboard에서 prompt 입력 또는 agent 종료
- 기존 runtime dashboard의 동작 변경

## 핵심 설계 결정

### 런타임 상태와 에이전트 상태 분리

기존 `Pane.Status`는 `starting`, `running`, `exited`, `closed`, `lost`, `error` 같은 pane 생명주기다. 코딩 에이전트의 `idle`, `working`, `blocked`, `unknown`은 별도 `AgentState`로 저장한다. 두 상태를 같은 필드에 넣지 않는다.

### daemon이 상태를 소유

dashboard가 화면을 직접 분석하지 않는다. daemon 내부의 `AgentMonitor`가 상태를 계산하고 저장한다. dashboard가 종료되어도 감지는 계속되며, 향후 알림 기능은 상태 변경 이벤트의 독립 구독자로 추가할 수 있다.

### 전용 dashboard 분리

기존 runtime dashboard는 모든 관리 pane의 생명주기와 출력 진단을 위한 도구로 유지한다. 코딩 에이전트 dashboard는 별도 패키지로 만들고 `AgentRecord`만 소비한다. 화면이나 키 처리 오류는 agent dashboard에서 고치며, 감지 오류는 detector 또는 manifest에서 고친다.

### 런타임 경계 유지

CLI와 dashboard는 Zellij를 직접 호출하지 않는다. pane 생성, 화면 감지 입력, focus 이동은 `AgentService`가 `RuntimeService`와 Zellij backend를 통해 수행한다.

## 기본 role과 에이전트 profile

기존 `coding-agent` role은 Codex 전용 설명과 인자를 갖고 있다. 기능 구현의 첫 단계에서 이 기본 role을 네 에이전트를 포괄하도록 일반화한다.

`AgentKind`는 다음 네 값으로 제한한다.

| AgentKind | 표시 이름 | 기본 명령 |
|---|---|---|
| `codex` | Codex | `codex --dangerously-bypass-approvals-and-sandbox` |
| `claude` | Claude Code | `claude --dangerously-skip-permissions` |
| `gemini` | Gemini | `agy --dangerously-skip-permissions` |
| `cursor` | Cursor Agent | `agent --yolo --trust` |

profile은 표시 이름, 실제 실행 파일, 기본 인자와 감지 manifest 이름을 제공한다. 사용자가 `--` 뒤에 준 인자는 기본 인자 뒤에 순서대로 추가한다. Cursor Agent profile은 특정 모델을 강제하지 않는다.

## 데이터 모델

`AgentRecord`는 최소한 다음 값을 가진다.

```text
ID                  daemon이 생성한 논리 agent ID
Kind                codex | claude | gemini | cursor
PaneID              기존 runtime의 논리 pane ID
State               idle | working | blocked | unknown
StateReason         매칭 규칙 또는 감지 실패 사유
MatchedRule         마지막으로 매칭된 manifest 규칙 ID
CreatedAt           agent 생성 시각
StateChangedAt      현재 상태로 전환된 시각
```

session, 실제 Zellij pane ID, CWD, 명령, pane 생명주기는 연결된 runtime `Pane`에서 가져온다. 같은 정보를 `AgentRecord`에 중복 저장하지 않는다.

`AgentStore`는 저장 구현과 service를 분리하는 인터페이스다. 첫 버전은 daemon 생명주기 동안만 유지되는 메모리 구현을 사용한다. pane close 이벤트가 오면 monitor와 record를 즉시 제거한다. 이벤트를 놓친 경우를 위해 daemon은 runtime reconciliation을 2초마다 실행하고, 실제 Zellij pane이 사라진 managed coding-agent record도 제거한다. runtime의 일반 pane registry가 `lost` 진단을 보존하더라도 coding-agent registry에는 사라진 pane을 남기지 않는다. 향후 파일 또는 SQLite 저장소를 추가할 수 있지만 첫 버전은 재시작 복구를 보장하지 않는다.

store 계약은 `Create`, `Get`, `List`, `UpdateState`, `Delete`로 제한한다. 상태 변경 비교와 `StateChangedAt` 갱신은 `UpdateState`가 원자적으로 수행한다.

## 서비스와 transport

`AgentService`는 다음 유스케이스를 제공한다.

```text
StartAgent(request) -> AgentRecord
ListAgents() -> []AgentRecordWithPane
FocusAgent(request) -> result
```

transport는 이에 대응하는 agent 전용 요청을 노출한다.

```text
POST /v1/agents
GET  /v1/agents
POST /v1/agents/{agent-id}/focus
```

`POST /v1/agents` 요청은 `kind`, `cwd`, `args`, `source_session`, `source_zellij_pane_id`를 받는다. `GET /v1/agents`는 각 AgentRecord와 연결된 runtime Pane을 함께 반환한다. focus 요청은 `source_session`과 `source_zellij_pane_id`를 받아 요청을 보낸 dashboard의 Zellij 문맥을 식별한다. 일반 pane API에는 agent 전용 필드를 추가하지 않는다.

### StartAgent

요청은 agent 종류, CWD, 추가 인자, 호출 pane의 Zellij session과 물리 pane ID를 포함한다. CLI는 `ZELLIJ_SESSION_NAME`과 `ZELLIJ_PANE_ID` 환경에서 호출 문맥을 얻는다. daemon은 Zellij `ListPanes`를 통해 호출 pane이 속한 tab을 찾고 그 tab을 대상으로 기존 `RuntimeService.CreatePane`을 호출한다.

논리 pane은 `role=coding-agent`로 등록한다. AgentRecord와 pane 생성 중 하나라도 실패하면 반대편 자원을 정리하여 반쪽 등록을 남기지 않는다. monitor는 pane 출력 구독이 준비된 뒤 시작한다.

### FocusAgent

dashboard는 선택한 agent ID와 dashboard 자신의 Zellij 호출 문맥을 요청에 넣는다. service는 AgentRecord에서 논리 pane을 찾고 runtime registry에서 대상 session과 실제 Zellij pane ID를 해석한다. backend는 설치된 Zellij 0.44.1의 다음 동작을 사용한다.

```bash
zellij action switch-session <target-session> --pane-id <target-pane-id>
```

focus 동작은 요청을 보낸 dashboard 클라이언트에 적용되어야 한다. daemon subprocess에서 실행할 때 dashboard의 source Zellij 문맥을 전달할 수 있도록 backend 요청을 설계하고, 단일 클라이언트와 복수 attach 환경의 실제 동작을 통합 테스트한다. dashboard는 Zellij 명령을 직접 실행하지 않는다.

## CLI

```bash
zellij-agent agent start codex
zellij-agent agent start claude --cwd /path/to/project
zellij-agent agent start gemini -- --model <model>
zellij-agent agent start cursor
zellij-agent agent dashboard
```

- `start`의 `--cwd` 기본값은 현재 디렉터리다.
- 첫 버전은 Zellij 내부 실행만 지원한다.
- agent 종류가 잘못됐거나 Zellij 호출 문맥이 없으면 pane을 만들기 전에 실패한다.
- agent ID와 pane 이름은 daemon이 충돌 없이 생성한다.
- `dashboard`는 코딩 에이전트 전용 TUI를 시작한다.

## 상태 감지 엔진

`docs/agent-status-detection.md`를 상태 의미와 규칙 동작의 기준 문서로 사용한다. 감지기는 Herdr 구현에 결합하지 않으며 이 저장소 안의 독립 패키지로 구현한다.

### 입력

```text
screen        Zellij 출력 구독에서 얻은 최신 렌더링 화면
osc_title     source adapter가 실제로 확보한 경우에만 제공
osc_progress  source adapter가 실제로 확보한 경우에만 제공
```

OSC 입력을 확보하지 못한 경우 빈 값을 실제 신호처럼 만들지 않는다. 첫 버전은 screen 규칙만으로도 동작하며, 입력 구조는 OSC source를 나중에 추가할 수 있게 유지한다. Zellij pane title이 OSC title과 동일한지 검증되면 별도 source adapter를 통해 제공한다.

### manifest와 평가

에이전트별 manifest는 감지 패키지 아래 독립 파일로 둔다. manifest는 다음 개념을 지원한다.

- region: `whole_recent`, `bottom_non_empty_lines(N)`, `after_last_prompt_marker`, `prompt_box_body`, `after_last_horizontal_rule`, `osc_title`, `osc_progress`
- matcher: `contains`, `regex`, `line_regex`, `all`, `any`, `not`
- 결과: `idle`, `working`, `blocked`, `unknown`
- 부가 정보: `priority`, `visible_idle`, `visible_working`, `visible_blocker`, `skip_state_update`

여러 규칙이 맞으면 높은 priority가 이긴다. 같은 priority면 manifest에서 먼저 선언한 규칙이 이긴다. `skip_state_update`는 viewer나 menu처럼 live 상태를 증명하지 않는 화면에서 기존 상태를 유지한다. 알려진 에이전트에서 어떤 규칙도 맞지 않으면 `idle` fallback을 사용한다.

### 상태 안정화

- 새 agent는 `unknown`으로 생성한다.
- 출력 구독이 시작되면 3초 startup grace 동안 화면 판정을 유예한다.
- grace 뒤 첫 유효 판정으로 상태를 설정한다.
- 명시적인 `visible_idle` 없이 `working -> idle` 후보가 생기면 100ms 간격으로 최대 3회 확인하고, 늦어도 700ms 안에 확정한다.
- viewer/menu의 `skip_state_update`는 현재 상태를 바꾸지 않는다.
- 강한 `visible_blocker`와 약한 blocked fallback을 구분한다.
- 실제 상태가 달라진 경우에만 store를 갱신하고 이벤트를 발행한다.

`AgentMonitor`는 기존 pane 출력 구독 경로가 받은 최신 rendered screen을 소비한다. monitor가 Zellij를 별도로 직접 구독하여 중복 subprocess를 만들지 않는다.

## 이벤트

상태 변경 시 기존 event bus에 `agent_state_changed`를 발행한다.

```text
agent_id
pane_id
agent_kind
previous_state
state
matched_rule
reason
time
```

첫 버전의 dashboard는 목록 refresh 또는 event stream 갱신에 이 이벤트를 사용한다. 이후 macOS 알림, 음성, hook은 이 이벤트의 별도 구독자로 추가한다. dashboard 표시 코드에 알림 로직을 넣지 않는다.

## Dashboard

전용 dashboard는 flat list를 사용한다.

```text
AGENT DASHBOARD  * LIVE  3 agents

STATE    AGENT       PROJECT             SINCE
working  Codex       zellij-with-code…   01:24
blocked  Claude      api-server          00:12
idle     Gemini      frontend            03:41

j/k move  Enter focus  R refresh  q quit
```

지원 키는 다음으로 제한한다.

- `j`, `k`, 방향키: 선택 이동
- `Enter`: 선택한 agent의 pane으로 focus
- `Space`: 선택한 agent를 목록 상단에 pin하거나 pin 해제
- `R`: 즉시 새로고침
- `q`, `Ctrl-C`: 종료

dashboard는 `AgentRecordWithPane`을 렌더링할 뿐 manifest를 평가하지 않는다. focus 실패는 TUI를 종료하지 않고 상태 줄에 표시한다. daemon 연결이 끊기면 마지막 성공 목록을 유지하고 connection 상태를 `DEGRADED`로 표시한다.

## 오류 처리

- 알 수 없는 agent 종류: 생성 전 validation 오류
- Zellij 외부 실행 또는 source pane 부재: 생성 전 문맥 오류
- pane 생성 실패: AgentRecord를 남기지 않음
- AgentRecord 등록 뒤 pane 초기화 실패: pane과 record 롤백
- 출력 구독 실패: pane 생명주기 오류와 별도로 agent 상태를 `unknown`으로 갱신하고 이유 기록
- manifest 로딩 실패: 해당 종류의 agent 시작을 안전하게 실패
- 개별 화면 평가 실패: 이전 상태 유지, 진단 이유 기록, daemon 지속
- focus 대상 pane 소실: not-found 오류, dashboard 유지 및 refresh
- pane 종료: monitor 중단, AgentRecord 제거, dashboard 목록에서 제거
- close 이벤트 유실: daemon의 2초 주기 runtime reconciliation이 실제 pane 부재를 확인하고 AgentRecord 제거

## 테스트 전략

### 단위 테스트

- 네 profile의 기본 명령과 추가 인자 결합
- 모든 region selector와 matcher 조합
- priority와 동률 순서
- 네 agent의 `idle`, `working`, `blocked` 화면 fixture
- `skip_state_update`, known-agent idle fallback
- startup grace와 `working -> idle` 안정화
- 상태가 변경된 경우에만 store update와 이벤트 발행
- dashboard 선택, refresh, focus 성공 및 실패

### 서비스와 transport 테스트

- source physical pane에서 현재 tab을 해석해 같은 tab에 agent pane 생성
- 생성 단계별 실패 시 rollback
- agent 목록과 runtime pane join
- focus 요청이 target session과 pane ID를 올바르게 해석
- `switch-session <session> --pane-id <pane>` 명령 생성
- agent API 요청과 응답 변환

### 통합 검증

- fake Zellij backend로 daemon 전체 흐름 검증
- 실제 Zellij opt-in 테스트로 현재 tab 생성과 focus 이동 검증
- 복수 attach 환경에서 focus 대상 클라이언트 검증
- 관련 패키지 테스트 후 `go test ./...`

## 향후 확장

다음 기능은 현재 경계를 유지한 채 독립적으로 추가할 수 있다.

- `agent_state_changed` 기반 macOS 및 음성 알림
- 상태 전환 hook과 자동 동작
- dashboard의 prompt 입력과 agent 종료
- OSC title 및 OSC progress source 개선
- AgentStore 영속화와 daemon 재시작 복구
- 새 agent profile과 manifest 추가
