# Ticket Worker 저장소 탭 이름 설계

## 목표

동일한 Zellij 세션에서 여러 저장소의 ticket worker를 동시에 실행할 때 각 탭이 어느 저장소에 속하는지 즉시 구분할 수 있어야 한다.

## 동작

`BuildStartPlan`이 만드는 탭 이름을 고정값 `ticket-worker`에서 `ticket:<repo-name>`으로 변경한다. `<repo-name>`은 정규화된 저장소 절대경로의 마지막 디렉터리명이며 유니코드와 원래 대소문자를 보존한다.

예시:

- `/Users/me/Documents/romance-agent` → `ticket:romance-agent`
- `/Users/me/src/zellij-with-codeagent` → `ticket:zellij-with-codeagent`

저장소 경로는 기존과 같이 절대경로여야 하므로 별도의 fallback 이름은 추가하지 않는다. Zellij가 표시 공간에 맞춰 탭 이름을 처리하도록 애플리케이션 수준의 임의 길이 제한도 추가하지 않는다.

## 변경 범위

탭 표시 이름만 변경한다. 다음 항목은 기존 저장소 경로 해시 기반 식별 방식을 유지한다.

- execution plan session 및 task ID
- ticket manager anchor pane ID
- start request ID
- 저장소별 ticket worker 데이터베이스와 설정 경로
- coding-agent pane ID와 이벤트 격리

이미 실행 중인 탭은 이름을 변경하지 않는다. 변경된 이름은 새로 제출하는 `ticket-worker start` 실행부터 적용된다.

## 구현

`internal/ticketworker/plan.go`에서 정규화한 `root`에 `filepath.Base`를 적용해 탭 이름을 구성한다. 별도 설정이나 CLI 옵션은 추가하지 않는다.

## 검증

`internal/ticketworker/plan_test.go`에서 다음을 검증한다.

- 저장소 basename이 `ticket:<repo-name>` 형식으로 탭 이름에 반영된다.
- 서로 다른 저장소 경로는 각 basename에 맞는 탭 이름을 만든다.
- 기존 session, anchor pane, command, CWD 및 request ID 계약은 유지된다.

관련 Go 패키지 테스트와 `go test ./...`를 실행하고 통합 바이너리를 다시 빌드해 원자적으로 등록한다.
