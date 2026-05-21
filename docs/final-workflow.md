# Zellij Agent Runtime 최종 동작 흐름 가이드

이 문서는 AI 에이전트가 Zellij 터미널을 자율적으로 제어하기 위해 구축된 `zellij-with-codeagent` 프로젝트의 현재 코드 수준 동작 흐름과 프로젝트가 최종적으로 완성되었을 때의 종합 시나리오를 설명합니다.

---

## 1. 프로젝트 아키텍처 개요

본 프로젝트는 에이전트가 터미널 환경을 안전하게 조작할 수 있도록 돕는 **데몬 환경(`agentd`)**과 **AI 에이전트(Planner)**를 분리한 계층형 구조를 따릅니다.

```mermaid
graph TD
    User([사용자 Goal 입력]) --> Planner[Planner (AI/Cognition)]
    Planner -- API 요청 (Unix Socket JSON HTTP) --> Daemon[agentd (Orchestrator)]
    
    subgraph Daemon Runtime (agentd)
        Server[transport.Server] --> Service[runtime.RuntimeService]
        Service --> Registry[runtime.Registry (상태 관리)]
        Service --> EventBus[eventbus.Bus (이벤트 발행)]
        Service --> SubMgr[SubscriptionManager (출력 감시)]
    end

    SubMgr -- zellij subscribe -- format json --> ZellijCLI[Zellij CLI]
    Service -- zellij action -- 실행 --> ZellijCLI
    ZellijCLI --> Zellij[Zellij Runtime (실제 터미널)]
```

---

## 2. 네 가지 통신 흐름 및 단계별 데이터/응답 타입 정의

`User ➡️ Planner ➡️ API ➡️ Daemon ➡️ Zellij`로 전파되는 상세한 각 단계별 요청/응답 구조 및 데이터 타입 명세는 별도 문서로 분리되어 관리됩니다.

상세 명세는 [docs/api-types-definition.md](file:///Users/in05908_mac/zellij-with-codeagent/docs/api-types-definition.md) 파일에서 확인하실 수 있습니다.

---

## 3. 현재 소스코드 상의 상세 동작 흐름

현재 구현된 MVP 소스코드(`agentd` 및 패키지들)가 실제로 어떻게 구동되는지에 대한 기술적 상세입니다.

### ① 데몬 구동 및 소켓 리스닝
1. `cmd/agentd/main.go`가 실행되면서 `serve` 커맨드를 호출합니다.
2. `internal/transport/server.go`에서 지정된 Unix Domain Socket 경로(예: `/tmp/agentd.sock`)를 열어 HTTP 서버를 바인딩하고 대기합니다.
3. 이 소켓 서버는 내부적으로 `runtime.RuntimeService`를 호출하는 핸들러를 갖습니다.

### ② 실행 계획(Execution Plan) 및 Pane 생성
1. 외부 클라이언트(Planner 등)가 `/v1/requests`에 `execution_plan` 타입의 JSON 페이로드를 전달합니다.
2. `RuntimeService.ApplyExecutionPlan`이 호출되어 다음과 같이 작동합니다.
   * `internal/zellij/backend.go`를 통해 실제 Zellij CLI 명령(`zellij action new-pane`)을 호출합니다.
   * 최초 Pane 생성 시 새 탭을 생성하고, 이후 생성되는 Pane들은 동일한 `ZellijTabID` 내에서 열립니다.
   * 생성된 Pane들은 logical ID(예: `coder`, `test`)와 Zellij가 부여한 physical ID를 매핑하여 `internal/registry/registry.go`에 영구 등록(`RegisterPane`)합니다.

### ③ 실시간 터미널 출력 감시 및 이벤트 발행
1. Pane이 생성되면 `SubscriptionManager`가 각각의 Pane에 대해 백그라운드에서 `zellij subscribe --format json` 서브프로세스를 가동합니다.
2. `internal/runtime/subscriptions.go`에서 터미널의 뷰포트 상태 프레임을 실시간 파싱(NDJSON)합니다.
3. 터미널 출력 데이터가 업데이트될 때마다, 미리 약속된 정규식 패턴(heuristic matchers)에 매칭되는지 분석합니다.
   * 예: `server listening on :3000` 감지 시 ➡️ `TypeServerReady` 이벤트 발행.
   * 예: `--- FAIL:` 감지 시 ➡️ `TypeTestFailed` 이벤트 발행.
   * 예: `ok` 또는 `PASS` 감지 시 ➡️ `TypeTestPassed` 이벤트 발행.
4. 발행된 이벤트는 `internal/eventbus/bus.go`를 거쳐 스트림 커넥션을 맺고 있는 외부 클라이언트에게 전달됩니다.

### ④ 클린업 및 리소스 반환
1. 클라이언트의 정리 요청 혹은 오류로 인해 `RuntimeService.Cleanup`이 실행됩니다.
2. `registry`에 등록되어 있는 `running` 상태 of managed pane들을 조회한 후, `zellij action close-pane`을 전송하여 안전하게 닫습니다.
3. 데몬에 등록되지 않은 사용자의 개인 Pane(unmanaged panes)은 건드리지 않고 AI 에이전트 전용 탭/Pane만 골라서 정상 클린업합니다.

---

## 4. 1차 MVP 시나리오 및 최종 자율 협업 흐름 (Milestones)

본 프로젝트의 1차 MVP는 사용자의 자연어 요청을 해석하여 알맞은 개발 환경(역할별 Pane)을 셋업하는 것까지를 목표로 합니다. 실시간 감시 및 에러 수정 등의 자율 자가치유 루프는 차기 개발 목표로 설정합니다.

### 1) 1차 MVP 동작 시나리오 (환경 자동 구성)
```
[User]                      [Planner (AI)]                 [agentd Daemon]                [Zellij Terminal]
  │                              │                               │                               │
  │ 1. 자연어 Goal 요청 전달      │                               │                               │
  ├─────────────────────────────>│                               │                               │
  │                              │ 2. 필요한 역할(role) Pane 결정 │                               │
  │                              │ 3. Execution Plan 요청        │                               │
  │                              ├──────────────────────────────>│                               │
  │                              │                               │ 4. Pane 생성 & Registry 등록  │
  │                              │                               │├─────────────────────────────>│
  │                              │ 5. ExecutionPlanResponse 응답 │                               │
  │                              │<──────────────────────────────┤                               │
  │ 6. 환경 생성 완료 보고        │                               │                               │
  │<─────────────────────────────┤                               │                               │
```
1. **사용자 Goal 하달**: 사용자가 Planner에게 자연어 목표를 제시합니다.
   * 예: *"Go 프로젝트 로그인 모듈 버그 고칠 수 있는 개발 환경 열어줘."*
2. **개발 환경 역할(Role) 결정**: Planner가 자연어를 해석하여 필요한 환경 레이아웃을 정의합니다.
   * 예: "코드 수정을 위해 `editor` 역할 Pane 1개, 테스트 구동을 위해 `tester` 역할 Pane 1개가 필요하겠군."
3. **Execution Plan 요청**: Planner가 `agentd` 데몬의 UDS API에 `execution_plan` payload를 전송합니다.
4. **Zellij Pane 생성**: 데몬이 요청에 부합하도록 Zellij CLI를 통해 실제 Pane을 실행하고, 에이전트가 관리할 Pane 메타데이터를 Registry에 안정적으로 등록합니다.
5. **결과 응답**: 데몬이 생성이 완료된 최종 세션 및 Pane 목록(`ExecutionPlanResponse`)을 즉시 Planner에게 반환하고, Planner는 이 최종 환경 정보를 기반으로 사용자에게 환경 셋업이 완료되었음을 보고합니다.

---

### 2) 최종 완성 시 전체 자율 협업 흐름 (Self-Healing Scenario) - *MVP 이후 단계*

데몬과 AI Planner(LLM 루프)가 완전히 결합되었을 때, 사용자의 골(Goal)을 해결하기 위한 전체 자율 자가치유(Self-Healing) 흐름은 다음과 같이 진행됩니다.

```
[Planner]                   [agentd Daemon]                [Zellij Terminal]
    │                              │                               │
    │ 1. Execution Plan 요청       │                               │
    ├─────────────────────────────>│                               │
    │                              │ 2. Pane 생성 & Registry 등록  │
    │                              ├──────────────────────────────>│
    │                              │ 3. zellij subscribe 백그라운드│
    │                              ├──────────────────────────────>│
    │ 4. Event Stream 구독         │                               │
    ├─────────────────────────────>│                               │
    │                              │                               │
    │ 5. "테스트 실행" 입력 전달    │                               │
    ├─────────────────────────────>│ 6. SendInput                  │
    │                              ├──────────────────────────────>│
    │                              │                               │ (테스트 실패)
    │                              │ 7. '--- FAIL' 출력 감지       │ <─────────────┘
    │ 8. 'TypeTestFailed' 수신     │                               │
    │<─────────────────────────────┤                               │
    │                              │                               │
    │ 9. 정밀 분석 (Snapshot 요청) │                               │
    ├─────────────────────────────>│ 10. DumpScreen                │
    │                              ├──────────────────────────────>│
    │ 11. 터미널 스냅샷 데이터 수신│                               │
    │<─────────────────────────────┤                               │
    │                              │                               │
    │ 12. 코드 수정 (자율 에이전트) │                               │
    │     및 테스트 재실행 명령    │                               │
    ├─────────────────────────────>│ 13. SendInput                 │
    │                              ├──────────────────────────────>│
    │                              │                               │ (테스트 통과)
    │                              │ 14. 'ok' / 'PASS' 출력 감지   │ <─────────────┘
    │ 15. 'TypeTestPassed' 수신    │                               │
    │<─────────────────────────────┤                               │
    │                              │                               │
    │ 16. 태스크 완료 및 Cleanup   │                               │
    ├─────────────────────────────>│ 17. Close-Panes               │
    │                              ├──────────────────────────────>│ (터미널 종료)
    v                              v                               v
```

1. **초기 셋업**: AI Planner가 `agentd`에 `execution_plan`을 호출하여 역할별 개발 환경 Pane을 생성합니다.
2. **이벤트 감시**: Planner가 이벤트 스트림(`/v1/events`)을 구독하고, `SubscriptionManager`가 백그라운드에서 `zellij subscribe --format json`을 수행합니다.
3. **오류 인지**: 테스트 실행 중 오류가 발생하여 화면에 `--- FAIL` 문자열이 감지되면 데몬이 `TypeTestFailed` 이벤트를 Planner로 발행합니다.
4. **자율 수정 (Self-Healing)**: Planner가 오류 스택 트레이스 스냅샷을 분석하고 소스코드를 올바르게 수정한 후 테스트 Pane에 입력을 주입해 테스트를 재구동합니다.
5. **검증 및 클린업**: 테스트 결과가 정상 통과(`ok` 또는 `PASS`)함을 수신한 뒤 작업을 마무리하고 생성된 모든 Pane에 대한 클린업을 요청합니다.

### 1단계: 사용자 Goal 하달
사용자가 메인 Planner에 **"백엔드 인증 오류 버그를 고쳐줘"**라는 목표를 주입합니다.

### 2단계: 가상 환경 레이아웃 빌드 (Setup)
Planner는 해당 Goal을 분석하여 필요한 작업 영역을 구상합니다. 
* "빌드 Pane, 테스트 감시 Pane, 서버 로그 Pane, 코드 수정용 Pane이 필요하겠군."
* Planner는 `agentd`에 `execution_plan` API를 던집니다. `agentd`는 Zellij에서 에이전트 전용 탭을 분리해 지정된 4개의 Pane을 자동 배치하고 실행합니다.

### 3단계: 이벤트 감시 및 초기 작업 시작
Planner는 `agentd` 소켓 스트림으로부터 "Pane 생성 완료" 이벤트를 수신한 즉시, 테스트 Pane에 `go test ./...` 또는 `npm run test` 명령을 전달(`SendInput`)합니다.

### 4단계: 오류 인지 및 자가 복구 루프 (Self-Healing Loop)
1. 테스트 러너가 에러를 뱉어 터미널 화면에 `--- FAIL: TestAuth`가 표시됩니다.
2. `agentd` 감시 모듈이 이를 1초 이내에 감지해 `TypeTestFailed` 이벤트를 Planner에 쏘아 올립니다.
3. Planner는 이를 수신한 뒤, 즉각 해당 테스터 Pane에 `/v1/panes/{pane_id}/snapshot`을 요청해 에러 스택 트레이스 및 로그 스냅샷을 덤프해 정밀 분석합니다.
4. 분석 결과, 특정 파일의 세션 유지 코드에 버그가 있음을 식별하고 에이전트 내부의 파일 편집 도구를 활용해 소스코드를 정상적으로 수정합니다.

### 5단계: 코드 검증 및 루프 탈출
1. Planner는 다시 테스트 Pane에 테스트 재실행 키(`Enter` 또는 명령어)를 전달합니다.
2. 테스트가 통과하여 `ok` 또는 `PASS` 문자열이 화면에 출력되면, `agentd`가 이를 캐치하여 `TypeTestPassed` 이벤트를 브로드캐스팅합니다.
3. Planner는 목표가 완전히 달성되었음을 인지하고 자가 치유 루프를 성공적으로 완료(Resolved) 처리합니다.

### 6단계: 리소스 완전 정리 (Teardown)
Planner는 마지막에 `POST /v1/cleanup`을 호출하여, AI 자율 작업 도중 생성했던 모든 Pane과 탭을 닫아 깔끔하게 리소스를 정제하며 사용자에게 최종 성공 리포트를 출력합니다.
