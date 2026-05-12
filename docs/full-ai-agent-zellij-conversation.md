
# AI Agent + Zellij Architecture Conversation

## Zellij Programmatic Control 핵심

- Zellij는 terminal multiplexer를 넘어 programmable runtime으로 진화
- pane lifecycle 제어 가능
- subscribe 기반 realtime event stream 제공
- AI agent orchestration에 적합

핵심 개념:
- pane = controllable process
- subscribe = realtime observable stream

---

# AI Agent 응용 아이디어

## 관찰형 코딩 에이전트
- build pane
- test pane
- lint pane
- repl pane

## 병렬 코드 수정 에이전트
- frontend agent
- backend agent
- review agent

## 실시간 로그 감시 에이전트
- kubectl logs 감시
- docker logs 감시
- 자동 복구

## AI pair programming workspace

## 자율 디버깅 시스템

## terminal-native multi-agent UI

## Agent CI/CD

핵심 철학:
- Zellij = execution fabric
- LLM = cognition
- Registry/Event Bus = orchestration

---

# Pane Registry 설계

핵심:
- pane_id는 ephemeral
- logical agent id는 stable

예시:

```json
{
  "agent_id": "backend-fixer",
  "pane_id": 12,
  "role": "coder"
}
```

중앙 registry daemon 필요.

관리 pane(supervisor pane)는:
- dashboard
- observability
- intervention UI

용도로 사용.

---

# Go 기반 Registry 구조

```go
type Pane struct {
    PaneID string
    AgentID string
    Role string
    State PaneState
}
```

Registry:

```go
type Registry interface {
    RegisterPane(...)
    RemovePane(...)
    GetPane(...)
}
```

Event 기반 구조 추천:

```go
type RegistryManager struct {
    events chan any
    panes map[string]*Pane
}
```

핵심 철학:
- pane는 disposable
- agent는 persistent

---

# Event Bus 설명

- Registry = 현재 상태 저장
- Event Bus = 사건 전달 시스템

예:

```text
pane 7 에서 test 실패
```

↓

```json
{
  "type": "test_failed"
}
```

↓

여러 consumer:
- planner
- logger
- dashboard

로 전달.

Go에서는:

```go
events chan Event
```

정도로 시작 가능.

---

# Registry Daemon이 모든 Pane Subscribe 해야 하나?

추천:
- daemon이 모든 pane output subscribe

이유:
- lifecycle 관리
- health monitoring
- observability
- cleanup

중요:
- 모든 output을 AI에 보내는 건 아님
- daemon이 filtering 수행

구조:

```text
zellij output
    ↓
registry daemon
    ↓
parser
    ↓
semantic event
    ↓
planner
```

---

# 전체 시스템 사용 예시

시나리오:
JWT refresh 로그인 버그 수정

흐름:

1. agentctl task create
2. planner가 작업 분해
3. backend/test/log/coder pane 생성
4. daemon이 subscribe
5. test 실패 감지
6. semantic event 생성
7. planner reasoning
8. coder pane 수정
9. retest
10. success
11. cleanup

최종 구조:

```text
AI planner
    ↓
runtime API
    ↓
registry daemon
    ↓
zellij
```

---

# Registry Daemon 실행 시점

정답:
- registry daemon(agentd)이 먼저 실행
- planner는 그 위에서 동작

구조:

```text
AI planner
    ↑
registry daemon
    ↑
zellij
```

planner는 infrastructure 위에서 동작하는 cognitive layer.

---

# Pane 생성/수정/삭제는 누가 하나?

정답:
YES.

핵심 원칙:

```text
ONLY registry daemon talks to zellij
```

planner는 직접 zellij 호출 금지.

좋은 구조:

```text
planner
   ↓
runtime API
   ↓
registry daemon
   ↓
zellij
```

daemon이:
- lifecycle
- subscriptions
- recovery
- reconciliation

을 모두 관리.

---

# 최종 Orchestration Flow

1. 사용자 요청
2. daemon 실행
3. planner가 분석
4. 필요한 pane 결정
5. runtime.CreatePane()
6. daemon이 zellij pane 생성
7. registry 등록
8. subscribe 연결
9. pane output 발생
10. semantic event 생성
11. planner가 이벤트 수신
12. planner가 다음 행동 결정
13. coder pane 생성
14. SendInput()
15. 수정
16. retest
17. success
18. cleanup

핵심 구조:

```text
AI reasoning
        ↓
terminal orchestration
        ↓
observable execution
```

---

# 최종 핵심 철학

## 역할 분리

### planner
- 무엇을 할지 결정

### daemon
- terminal orchestration
- lifecycle 관리
- registry
- subscriptions

### zellij
- 실제 execution runtime

---

# 전체 구조 요약

```text
             User
               │
               ▼
         AI Planner
               │
               ▼
      Runtime API Layer
               │
               ▼
      Registry Daemon
      ├── Registry
      ├── Event Bus
      ├── Subscriptions
      ├── Runtime
      └── Supervisor
               │
               ▼
             Zellij
               │
               ▼
          Multiple Panes
```

---

# 중요한 아키텍처 원칙

## BAD

```text
planner → zellij
```

## GOOD

```text
planner → daemon API → zellij
```

---

# 핵심 개념 정리

| 개념 | 역할 |
|---|---|
| Pane | 실제 terminal worker |
| Registry | 현재 상태 저장 |
| Event Bus | 사건 전달 |
| Planner | reasoning |
| Daemon | orchestration |
| Zellij | execution runtime |
