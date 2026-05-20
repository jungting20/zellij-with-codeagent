# AI Agent + Zellij Runtime Architecture

## 전체 흐름

```text
1. 사용자 요청
2. 데몬 실행(agentd)
3. AI planner가 요청 분석
4. 필요한 pane 결정
```

이제부터가 실제 runtime orchestration 단계다.

---

# 5. planner가 daemon runtime API 호출

예를 들어 planner가 판단:

```text
- backend 서버 필요
- test runner 필요
- coder pane 필요
```

planner는 직접 zellij를 만지지 않는다.

대신 daemon API 호출:

```go
runtime.CreatePane(CreatePaneRequest{
    Role: "backend",
    Cmd: []string{"npm", "run", "dev"},
})
```

---

# 6. daemon이 실제 zellij pane 생성

daemon 내부:

```bash
zellij action new-pane -- npm run dev
```

실행.

---

# 7. zellij가 pane ID 반환

예:

```text
pane_id = 12
```

---

# 8. daemon이 registry에 등록

registry state:

```json
{
  "pane_id": 12,
  "role": "backend",
  "status": "starting"
}
```

---

# 9. daemon이 subscribe 연결

중요한 단계.

daemon이:

```bash
zellij subscribe --pane-id 12 --format json
```

시작.

---

# 10. pane output 발생

예:

```text
Server started on :3000
```

---

# 11. subscribe reader가 이벤트 수신

daemon 내부:

```json
{
  "pane_id": 12,
  "content": "Server started on :3000"
}
```

---

# 12. daemon이 event 생성

semantic event로 변환 가능.

예:

```json
{
  "type": "server_ready",
  "pane_id": 12,
  "port": 3000
}
```

---

# 13. event bus에 publish

daemon 내부:

```text
eventBus.Publish(event)
```

---

# 14. planner가 이벤트 수신

planner는:

```go
planner.Subscribe("server_ready")
```

같은 상태.

---

# 15. planner가 다음 행동 결정

AI reasoning:

```text
backend 준비 완료
→ 이제 test pane 실행 가능
```

---

# 16. planner가 또 runtime API 호출

```go
runtime.CreatePane(CreatePaneRequest{
    Role: "test-runner",
    Cmd: []string{"npm", "test"},
})
```

---

# 17. test pane 생성

예:

```text
pane 13
```

---

# 18. test output 발생

```text
FAIL auth_refresh_test
```

---

# 19. daemon이 이벤트 변환

```json
{
  "type": "test_failed",
  "test": "auth_refresh_test"
}
```

---

# 20. planner가 실패 분석

AI reasoning:

```text
auth refresh 관련 문제
→ coder pane 필요
```

---

# 21. coder pane 생성

```go
runtime.CreatePane(CreatePaneRequest{
    Role: "coder",
})
```

---

# 22. planner가 coder pane에 입력 전달

```go
runtime.SendInput(
    paneID,
    "Investigate auth refresh failure\n",
)
```

---

# 23. coder pane 작업 수행

예:

```text
vim auth.ts
```

또는 AI coding agent 실행.

---

# 24. 수정 감지

daemon이:

```text
git diff
```

또는 filesystem watch 감지.

---

# 25. planner가 재테스트 요청

```go
runtime.SendInput(
    testPane,
    "npm test auth_refresh_test\n",
)
```

---

# 26. 테스트 성공

```text
PASS auth_refresh_test
```

---

# 27. daemon 이벤트 publish

```json
{
  "type": "task_resolved"
}
```

---

# 28. planner가 작업 종료 판단

```text
모든 테스트 통과
→ task complete
```

---

# 29. daemon이 pane cleanup

예:

```go
runtime.KillPane(testPane)
```

---

# 30. 최종 상태

supervisor pane:

```text
TASK: fix auth bug
STATUS: resolved
TESTS: passing
PANES: cleaned
```

---

# 전체 흐름 한 줄 요약

```text
planner는 "무엇을 할지" 결정하고

daemon은 "실제로 terminal을 운영"한다
```

---

# 각 역할 정리

## 사용자

```text
goal 제공
```

---

## planner

```text
생각하는 역할
```

예:
- 어떤 pane 필요?
- 다음 행동?
- 실패 원인?

---

## daemon

```text
운영체제 역할
```

예:
- pane 생성
- subscribe
- registry
- cleanup
- lifecycle

---

## zellij

```text
실제 terminal runtime
```

---

# 핵심

## BAD

```text
planner → zellij
```

---

## GOOD

```text
planner → daemon API → zellij
```

---

# 왜 중요하냐

그래야:

- pane 상태 일관성 유지
- recovery 가능
- observability 가능
- subscription 관리 가능

해진다.

---

# MVP 구현 invariant

현재 구현된 MVP에서도 핵심 방향은 같다.

```text
planner / client
        ↓
local transport (Unix socket JSON HTTP) 또는 in-process caller
        ↓
internal/runtime.RuntimeService
        ↓
registry + eventbus + zellij backend
        ↓
zellij CLI
```

## 반드시 지킬 것

- planner나 외부 client는 zellij CLI를 직접 호출하지 않는다.
- 외부 client는 local transport를 통해 요청하고, transport는 `RuntimeService`만 호출한다.
- zellij pane 생성, 입력, snapshot, subscribe, reconcile, cleanup은 daemon runtime boundary를 통해서만 한다.
- logical `PaneID`는 daemon-owned ID이고, `ZellijPaneID`는 backend ID일 뿐이다.
- registry가 managed pane 상태의 기준이다. zellij는 실행 runtime이지 상태 저장소가 아니다.
- reconcile은 unmanaged live pane을 보고할 수 있지만 기본적으로 adopt하거나 close하지 않는다.
- pane이 `closed`, `exited`, `lost`가 되면 subscription lifecycle도 같이 종료되어야 한다.
- supervisor/debug view도 planner와 같은 runtime introspection 데이터를 읽어야 한다.

## 현재 transport

현재 외부 진입점은 `agentd serve --socket <path>`이다.

```text
외부 Client
        ↓
Unix socket JSON HTTP
        ↓
agentd transport
        ↓
RuntimeService
```

외부 client는 production planner와 같은 규칙을 따른다.

- pane 생성, 입력, event stream, recent events, snapshot, cleanup은 모두 transport API를 통한다.
- 같은 task의 pane들은 logical `TaskID`로 묶고, cleanup도 `TaskID` 기준으로 수행한다.
- live event stream이 끊겨도 `RecentEvents`와 snapshot으로 상태를 다시 확인할 수 있어야 한다.

## 현재 MVP 한계

- 상태는 local memory에만 있다.
- daemon restart 후 durable recovery는 아직 없다.
- semantic event는 MVP regex/heuristic 기반이다.
- 외부 transport는 Unix socket JSON HTTP만 있다. TCP HTTP, stdio JSON-RPC, gRPC는 아직 없다.
- LLM planner reasoning loop는 아직 붙지 않았다.
- rich TUI dashboard는 아직 없다.

---

# 최종 구조

```text
AI reasoning
        ↓
terminal orchestration
        ↓
observable execution
```
