# Codex working 상태 trailing-space 감지 수정 설계

## 목표

`zellij-agent agent start codex`로 시작한 Codex가 작업 중일 때 agent dashboard에 `working`으로 표시되게 한다.

## 원인

Zellij의 rendered screen dump는 각 줄을 pane 너비까지 공백으로 채운다. Codex의 실제 작업 표시는 `• Working (... esc to interrupt)` 뒤에 공백이 붙지만, `screen_working_fallback` 규칙은 닫는 괄호 직후의 줄 끝만 허용한다. 그 결과 규칙이 매칭되지 않고 detector가 `default_known_agent_idle_fallback`으로 `idle`을 반환한다.

OSC title 기반 working 규칙도 존재하지만 현재 runtime observation은 rendered screen만 detector에 전달한다. 이번 수정은 확인된 screen fallback 결함만 다루며 OSC 수집 기능은 추가하지 않는다.

## 수정 범위

- 실제 Zellij 출력처럼 `Working` 줄 오른쪽에 공백이 있는 Codex fixture를 추가한다.
- Codex의 `screen_working_fallback` 정규식이 선택적 suffix 뒤의 trailing whitespace를 허용하게 한다.
- 공통 matcher와 runtime snapshot 처리는 변경하지 않는다.
- 다른 coding-agent manifest는 변경하지 않는다.

## 동작 흐름

1. Runtime이 Zellij rendered screen을 수집한다.
2. Codex monitor가 screen을 embedded detector에 전달한다.
3. `bottom_non_empty_lines` 영역에서 공백이 붙은 `• Working (... esc to interrupt)` 줄을 찾는다.
4. `screen_working_fallback`이 매칭되어 agent state가 `working`으로 갱신된다.
5. 작업 표시가 사라지면 기존 idle 확인 절차가 상태를 `idle`로 되돌린다.

## 오류 및 호환성

수정은 Codex working 규칙 하나에만 적용한다. 원본 snapshot, 일반 `line_regex` 의미, 다른 에이전트 감지는 그대로 유지한다. `Conversation interrupted` 제외 조건도 유지한다.

## 테스트와 검증

- trailing whitespace가 있는 실제 형태의 fixture 테스트가 수정 전 실패하고 수정 후 `screen_working_fallback`으로 통과해야 한다.
- 기존 Codex interrupted/unmatched fixture가 계속 `idle`이어야 한다.
- `go test ./internal/codingagent`와 `go test ./...`를 실행한다.
- 통합 바이너리를 빌드하고 원자적으로 `~/.config/custom-cli/zellij-agent`에 등록한 뒤 설치 파일의 실행과 동일성을 확인한다.
