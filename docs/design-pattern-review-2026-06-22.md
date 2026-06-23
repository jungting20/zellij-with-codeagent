# 디자인패턴 관점 소스코드 리뷰

- 날짜: 2026-06-22
- 도구: `review` / `zellij-agent ctl debate`
- 주제: 현재 `zellij-with-codeagent` 소스코드를 디자인패턴 관점에서 검토하고, 현재 설계를 유지할지 리팩터링할지 토론

## 결론

현재 설계는 **유지**하는 것이 좋다. 다만 코어 아키텍처를 갈아엎기보다는, 외곽 계층과 비대해진 CLI 흐름을 중심으로 **점진적 리팩터링**을 진행하는 것이 타당하다.

세 에이전트의 합의는 다음과 같다.

- `runtime.Service`, `zellij.Backend`, `registry.Registry`, `eventbus.Bus`, 작은 서비스 인터페이스 조합은 전반적으로 잘 설계되어 있다.
- 현재 구조에는 다음 패턴들이 비교적 명확하게 적용되어 있다.
  - Facade
  - Ports & Adapters
  - Repository
  - Observer
  - Dependency Injection
  - Strategy
- 전면 재설계나 대규모 rewrite는 현재 비용 대비 이득이 낮다.
- 대신 CLI, 경계 매핑, role dispatch 등 변경 압력이 커지는 지점만 작게 분리하는 방향이 적절하다.

## 주요 이견

가장 큰 이견은 “무엇을 먼저 고칠 것인가”였다.

- Agent B/C는 `internal/cli/ctl/ctl.go`의 비대화를 가장 큰 구조적 문제로 봤다.
  - 라우팅
  - 플래그 파싱
  - 출력 포맷
  - debate 실행 계획
  - pane 대기
  - marker 수집
  - synthesis
  
  위 책임들이 CLI 파일에 몰려 있어 SRP 위반이 명확하다는 의견이다.

- Agent B/C는 `registry.resolvePanePathLocked`의 nil error 반환 가능성을 실제 정확성 결함으로 인정했다.
  - 다만 정상 경로에서는 방어 코드에 가까우므로 구조 리팩터링보다 작은 버그 수정으로 처리하는 것이 적절하다는 의견도 있었다.

- Agent A는 영속성 추상화, 이벤트 신뢰성, shell command builder를 더 적극적으로 제안했다.
  - B/C는 현재 요구사항 기준으로는 YAGNI에 가깝다고 봤다.

- planner가 transport DTO에 의존하는 문제는 설계 냄새로 볼 수 있으나, 당장 분리할 필요성에는 의견 차이가 있었다.

## 가장 강한 주장

### 1. 코어보다 CLI가 문제다

가장 설득력 있는 주장은 `internal/cli/ctl/ctl.go`가 너무 많은 책임을 갖고 있다는 점이다.

현재 CLI 계층이 단순 명령 진입점 역할을 넘어 debate 오케스트레이션까지 들고 있다. 이로 인해 다음 문제가 생긴다.

- SRP 위반
- 테스트 어려움 증가
- 기능 추가 시 `ctl.go` 변경 범위 확대
- CLI와 도메인 오케스트레이션 로직의 결합 증가

따라서 debate 관련 흐름은 `internal/debate` 같은 별도 패키지로 분리하는 것이 좋다.

### 2. registry nil-error 가능성은 작은 비용으로 고쳐야 한다

`registry.resolvePanePathLocked`에서 내부 invariant가 깨졌을 때 nil error로 실패를 숨길 가능성이 있다.

발생 가능성이 낮더라도 repository 내부 상태 불일치를 조용히 통과시키는 것은 정확성 측면에서 위험하다. 수정 비용도 작으므로 우선순위가 높다.

## 약한 가정

토론 중 다음 주장들은 근거가 약하거나 현재 단계에서는 과설계 가능성이 있다고 평가됐다.

- “테스트 커버리지가 높다”는 표현
  - `go test ./...` 통과와 커버리지 평가는 다르다.
- Registry를 지금 인터페이스화하자는 주장
  - 영속성 요구가 없다면 불필요한 추상화가 될 수 있다.
- 이벤트 버스에 일반적인 backpressure를 넣자는 제안
  - 현재 drop-on-full 정책은 관찰 이벤트에는 합리적일 수 있다.
  - 누락 불가 이벤트가 생기면 기존 event bus를 바꾸기보다 별도 lifecycle log/store가 더 적절하다.
- role dispatch 리팩터링
  - 역할이 계속 늘어난다는 전제가 있을 때 가치가 더 크다.

## 권고 리팩터링 순서

1. `registry.resolvePanePathLocked`의 nil error 가능성 수정 및 invariant 테스트 추가
2. `internal/cli/ctl/ctl.go`의 debate 오케스트레이션을 `internal/debate` 같은 패키지로 분리
3. role dispatcher를 `map[string]func([]string) int` 형태와 공통 runner 계약으로 정리
4. planner → transport DTO 의존은 멀티 transport, 영속성, planner 확장 요구가 생길 때 중립 plan 모델로 분리
5. transport mapper 분할, semantic event detector 전략화는 파일/이벤트 수가 더 커질 때 수행

## 최종 권고

**코어 아키텍처는 신뢰하고 보존하되, CLI와 경계 매핑의 비대화만 측정 기반으로 잘라내는 방향**이 가장 타당하다.

즉, 현재 설계는 유지하고 다음 원칙으로 개선한다.

- 대규모 rewrite 금지
- 코어 runtime boundary 보존
- Zellij 직접 호출 우회 금지
- CLI는 얇게 유지
- 오케스트레이션은 별도 패키지로 이동
- 실제 요구가 생기기 전 과도한 추상화는 피하기
