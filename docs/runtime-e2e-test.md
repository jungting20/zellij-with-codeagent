# Runtime E2E Test

`TestE2ECreateTabAndFourPanesPrintRegistry`는 실제 Zellij session에 탭 1개와 pane 4개를 만들고, `RuntimeService`가 관리하는 registry 내용을 출력하는 수동 확인용 테스트다.

`TestE2EClosePaneWhenManualPhraseObserved`는 실제 Zellij session에 탭 1개와 pane 4개를 만들고, 테스터가 아무 pane에 정해진 문구를 입력하면 해당 pane의 subscribe 출력 이벤트를 감지해 `RuntimeService.ClosePane`으로 그 pane만 닫는 수동 확인용 테스트다.

## What It Does

- 새 Zellij tab `agentd-e2e-four-panes`를 생성한다.
- 첫 pane을 새 tab 안에 만든다.
- 생성된 `ZellijTabID`를 사용해 같은 tab 안에 pane 3개를 추가로 만든다.
- 각 pane의 화면 snapshot에서 marker 문자열을 확인한다.
- `RuntimeService.ListPanes()` 결과를 JSON 형태로 테스트 로그에 출력한다.
- 테스트 종료 시 `ClosePane`이나 `CloseTab`을 호출하지 않는다.

## Run

repo 루트에서 실행한다.

```bash
AGENTD_ZELLIJ_E2E=1 go test ./internal/runtime -run '^TestE2ECreateTabAndFourPanesPrintRegistry$' -v -count=1
```

특정 문구 입력 감지 후 해당 pane만 닫는 흐름은 아래처럼 실행한다.

```bash
AGENTD_ZELLIJ_E2E=1 go test ./internal/runtime -run '^TestE2EClosePaneWhenManualPhraseObserved$' -v -count=1
```

테스트가 `agentd-e2e-close-on-input` 탭과 pane 4개를 만든 뒤 최대 2분 동안 대기한다. Zellij UI에서 생성된 pane 중 하나를 선택해 `close this pane`을 입력하고 Enter를 누르면, 테스트가 `agentd_manual_input:close this pane` 출력이 발생한 pane의 logical `PaneID`를 찾아 그 pane만 닫는다.

특정 Zellij session에 만들고 싶으면 `ZELLIJ_SESSION_NAME`을 같이 지정한다.

```bash
ZELLIJ_SESSION_NAME=my-session AGENTD_ZELLIJ_E2E=1 go test ./internal/runtime -run '^TestE2ECreateTabAndFourPanesPrintRegistry$' -v -count=1
```

```bash
ZELLIJ_SESSION_NAME=my-session AGENTD_ZELLIJ_E2E=1 go test ./internal/runtime -run '^TestE2EClosePaneWhenManualPhraseObserved$' -v -count=1
```

## Output

`-v`를 붙여야 registry JSON 로그가 보인다. 성공하면 대략 다음 형태의 로그가 출력된다.

```text
created e2e-pane-1 -> zellij pane terminal_... in tab ...
created e2e-pane-2 -> zellij pane terminal_... in tab ...
created e2e-pane-3 -> zellij pane terminal_... in tab ...
created e2e-pane-4 -> zellij pane terminal_... in tab ...
runtime registry after creating tab ... (agentd-e2e-four-panes) and 4 panes:
[
  {
    "ID": "e2e-pane-1",
    "ZellijPaneID": "terminal_...",
    "ZellijTabID": ...,
    "TabName": "agentd-e2e-four-panes",
    "Status": "starting"
  }
]
```

`TestE2EClosePaneWhenManualPhraseObserved`는 생성된 pane 목록과 입력 안내를 로그로 출력한다. 성공하면 대략 다음 형태의 로그가 출력된다.

```text
created e2e-close-input-pane-1 -> zellij pane terminal_... in tab ...
created e2e-close-input-pane-2 -> zellij pane terminal_... in tab ...
created e2e-close-input-pane-3 -> zellij pane terminal_... in tab ...
created e2e-close-input-pane-4 -> zellij pane terminal_... in tab ...
type "close this pane" in any pane in tab ... (agentd-e2e-close-on-input); only that pane will be closed
closed pane e2e-close-input-pane-... after observing manual input "close this pane"
```

## Cleanup

`TestE2ECreateTabAndFourPanesPrintRegistry`는 E2E 관찰을 위해 생성한 tab과 pane을 닫지 않는다. 테스트 후 Zellij UI에서 직접 확인하고 필요하면 tab을 닫는다.

`TestE2EClosePaneWhenManualPhraseObserved`는 정해진 문구 입력이 감지된 pane만 닫는다. 나머지 pane과 tab은 E2E 관찰을 위해 남겨두므로, 테스트 후 Zellij UI에서 직접 확인하고 필요하면 tab을 닫는다.

자동 cleanup이 필요한 검증은 `AGENTD_ZELLIJ_INTEGRATION=1`로 실행하는 integration 테스트를 사용한다.

```bash
AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1
```
