# 현재 Pane Coding Agent 시작 설계

## 목표

`zellij-agent agent start <kind>`가 새 Zellij pane을 생성하지 않고 명령을
실행한 현재 pane을 daemon 관리 coding-agent pane으로 전환한다. 등록이 끝나면
선택한 coding agent를 현재 터미널에서 바로 실행하며, coding agent가 종료되면
daemon이 현재 pane도 닫는다.

dashboard 표시, 상태 감지, `agent next`, focus, pane close와 같은 이후 동작은
daemon이 새로 생성한 coding-agent pane과 동일해야 한다.

## 사용자 흐름

```text
zellij-agent agent start codex
  -> 현재 session/tab/pane 확인
  -> 현재 물리 pane을 daemon registry에 coding-agent로 등록
  -> monitor와 Zellij output subscription 시작
  -> 현재 터미널에서 Codex 실행
  -> Codex 종료
  -> daemon의 기존 ClosePane 경로로 현재 pane 종료 및 agent 정리
```

명령은 기존과 같이 Zellij 내부에서만 실행할 수 있다. `--cwd`, `--socket`,
`--timeout`, `--` 뒤의 agent 추가 인자와 네 종류의 agent profile은 유지한다.
등록 후에는 별도의 `started ...` 성공 문구를 출력하지 않고 interactive agent
화면으로 바로 전환한다.

## 핵심 설계 결정

### 현재 pane을 daemon 소유로 전환

현재 pane에 별도의 `adopted` 수명주기를 도입하지 않는다. 등록에 성공한 pane은
daemon이 생성한 pane과 같은 관리 대상으로 취급한다. agent 실행 실패 또는 정상
종료 후에도 pane을 보존하거나 shell prompt로 돌아가지 않고 pane을 닫는다.

다만 물리 pane 생성 책임만 다르다. 기존 `CreatePane`은 Zellij backend를 통해
새 pane을 만든 뒤 registry에 등록한다. 새 `ClaimPane` runtime 유스케이스는 이미
존재하는 물리 pane을 조회하고 registry에 등록하며, Zellij pane 생성 명령은
실행하지 않는다.

### 실행과 종료는 agent CLI가 관리

daemon은 다른 pane의 shell 프로세스를 직접 교체할 수 없으며, 현재 shell에
명령 문자열을 입력하는 방식은 quoting과 실행 시점 race를 만든다. 따라서
`agent start` CLI가 등록 응답에 포함된 runtime `Pane.Command`와 `Pane.CWD`를
사용해 coding agent 자식 프로세스를 현재 stdin/stdout/stderr에 연결하여 실행하고
종료를 기다린다.

단순 process replacement는 사용하지 않는다. 현재 pane의 shell이
`zellij-agent`의 부모이므로 agent 프로세스가 종료되면 shell이 다시 나타난다.
CLI wrapper가 자식 종료를 관찰한 뒤 기존 daemon `ClosePane` API를 호출해야
요구한 pane 종료 동작을 보장할 수 있다.

### 기존 coding-agent role의 의미 유지

이 기능에는 이미 기본 `coding-agent` role이 있으므로 새 role을 추가하지 않는다.
`agent start`는 기존처럼 coding-agent service의 agent profile 명령을 사용하며,
ticket worker가 사용하는 `zellij-agent role coding-agent`는 계속 agent process를
실행하는 순수 wrapper로 유지한다. role은 스스로 현재 pane을 등록하지 않는다.
현재 pane 등록 책임은 `zellij-agent agent start`와 daemon의 agent/runtime
서비스에만 둔다.

## Runtime 변경

`RuntimeService`에 기존 물리 pane을 등록하는 별도 유스케이스를 추가한다.

```text
ClaimPane(request) -> Pane

request:
  ID                 daemon 논리 pane ID
  AgentID            coding-agent ID
  Role               coding-agent
  ZellijSession      현재 Zellij session
  ZellijPaneID       현재 물리 pane ID
  Command            실행할 agent 명령
  CWD                agent 작업 디렉터리
```

`ClaimPane`은 다음 순서로 동작한다.

1. session과 물리 pane ID를 정규화하고 필수 값을 검증한다.
2. Zellij backend의 `ListPanes`로 해당 session에서 물리 pane을 찾는다.
3. 일치하는 terminal pane이 정확히 하나인지 확인하고 plugin pane은 거부한다.
4. 조회 결과에서 Zellij tab ID와 tab 이름을 얻는다.
5. runtime registry에 logical ID, physical ID, agent ID, role, command, CWD를
   `starting` 상태로 등록한다.
6. 기존 `PaneOpened` observer와 subscription을 시작한다.
7. 등록된 runtime `Pane`을 반환한다.

이 경로는 `CreatePane`, `CreateTab`, `SendInput`을 호출하지 않는다. 초기 입력
준비 기능도 사용하지 않는다.

### 물리 pane 중복 방지

동일한 `(ZellijSession, ZellijPaneID)`가 `starting` 또는 `running` 상태로 이미
등록되어 있으면 claim을 원자적으로 거부한다. logical pane ID 검사와 같은 registry
lock 안에서 검사하여 동시 `agent start` 요청도 하나만 성공하게 한다.

종료 상태인 과거 레코드는 중복으로 간주하지 않는다. Zellij가 pane ID를
재사용할 수 있고 서로 다른 Zellij session에서 같은 pane ID가 존재할 수 있으므로
session과 physical ID를 함께 비교한다.

## Coding-agent 서비스 변경

`StartAgent`의 validation, agent ID 생성, store 등록, monitor 시작과 profile 명령
생성은 유지한다. 기존 `RuntimeService.CreatePane` 호출만 `ClaimPane`으로 교체한다.
요청의 `SourceZellijSession`과 `SourceZellijPaneID`가 claim 대상이다.

claim에 실패하면 monitor와 agent store record를 롤백하되 현재 물리 pane은 닫지
않는다. claim이 성공한 뒤에는 agent ownership을 active로 전환하고 runtime pane이
포함된 기존 응답 형식을 반환한다.

`ListAgents`, dashboard, `FocusAgent`, `FocusNextAgent`는 agent와 runtime pane의
연결 구조가 동일하므로 변경하지 않는다.

## CLI 실행 수명주기

`agent start`는 등록 요청에 사용한 timeout과 agent 실행 시간을 분리한다.
`--timeout`은 daemon 등록 요청에만 적용한다. 등록 성공 후 요청 context를 해제하고
agent process는 시간 제한 없이 실행한다.

등록 응답의 `Pane.Command`가 비어 있거나 executable을 시작할 수 없으면 오류를
기록하고 해당 logical pane의 `ClosePane`을 요청한다. agent가 정상 또는 비정상
종료해도 같은 close 경로를 호출한다. close 요청에는 새로운 짧은 timeout context를
사용한다.

CLI wrapper는 interactive stdin/stdout/stderr를 그대로 연결한다. 종료 처리는
signal로 인한 child 종료도 포함해야 하며, wrapper가 child보다 먼저 사라져 close
요청을 놓치지 않도록 signal handling을 테스트 가능한 실행 단위로 분리한다.

`ClosePane`은 기존 runtime boundary를 통해 Zellij pane을 닫고 generation-aware
closure claim, subscription 중단, `PaneClosed` observer 호출을 수행한다. 이
observer가 coding-agent monitor와 store record를 제거한다. CLI는 Zellij를 직접
호출하지 않는다.

## Ticket Worker 격리

`ticket-worker start` 동작은 변경하지 않는다.

- manager pane은 기존 execution plan으로 새 tab/pane을 만든다.
- ticket manager는 worker마다 기존 `POST /v1/panes`와 `CreatePane`을 호출한다.
- worker command인 `zellij-agent role coding-agent --yolo ...`의 등록 책임이나
  종료 의미를 변경하지 않는다.
- `CreatePane` 구현과 transport 계약은 유지한다.

따라서 current-pane claim은 `/v1/agents`의 `StartAgent`에서만 사용한다. 이미
ticket-worker 또는 다른 runtime 관리 대상으로 등록된 pane에서 사용자가
`agent start`를 실행하면 physical pane 중복 검사로 요청을 거부하고 기존 pane은
그대로 유지한다.

## 오류 및 정리 정책

- Zellij 외부 실행, 빈 session/pane ID, 잘못된 agent kind 또는 CWD는 등록 전에
  실패한다.
- 현재 물리 pane을 찾지 못하거나 plugin pane이면 agent record와 monitor를
  롤백하고 pane은 유지한다.
- 이미 active 관리 중인 물리 pane이면 중복 오류를 반환하고 기존 record를
  변경하지 않는다.
- agent process 시작 실패와 agent process 종료는 등록된 pane의 `ClosePane`을
  요청한다.
- 사용자가 agent 실행 중 pane을 직접 닫으면 기존 subscription의 `pane_closed`
  처리로 runtime과 coding-agent 상태를 정리한다.
- daemon이 agent 실행 중 재시작되어 in-memory registry가 사라지는 경우의 복구는
  기존 coding-agent 기능과 동일하게 이 변경의 범위에서 제외한다.
- close 요청 자체가 실패하면 CLI는 오류를 기록한다. runtime boundary를 우회하는
  직접 Zellij close fallback은 추가하지 않는다.

## 테스트 전략

### Runtime과 registry

- 기존 pane claim이 backend `CreatePane` 없이 tab metadata를 등록하는지 검증
- 존재하지 않는 pane, plugin pane, session 불일치와 모호한 결과 검증
- 동일 session/physical pane의 active 중복을 거부하는지 검증
- 종료 레코드의 pane ID 재사용과 다른 session의 같은 pane ID를 허용하는지 검증
- claim 성공 시 observer와 subscription이 시작되는지 검증
- 기존 `CreatePane` 테스트가 그대로 통과하는지 검증

### Coding-agent 서비스와 transport

- `StartAgent`가 source pane을 claim하고 profile command를 record에 저장하는지 검증
- claim 단계별 실패가 agent record와 monitor만 롤백하고 물리 pane을 닫지 않는지
  검증
- 기존 `/v1/agents` 요청/응답 계약과 agent 목록/focus 동작 검증

### CLI

- 등록 성공 후 응답 command와 CWD로 interactive child를 실행하는지 검증
- 등록 timeout이 child lifetime에 적용되지 않는지 검증
- 정상 종료, non-zero 종료와 process 시작 실패 모두 close를 요청하는지 검증
- 등록 실패 시 child를 실행하거나 pane을 닫지 않는지 검증
- close 실패 진단과 help/output 변경 검증

### 회귀 검증

- ticket-worker manager가 계속 `CreatePane`으로 worker pane을 생성하는 단위 테스트
- `zellij-agent role coding-agent` 기존 테스트
- `go test ./...`
- unified binary build와 atomic local registration
- 실제 Zellij에서 현재 pane 등록, dashboard 상태 감지, `agent next` focus와 agent
  종료 후 pane close를 수동 확인
- 실제 Zellij에서 `ticket-worker start`가 manager와 worker pane을 기존 방식으로
  생성하는지 확인

## 제외 범위

- daemon 재시작 후 실행 중 agent 자동 복구
- 기존 임의 agent process 자동 발견
- 현재 pane을 등록 해제하고 shell로 돌아오는 detach 동작
- `ticket-worker` worker 생성 방식 변경
- shell command injection 또는 CLI의 직접 Zellij 호출
