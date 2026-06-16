# Scenario Test Info Panes

## Context

화면 URL이 입력되면 LLM이 해당 URL을 렌더링하는 최상위 Page 소스코드를 찾아 반환한다고 가정한다.

현재 단계에서는 자동 수정 기능은 고려하지 않고, 화면 분석과 기록을 위한 정보 pane 구성에 집중한다.

## Selected Panes

### 1. Browser Preview Pane

- 입력된 URL의 실제 렌더링 화면을 확인한다.
- 소스코드 기준 판단과 실제 화면 상태를 비교하기 위한 기준 pane이다.

### 2. Page Source Pane

- LLM이 반환한 최상위 Page 소스코드를 Neovim으로 연다.
- URL과 직접 연결된 화면 진입점을 확인하는 핵심 pane이다.

### 3. Component Tree Pane

- Page source에서 시작되는 화면 중심 컴포넌트 구조를 보여준다.
- 예: `Page -> Layout -> Section -> Button`
- LSP/import 분석을 통해 내부 tree 구조를 미리 형성해두는 용도다.

### 4. DOM Tree Pane

- 브라우저에 실제 렌더링된 DOM 구조를 보여준다.
- source/component tree와 실제 DOM이 어떻게 대응되는지 확인한다.

### 5. Network Pane

- 해당 페이지에서 발생한 network request/response를 출력한다.
- API endpoint, status, timing, payload 요약을 확인한다.

## Current Target Layout

```text
[Browser Preview]    [Page Source]
[Component Tree]     [DOM Tree]
[Network]
```

## Notes

- 자동 수정, git diff, test runner, coding agent 수정 pane은 현재 범위에서 제외한다.
- 이후 준비 단계에서는 URL에서 Page source를 찾는 route resolver와 component tree 생성 방식을 별도로 정의해야 한다.
