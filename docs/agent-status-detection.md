# Codex, Gemini, Cursor Agent, Claude Code 상태 감지 로직

이 문서는 현재 Herdr 소스 기준으로 네 코딩 에이전트의 상태 감지 방식을 정리한다.

- 조사 기준일: 2026-07-28
- 상태 종류: `idle`, `working`, `blocked`, `unknown`
- 주요 구현:
  - `src/detect/mod.rs`: 에이전트 식별과 화면 감지 진입점
  - `src/detect/manifest.rs`: manifest 로딩, 영역 선택, 규칙 평가
  - `src/pane.rs`: 프로세스 및 화면 감지 루프
  - `src/pane/agent_detection.rs`: 상태 전환 안정화
  - `src/terminal/state.rs`: hook 상태와 화면 상태의 최종 병합
  - `src/detect/manifests/*.toml`: 에이전트별 화면 판정 규칙

## 1. 공통 감지 파이프라인

### 1.1 에이전트 식별

Herdr는 pane shell의 전경 process group을 조사하고 실행 파일명, `argv[0]`, wrapper runtime의 인자를 정규화하여 에이전트를 식별한다.

| 에이전트 | 대표 실행 파일/인식 이름 |
|---|---|
| Codex | `codex` |
| Gemini | `gemini` |
| Cursor Agent | `cursor-agent`, `cursor` |
| Claude Code | `claude`, `claude-code` |

관련 코드: `src/detect/mod.rs:115-237`, `src/pane.rs:478-598`

### 1.2 입력 데이터

식별된 에이전트의 상태는 다음 데이터를 manifest에 전달해 판정한다.

- `screen`: 사용자 스크롤 위치와 분리된 terminal bottom/detection buffer
- `osc_title`: 에이전트가 설정한 OSC terminal title
- `osc_progress`: OSC 9;4 progress payload

기본 감지 루프는 약 300ms 주기로 실행된다. 새 에이전트 프로세스를 식별하면 우선 `idle`을 발행하고 3초의 startup grace 동안 화면 판정을 유예한다. 식별된 프로세스는 기본적으로 5초마다 다시 확인한다.

관련 코드: `src/pane.rs:248-256`, `src/pane.rs:625-905`, `src/pane/agent_detection.rs:5-13`

### 1.3 manifest 규칙 평가

각 규칙은 지정한 `region`에 대해 평가된다.

| region | 의미 |
|---|---|
| `whole_recent` | 최근 detection buffer 전체 |
| `bottom_non_empty_lines(N)` | 아래쪽 N개 비어 있지 않은 행부터 끝까지 |
| `after_last_prompt_marker` | 마지막 Codex `›` prompt 다음 영역. marker가 없으면 전체 |
| `prompt_box_body` | Claude prompt box의 위쪽 border와 다음 horizontal rule 사이 |
| `after_last_horizontal_rule` | 마지막 horizontal rule 다음 영역 |
| `osc_title` | screen이 아닌 OSC title |
| `osc_progress` | screen이 아닌 OSC progress payload |

matcher 의미는 다음과 같다.

- `contains`: 대소문자를 무시하며, 배열의 모든 문자열이 포함되어야 한다.
- `regex`: region 전체에 대해 배열의 모든 정규식이 맞아야 한다.
- `line_regex`: 각 정규식마다 적어도 한 행이 맞아야 한다.
- `all`: 모든 nested gate가 맞아야 한다.
- `any`: 하나 이상의 nested gate가 맞아야 한다.
- `not`: 하나라도 맞으면 해당 규칙을 제외한다.

여러 규칙이 맞으면 가장 높은 `priority`를 선택한다. 우선순위가 같으면 manifest에서 먼저 정의된 규칙이 이긴다. 알려진 에이전트인데 맞는 규칙이 없으면 `default_known_agent_idle_fallback`에 의해 `idle`이 된다.

관련 코드: `src/detect/manifest.rs:414-545`, `src/detect/manifest.rs:1178-1292`

### 1.4 상태 전환과 hook 결합

- `skip_state_update=true`인 규칙은 viewer/menu처럼 live prompt 상태를 알 수 없는 화면을 뜻하며, 현재 상태를 유지한다.
- 명시적인 `visible_idle` 없이 `working -> idle`로 바뀌면 오탐 방지를 위해 100ms 간격으로 재확인하며, 3회 확인 또는 최대 700ms 후 전환한다.
- 네 에이전트 모두 Herdr의 `full_lifecycle_hook_authority` 목록에는 없다. 따라서 hook 상태가 있더라도 화면 감지가 완전히 중단되지는 않는다.
- 최종 상태는 최신의 강한 `visible_blocker`, 유효한 hook 상태, 화면 fallback 상태를 조정해 결정한다. `visible_blocker=true`가 없는 blocked 규칙은 화면 fallback을 blocked로 만들지만, non-blocked hook을 덮는 강한 증거는 아니다.

관련 코드: `src/detect/mod.rs:283-297`, `src/pane/agent_detection.rs:39-69`, `src/terminal/state.rs:1733-1760`, `src/terminal/state.rs:2005-2045`

## 2. Codex

Manifest: `src/detect/manifests/codex.toml`  
Manifest version: `2026.07.18.1`, engine version 2

### 판정 규칙

우선순위 순서대로 평가 결과를 정리하면 다음과 같다.

| 우선순위 | 규칙 | 판정 | 영역 | 핵심 증거 |
|---:|---|---|---|---|
| 1100 | `osc_title_blocked` | `blocked` | `osc_title` | `Action Required` 포함 |
| 1050 | `osc_title_working` | `working` | `osc_title` | 공백 또는 시작 경계에 Codex braille spinner `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` |
| 1000 | `transcript_viewer` | 상태 유지 | `after_last_prompt_marker` | scroll/page/home/end/quit 안내와 이전 메시지 편집 안내 |
| 900 | `live_strong_blocker` | `blocked` | `after_last_prompt_marker` | confirm, submit answer/all, `allow command?` 중 하나 |
| 600 | `weak_blocker` | `blocked` | `whole_recent` | `[y/n]`, `yes (y)`, yes 선택지가 있는 `do/would you...` |
| 500 | `screen_working_fallback` | `working` | 하단 3개 non-empty 행 | `• Working (... esc to interrupt)` 또는 `◦ Working (...)`; conversation interrupted 화면은 제외 |
| 100 | `osc_title_idle` | `idle` | `osc_title` | title이 비어 있지 않고 spinner와 `Action Required`가 없음 |

### 해석상 주의점

- `osc_title_blocked`와 `live_strong_blocker`는 `visible_blocker=true`라서 현재 화면의 강한 사용자 입력 대기 증거다.
- `weak_blocker`는 `blocked`를 반환하지만 `visible_blocker`는 아니다. 오래된 scrollback의 일반적인 yes/no 문구가 hook 상태를 강제로 덮지 않도록 한 약한 fallback 규칙이다.
- transcript viewer에서는 보이는 내용이 현재 live prompt 상태가 아니므로 `unknown`으로 바꾸지 않고 기존 상태를 유지한다.
- OSC title이 정상적으로 제공되면 blocked/working/idle이 화면 텍스트보다 높은 우선순위로 정리된다.
- OSC title이 없고 다른 규칙도 맞지 않으면 알려진 에이전트 fallback으로 `idle`이다.

## 3. Gemini

Manifest: `src/detect/manifests/gemini.toml`  
Manifest version: `2026.06.10.1`, engine version 1

### 판정 규칙

| 우선순위 | 규칙 | 판정 | 영역 | 핵심 증거 |
|---:|---|---|---|---|
| 300 | `apply_or_allow_change` | `blocked` | `whole_recent` | `│ Apply this change`, `│ Allow execution`, confirmation 문구와 `yes`, 또는 `❯ ... yes/allow` 행 |
| 100 | `esc_cancel_working` | `working` | `whole_recent` | `esc to cancel` 포함 |

### 해석상 주의점

- blocked 규칙의 priority가 working 규칙보다 높다. 승인 화면에 `esc to cancel`이 같이 보여도 `blocked`가 이긴다.
- blocked 규칙은 `visible_blocker=true`, working 규칙은 `visible_working=true`다.
- 별도의 명시적 idle UI 규칙이나 OSC 규칙이 없다. 두 규칙이 모두 맞지 않으면 `idle` fallback이다.
- 따라서 Gemini 감지는 승인 문구와 cancel hint가 실제 detection buffer에 유지되는지에 크게 의존한다.

## 4. Cursor Agent

Manifest: `src/detect/manifests/cursor.toml`  
Manifest version: `2026.06.10.1`, engine version 1  
Alias: `cursor-agent`

### 판정 규칙

| 우선순위 | 규칙 | 판정 | 영역 | 핵심 증거 |
|---:|---|---|---|---|
| 320 | `write_file_approval` | `blocked` | 하단 8개 non-empty 행 | `write to this file?` + `proceed (y)` + reject/escape/add-write 증거 중 하나 |
| 300 | `approval_prompt` | `blocked` | `whole_recent` | command approval, `(y) (enter)`, `allow ...(y)`, `keep (n)`, `skip (esc or n)` 등 |
| 100 | `stop_hint_working` | `working` | 하단 6개 non-empty 행 | `ctrl+c to stop` |
| 95 | `background_task_status_working` | `working` | 하단 5개 non-empty 행 | 1 이상의 `background task(s)` |
| 90 | `spinner_working` | `working` | 하단 8개 non-empty 행 | `⬡`, `⬢`, braille spinner 뒤에 영문 `...ing` 상태 |

### 해석상 주의점

- 파일 쓰기 승인은 일반 승인보다 높은 priority를 갖지만 둘 다 결과는 `blocked`이고 `visible_blocker=true`다.
- working 규칙은 모두 화면 하단으로 범위를 제한해 과거 transcript의 stop/spinner 문구가 매칭될 가능성을 줄인다.
- background task count는 0이 아닌 숫자만 working으로 인정한다.
- 명시적인 idle 규칙과 OSC 규칙은 없다. blocker/working 규칙이 없으면 `idle` fallback이다.

## 5. Claude Code

Manifest: `src/detect/manifests/claude.toml`  
Manifest version: `2026.07.13.1`, engine version 2  
Alias: `claude-code`

### 판정 규칙

| 우선순위 | 규칙 | 판정 | 영역 | 핵심 증거 |
|---:|---|---|---|---|
| 1100 | `osc_title_working` | `working` | `osc_title` | title이 braille spinner와 공백으로 시작 |
| 1000 | `transcript_viewer` | 상태 유지 | 하단 3개 non-empty 행 | `showing detailed transcript`와 toggle/scroll/shortcut 안내 |
| 980 | `live_blocked_form` | `blocked` | 마지막 horizontal rule 뒤 | select/cancel + navigation 안내 |
| 980 | `dynamic_workflow_prompt` | `blocked` | `whole_recent` | `run a dynamic workflow?` + `esc to cancel` |
| 975 | `btw_overlay_working` | `working` | 하단 5개 non-empty 행 | `/btw` 행과 `esc to close` 행이 모두 존재 |
| 950 | `live_prompt_box` | `idle` | `prompt_box_body` | `❯` prompt가 있고 selection/navigation UI가 아님 |
| 900 | `model_picker_menu` | 상태 유지 | `whole_recent` | select model/default/cancel 문구, permission/selection form은 제외 |
| 850 | `bash_permission_prompt` | `blocked` | `whole_recent` | proceed 질문 + Bash/expansion/amend/explain 증거 + yes/no 선택 행 |
| 840 | `generic_permission_prompt` | `blocked` | 마지막 horizontal rule 뒤 | proceed/cancel 문구 + 번호가 붙은 yes/no 선택 행 |
| 300 | `legacy_no_prompt_blocker` | `blocked` | `whole_recent` | permission/connection/review/interview 관련 legacy 문구; 빈 `❯` prompt가 있으면 제외 |
| 250 | `osc_title_idle` | `idle` | `osc_title` | title이 `✳ `로 시작 |
| 250 | `osc_progress_idle` | `idle` | `osc_progress` | payload가 `4;0`으로 시작 |

동일 priority 980에서는 manifest에 먼저 있는 `live_blocked_form`이 우선한다. 두 규칙의 결과와 visible flag가 같으므로 최종 상태 차이는 없고 explain의 `matched_rule`만 달라질 수 있다. 동일 priority 250에서는 `osc_title_idle`이 `osc_progress_idle`보다 먼저 선택되며, `osc_progress_idle`에는 `visible_idle=true`가 없다.

### 해석상 주의점

- transcript viewer와 model picker는 live 실행 상태를 증명하지 않으므로 기존 상태를 유지한다.
- `live_prompt_box`는 단순히 `❯`만 보는 것이 아니라 prompt box 내부만 검사하고, select/cancel/navigation UI를 `not` gate로 제외한다.
- Bash와 generic permission 규칙은 질문 문구뿐 아니라 실제 yes/no 선택 행까지 요구해 과거 대화 내용 오탐을 줄인다.
- `legacy_no_prompt_blocker`는 호환용 약한 규칙이며 `visible_blocker=true`가 아니다. 즉 화면 fallback은 blocked가 되지만 강한 blocker 신호로 hook을 덮지는 않는다.
- 명시적인 working OSC title과 idle OSC title/progress를 사용하므로 네 에이전트 중 Codex와 함께 OSC 의존도가 높다.

## 6. 네 에이전트 비교

| 항목 | Codex | Gemini | Cursor Agent | Claude Code |
|---|---|---|---|---|
| OSC title 사용 | blocked/working/idle | 없음 | 없음 | working/idle |
| OSC progress 사용 | 없음 | 없음 | 없음 | idle |
| 명시적 idle 화면 | OSC title | 없음 | 없음 | prompt box, OSC |
| 명시적 blocked | 강/약 blocker 분리 | 승인/적용 UI | 파일/명령 승인 UI | form, workflow, permission, legacy |
| 명시적 working | OSC spinner, working footer | cancel hint | stop/background/spinner | OSC spinner, `/btw` |
| 상태 유지 화면 | transcript viewer | 없음 | 없음 | transcript viewer, model picker |
| 규칙 불일치 | `idle` fallback | `idle` fallback | `idle` fallback | `idle` fallback |

## 7. 실제 상태 판정 확인 명령

실행 중인 pane을 대상으로 다음 순서로 확인한다.

```bash
# Herdr가 최종적으로 노출하는 상태
herdr agent get <target>

# 현재 어떤 규칙이 매칭됐는지와 평가 근거
herdr agent explain <target> --verbose
herdr agent explain <target> --json

# 화면 matcher가 실제로 읽는 detection buffer
herdr agent read <target> --source detection --format text

# 스타일 또는 alternate-screen 영향까지 볼 때
herdr agent read <target> --source detection --format ansi
```

오탐을 분석할 때는 `agent explain --json`의 다음 필드를 확인한다.

- `state`
- `matched_rule.id`, `priority`, `region`
- `visible_idle`, `visible_blocker`, `visible_working`
- `skip_state_update`, `skipped_update_reason`
- `fallback_reason`
- `evaluated_rules[].matched`
- `evaluated_rules[].evidence.region_preview`

## 8. 원본 manifest 위치

- Codex: `src/detect/manifests/codex.toml`
- Gemini: `src/detect/manifests/gemini.toml`
- Cursor Agent: `src/detect/manifests/cursor.toml`
- Claude Code: `src/detect/manifests/claude.toml`
