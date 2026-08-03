# List Selector 통합 설계

## 목표

`~/.config/code-agent-list-selector`의 에이전트 선택 TUI를 현재 Go 모듈로 옮기고, `zellij-agent list-selector`로 실행할 수 있게 한다. 원본의 에이전트 목록, 초기 프롬프트 입력, yolo 토글, 키 조작, 자식 프로세스 종료 코드 전달 동작을 유지한다.

## 검토한 접근

1. 원본 디렉터리를 `internal/list-selector`에 그대로 복사한다. 변경량은 작지만 `package main`을 내부 패키지에서 재사용할 수 없고, 별도 `go.mod`와 바이너리가 현재 모듈 구조에 섞인다.
2. 선택 TUI를 `internal/listselector`로 옮기고 실행 어댑터를 `internal/cli/listselector`에 둔다. 현재 프로젝트의 도메인/표현 계층 구분과 통합 CLI 패턴을 따르면서 원본 동작을 보존할 수 있다.
3. 기존 `internal/cli/agent` TUI에 선택 화면을 합친다. UI 진입점은 줄지만 현재 에이전트 관리 화면과 신규 에이전트 실행 화면의 책임이 섞이고 변경 범위가 커진다.

접근 2를 채택한다.

## 구조

- `internal/listselector`: Bubble Tea 모델, 렌더링, 에이전트 명령 정의와 명령 조립을 소유한다.
- `internal/cli/listselector`: 표준 입출력으로 Bubble Tea 프로그램을 실행하고 오류 및 자식 프로세스 종료 코드를 CLI 종료 코드로 변환한다.
- `cmd/zellij-agent/main.go`: `list-selector` 서브커맨드를 등록하고 사용법에 노출한다.

Go 패키지 이름은 하이픈 없이 `listselector`를 사용한다. 원본의 별도 `go.mod`, `go.sum`, 빌드된 `code-agent-list-selector` 바이너리는 복사하지 않는다. 프로젝트 모듈에 없는 직접 의존성 `github.com/charmbracelet/bubbles`만 추가한다.

## 실행 흐름

사용자가 목록에서 에이전트와 yolo 여부를 선택하고 선택적으로 초기 프롬프트를 입력한다. 모델은 원본과 동일하게 다음 명령을 구성한다.

- `agent`: `zellij-agent agent start agent [prompt]`
- `antigravity`: `zellij-agent agent start agy [--dangerously-skip-permissions] [prompt]`
- `codex`: `zellij-agent agent start codex [--dangerously-bypass-approvals-and-sandbox] [prompt]`
- `claude`: `claude [--dangerously-skip-permissions] [prompt]`

기본 선택은 기존 기본 `agent` 역할이며 yolo는 켜진 상태다. selector 자체는 별도의 runtime pane 역할이 아니라 현재 pane에서 실행되는 전경 CLI이므로 역할 카탈로그에 가짜 pane 역할을 추가하지 않는다. `agent`, `antigravity`, `codex` 실행은 기존 `zellij-agent agent start` 경계를 그대로 사용한다.

## Zellij 처리

원본의 직접 `zellij action rename-pane` 호출은 가져오지 않는다. 관리형 에이전트의 pane 수명주기와 표시는 기존 `agent start` 및 runtime 계층이 소유한다. `claude` 직접 실행도 selector에서 Zellij를 직접 조작하지 않는다.

## 오류와 종료

- Bubble Tea 자체 실행 실패는 표준 오류에 출력하고 종료 코드 1을 반환한다.
- 선택한 명령이 정상 종료하면 selector도 종료 코드 0을 반환한다.
- 자식 명령이 종료 코드와 함께 실패하면 같은 종료 코드를 반환한다.
- 그 밖의 실행 오류는 표준 오류에 출력하고 종료 코드 1을 반환한다.
- `ctrl+c`와 `esc`는 명령을 실행하지 않고 정상 종료한다.

## 테스트

- `internal/listselector`에서 기본 에이전트 명령, 제거된 에이전트 부재, yolo/프롬프트 인자 조립, 포커스와 선택 키 동작을 검증한다.
- `internal/cli/listselector`에서 도움말, 예기치 않은 인자, 프로그램 실행 성공/실패와 종료 코드 전달을 검증한다.
- `cmd/zellij-agent`에서 `list-selector` dispatch와 최상위 사용법을 검증한다.
- 관련 패키지 테스트 후 `go test ./...`와 통합 바이너리 빌드를 실행한다.

## 범위 제외

- 에이전트 목록의 설정 파일화
- 기존 `agent` 관리 TUI와 화면 통합
- selector에서 daemon API를 새로 추가하거나 pane을 직접 생성·변경하는 기능
- 원본 설치 경로의 파일 또는 바이너리 삭제
