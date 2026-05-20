# Zellij Agent Runtime 단계별 통신 및 응답 타입 정의 가이드

이 문서는 `User ➡️ Planner ➡️ API ➡️ Daemon ➡️ Zellij`로 이어지는 시스템 흐름의 각 구간별 요청/응답 형식과 데이터 구조를 상술합니다.

---

## 1. 아키텍처 상의 통신 관계도

```
 [User] <──────────────── (1) 자연어 Goal / 피드백 ────────────────> [Planner]
                                                                        │
                                                                        │ (2) HTTP/JSON
                                                                        ▼
 [Daemon] <── (4) Go Struct ──> [Runtime Service] <── Go Struct ──> [API Server]
    │
    │ (3) CLI 호출 / NDJSON
    ▼
 [Zellij]
```

---

## 2. 각 흐름별 상세 데이터 구조 및 타입 정의

### 1) User ↔️ Planner (자연어 및 고수준 리포트)
사용자가 AI 에이전트(Planner)에게 최종 목표를 지시하고, Planner가 사용자에게 사고 과정 및 해결 결과를 요약 제공하는 타입입니다.

*   **요청 타입 (`UserGoalRequest`)**:
    *   사용자가 주입하는 자연어 텍스트 데이터.
    ```json
    {
      "goal": "Go 언어로 짜여진 로그인 모듈의 세션 만료 버그를 찾아서 고치고 테스트를 통과시켜줘."
    }
    ```
*   **응답 타입 (`PlannerGoalResponse`)**:
    *   현재 Planner의 사고 진행 단계, 수행한 작업 로그, 최종 성공 여부를 리포팅하는 응답.
    ```json
    {
      "task_id": "task_auth_bugfix_109",
      "status": "resolved", // starting | diagnosing | fixing | verifying | resolved | failed
      "summary": "auth_service.go 45라인의 세션 유지 시간 연장 로직 수정 완료 및 유닛 테스트 통과 확인.",
      "steps_executed": [
        {"step": "환경 분석", "result": "Zellij 탭 및 Pane 4개(coder, test, server, log) 정상 구동 완료"},
        {"step": "오류 식별", "result": "TestSessionTimeout 실패 감지 (expected 30m, got 30s)"},
        {"step": "수정 조치", "result": "Time.Duration 단위 변환 오류 복구 (30 * time.Second -> 30 * time.Minute)"},
        {"step": "최종 검증", "result": "go test ./internal/auth 실행 후 PASS 이벤트 확인"}
      ],
      "finished_at": "2026-05-20T14:25:00+09:00"
    }
    ```

---

### 2) Planner ↔️ API (HTTP/JSON over Unix Domain Socket)
외부 프로세스인 Planner가 `agentd` 소켓 서버 게이트웨이에 던지는 프로토콜 형식과 서버가 보내는 JSON Response 형식입니다.

*   **요청 봉투 타입 (`RequestEnvelope`)**:
    *   모든 Planner 요청의 상위 공통 래핑 규격.
    ```json
    {
      "type": "execution_plan", // execution_plan | create_pane | send_input | snapshot | cleanup
      "request_id": "req_uuid_998",
      "payload": { ... } // 각 요청타입별 상세 Payload
    }
    ```
*   **실행 계획 요청 페이로드 (`ExecutionPlanPayload`)**:
    *   Planner가 레이아웃과 초기 터미널 목록을 일괄 요청할 때 사용.
    ```json
    {
      "session": "feature-auth-fix",
      "layout": "triple-horizontal",
      "panes": [
        {"id": "coder", "role": "editor", "cwd": "/Users/in05908_mac/project"},
        {"id": "test", "role": "tester", "cwd": "/Users/in05908_mac/project"}
      ]
    }
    ```
*   **실행 계획 응답 타입 (`ExecutionPlanResponse`)**:
    *   데몬에 의해 할당된 실제 Zellij 메타데이터를 매핑하여 Planner에게 응답.
    ```json
    {
      "request_id": "req_uuid_998",
      "session": "feature-auth-fix",
      "layout": "triple-horizontal",
      "panes": [
        {
          "id": "coder",
          "task_id": "feature-auth-fix",
          "zellij_pane_id": "terminal_42",
          "zellij_tab_id": 1,
          "tab_name": "feature-auth-fix",
          "role": "editor",
          "status": "running",
          "created_at": "2026-05-20T14:24:00Z"
        }
      ]
    }
    ```

---

### 3) API ↔️ Daemon / Runtime Service (Go 내부 Struct)
HTTP API 계층(`internal/transport`)에서 JSON을 파싱하여 Go 내부 서비스 비즈니스 로직 계층(`internal/runtime`)으로 전달하는 타입 구조입니다.

*   **Pane 생성 요청 구조체 (`rt.CreatePaneRequest`)**:
    ```go
    type CreatePaneRequest struct {
        ID          PaneID      // "coder"
        TaskID      TaskID      // "feature-auth-fix"
        AgentID     AgentID     // "agent-llm"
        Role        string      // "editor"
        Name        string      // "coder"
        NewTab      bool        // true
        TabName     string      // "feature-auth-fix"
        ZellijTabID *ZellijTabID // 기존 탭을 재사용할 경우 탭 ID 포인터
        Command     []string    // 실행할 쉘 랩핑 커맨드 (예: ["sh", "-lc", "..."])
        CWD         string      // 작업 디렉토리 경로
    }
    ```
*   **Pane 생성 응답 구조체 (`rt.CreatePaneResponse`)**:
    ```go
    type CreatePaneResponse struct {
        Pane Pane // 생성된 Pane의 전체 상태 구조체
    }
    ```
*   **Pane 상태 구조체 (`rt.Pane`)**:
    ```go
    type Pane struct {
        ID            PaneID        // "coder"
        TaskID        TaskID        // "feature-auth-fix"
        AgentID       AgentID       // "agent-llm"
        ZellijPaneID  ZellijPaneID  // "terminal_42"
        ZellijTabID   *ZellijTabID  // 1
        TabName       string        // "feature-auth-fix"
        Role          string        // "editor"
        Command       []string      // ["sh", "-lc", "..."]
        CWD           string        // "/Users/in05908_mac/project"
        Status        PaneStatus    // "running" (starting | running | exited | closed | lost | error)
        LastOutput    string        // 최신 출력 일부 캐시
        StatusMessage string        // 상세 상태 메시지
        CreatedAt     time.Time
        UpdatedAt     time.Time
    }
    ```

---

### 4) Daemon ↔️ Zellij CLI (시스템 표준 입출력 및 NDJSON)
데몬(`agentd`)이 시스템 쉘을 통해 Zellij CLI와 조작/구독 통신을 수행할 때의 데이터 규격입니다.

*   **Zellij 명령 호출 및 CLI 표준 출력**:
    *   데몬은 `zellij action list-panes --json` 등을 실행하고 Zellij CLI가 출력하는 JSON 표준 형식을 수신하여 Go 슬라이스로 바인딩합니다.
    ```json
    [
      {
        "id": 42,
        "is_plugin": false,
        "is_focused": true,
        "is_fullscreen": false,
        "is_floating": false,
        "location": { "x": 0, "y": 0, "cols": 80, "rows": 24 }
      }
    ]
    ```
*   **Zellij 실시간 구독 스트림 프레임 (`ZellijSubscriptionFrame`)**:
    *   `zellij subscribe` 커맨드가 백그라운드 쉘 파이프를 통해 실시간으로 흘려보내는 JSON 뷰포트 변경 규격입니다.
    ```json
    {
      "pane_update": {
        "panes": [
          {
            "id": 42,
            "is_plugin": false,
            "viewport": [
              "--- FAIL: TestSessionTimeout (0.05s)",
              "    auth_test.go:12: Session duration mismatch: expected 30m, got 30s",
              "FAIL"
            ]
          }
        ]
      }
    }
    ```
