# Pane-less Agent Next Bridge Design

## 목표

Zellij에서 `Alt+o`와 `Alt+p`를 눌렀을 때 임시 terminal/floating pane을
생성하지 않고 managed coding agent를 순회한다.

- `Alt+o`: 모든 managed coding agent를 생성 순서대로 순회한다.
- `Alt+p`: 상태가 `idle`인 managed coding agent만 순회한다.
- 두 단축키는 현재와 동일하게 `locked`를 제외한 모든 Zellij 모드에서
  동작한다.
- 기존 `zellij-agent agent next` 명령, daemon API, 런타임 포커스 경계를
  재사용한다.

## 현재 문제

현재 `~/.config/zellij/config.kdl`은 두 단축키에 Zellij `Run` 액션을
사용한다. `Run`은 명령을 실행할 때 항상 새 command pane을 생성하므로,
명령이 곧 종료되더라도 키 입력마다 floating pane 생성 비용이 발생한다.

Zellij에는 외부 프로그램을 pane 없이 직접 실행하는 keybinding 액션이
없다. 대신 background plugin은 `MessagePlugin` 메시지를 받은 뒤
`run_command_with_env_variables_and_cwd`로 host 명령을 조용히 실행할 수
있다.

## 사용자 경험

새 Zellij 세션이 시작되면 작은 WASM bridge plugin 하나가 background로
로드된다. 사용자가 `Alt+o` 또는 `Alt+p`를 누르면 화면 구성이나 pane 수가
변하지 않은 채 다음 eligible agent로 포커스가 이동한다.

plugin이 처음 로드될 때 Zellij가 다음 권한 승인을 한 번 요청한다.

- `ReadApplicationState`: 현재 세션과 포커스된 pane을 확인한다.
- `RunCommands`: `zellij-agent agent next`를 background에서 실행한다.

권한 승인 뒤에는 navigation을 위해 별도 UI나 pane을 표시하지 않는다.

## 아키텍처

### Background bridge plugin

저장소에 `plugins/agent-next-bridge` Rust/WASM package를 추가한다. 이 기능은
UI를 제공하지 않는 background logic이므로 별도 default role을 만들지
않는다.

plugin은 Zellij pipe lifecycle의 두 메시지만 처리한다.

- `agent-next`와 payload `all`
- `agent-next`와 payload `idle-only`

알 수 없는 name 또는 payload는 명령을 실행하지 않고 진단 로그만 남긴다.
plugin은 render 결과를 만들지 않으며 visible UI를 갖지 않는다.

### Source context resolution

기존 agent-next CLI는 다음 source context를 요구한다.

- `ZELLIJ_SESSION_NAME`
- `ZELLIJ_PANE_ID`

bridge는 메시지를 처리할 때 Zellij plugin API로 현재 plugin client의
focused pane과 현재 session name을 구한다. focused pane이 terminal이면
`terminal_N` 형식으로 정규화한다. focused pane이 plugin이면 해당 client의
pane history에서 가장 최근 terminal pane을 source로 사용한다. 사용할 수
있는 terminal source가 없으면 요청을 실행하지 않고 로그를 남긴다.

bridge는 선택한 source context를 environment override로 넣고 configured
`zellij-agent` executable을 background에서 실행한다. 이 방식은 plugin
자신의 pane ID가 source로 잘못 사용되는 것을 방지한다.

### Command dispatch

payload에 따른 실행 명령은 다음과 같다.

```text
all       -> zellij-agent agent next
idle-only -> zellij-agent agent next --idle-only
```

각 키 입력은 하나의 명령 실행 요청으로 보존한다. bridge는 연속 입력을
합치거나 버리지 않는다. daemon의 기존 focus mutex가 동시 요청의 selection,
focus, cursor advancement를 직렬화한다.

bridge는 agent selection이나 Zellij focus를 직접 구현하지 않는다. CLI가
Unix socket transport를 통해 daemon의 `/v1/agents/next` endpoint를 호출하고,
daemon이 `RuntimeService`를 통해 session/pane focus를 수행한다.

## Zellij 설정 및 설치

빌드된 WASM은 사용자 Zellij plugin directory의 고정 경로에 설치한다.

```text
~/.config/zellij/plugins/agent-next-bridge.wasm
```

`config.kdl`의 `load_plugins` block에서 이 plugin을 background로 로드한다.
기존 `shared_except "locked"` block의 `Run` 액션 두 개는 동일한 plugin URL을
대상으로 하는 `MessagePlugin` 액션으로 교체한다.

plugin configuration에는 atomic하게 등록된 unified executable의 절대
경로를 전달한다.

```text
~/.config/custom-cli/zellij-agent
```

절대 경로를 사용해 Zellij session의 `PATH` 차이에 따른 실행 실패를
방지한다. WASM 설치도 임시 파일에 쓴 뒤 rename하는 방식으로 수행해 기존
plugin을 부분 파일로 덮어쓰지 않는다.

새 config는 새로 시작한 session에 자동 적용된다. 이미 실행 중인 session은
config reload 뒤 bridge가 background로 로드됐는지 확인하고 smoke test한다.

## 오류 처리

- plugin 권한이 거부되면 명령을 실행하지 않고 Zellij plugin log에 원인을
  남긴다.
- 현재 session, terminal source pane, executable configuration을 구하지
  못하면 명령을 실행하지 않고 로그를 남긴다.
- background command의 non-zero exit와 stderr는 UI pane을 열지 않고 plugin
  log에 기록한다.
- agent가 없거나 idle agent가 없는 경우에는 기존 CLI/daemon 계약을 따른다.
  특히 idle 대상이 없을 때는 성공한 no-op이다.
- 실패한 요청을 자동 재시도하지 않는다. 재시도에 따른 예상치 못한 추가
  cursor advancement를 피하기 위함이다.

## 테스트와 검증

### 자동 테스트

- pipe name/payload가 정확한 argv로 변환되는지 unit test한다.
- `all`, `idle-only`, unknown payload를 모두 검사한다.
- terminal focus, plugin focus의 terminal-history fallback, terminal source 없음
  경로를 검사한다.
- permission 전후에 명령이 중복 실행되지 않는지 검사한다.
- WASM target으로 plugin을 빌드한다.
- 기존 Go 동작의 회귀 여부를 확인하기 위해 `go test ./...`를 실행한다.

### 설정 및 수동 smoke test

- `zellij setup --check`로 config 경로와 parse 성공을 확인한다.
- 새 session에서 bridge 권한을 승인하고 background load를 확인한다.
- `Alt+o`를 반복해 모든 agent가 순회되고 wrap되는지 확인한다.
- `Alt+p`를 반복해 idle agent만 순회되고 대상이 없을 때 no-op인지 확인한다.
- 각 키 입력 전후 `zellij action list-panes --all`을 비교해 terminal pane이
  추가되지 않는지 확인한다.
- normal, pane, tab 등 `locked` 이외 모드에서 두 단축키가 동작하고 locked
  모드에서는 기존처럼 application input으로 전달되는지 확인한다.

## 비목표

- agent selection, cursor 또는 idle filtering 정책 변경
- daemon transport를 Unix socket 외 방식으로 노출
- macOS 전역 단축키 제공
- navigation 성공/실패를 위한 별도 popup 또는 TUI 추가
- `locked` 모드에서 단축키를 가로채는 동작
