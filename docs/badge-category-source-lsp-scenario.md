# Badge Category Source LSP Scenario

## Purpose

URL 형태의 입력 payload를 받아 새 Zellij tab을 만들고, 같은 tab 안에서 다음 두 pane을 확인한다.

- `badge-category-editor`: `agent-role editor`로 resolved source file을 Neovim에서 연다.
- `badge-category-lsp`: 같은 source file을 LSP role로 분석해 component/call tree를 출력한다.

## Scenario Input

```text
/Users/in05908_mac/mysunny/sku-admin-front/sku-manager-front/src/certification/badge/category/ui/logic/BadgeCategoryManagementListContainer.tsx
```

현재 런타임의 `ExecutionPlanPayload` 정식 필드는 `session`, `layout`, `tabs`다. URL과 source resolve 결과는 planner 내부 메타데이터이며, `agentd` 요청 payload에는 포함하지 않는다.

## Prerequisites

- `zellij`, `nvim`, `go`가 설치되어 있어야 한다.
- `/Users/in05908_mac/mysunny/sku-admin-front/sku-manager-front` checkout이 존재해야 한다.
- LSP pane은 `typescript-language-server`가 없으면 `npx` fallback을 사용하므로 Node/npm 실행 환경이 필요하다.

## Execution Plan

실행 가능한 envelope는 [examples/plans/badge-category-source-lsp.json](/Users/in05908_mac/zellij-with-codeagent/examples/plans/badge-category-source-lsp.json)에 있다.

핵심 payload 구조:

```json
{
  "type": "execution_plan",
  "request_id": "req_badge_category_source_lsp",
  "payload": {
    "session": "badge-category-source-lsp",
    "layout": "triple-horizontal",
    "tabs": [
      {
        "name": "badge-category-source-lsp",
        "panes": [
          {
            "id": "badge-category-editor",
            "role": "editor"
          },
          {
            "id": "badge-category-lsp",
            "role": "lsp"
          }
        ]
      }
    ]
  }
}
```

## Run

Repo 루트에서 바이너리를 빌드한다.

```bash
go build -o bin/agentd ./cmd/agentd
go build -o bin/agentctl ./cmd/agentctl
go build -o bin/agent-role ./cmd/agent-role
```

Zellij session 안에서 daemon을 실행한다.

```bash
export ZELLIJ_SESSION_NAME=agentd-test-session
./bin/agentd serve --socket /tmp/agentd.sock
```

다른 터미널에서 plan을 제출한다.

```bash
./bin/agentctl plan --socket /tmp/agentd.sock --file examples/plans/badge-category-source-lsp.json
```

같은 요청을 `/v1/requests`로 직접 보내려면 아래처럼 실행한다.

```bash
curl --unix-socket /tmp/agentd.sock http://localhost/v1/requests \
  -H 'Content-Type: application/json' \
  -d @examples/plans/badge-category-source-lsp.json
```

## Expected Result

- `badge-category-source-lsp` 새 tab이 생성된다.
- `badge-category-editor` pane이 `agent-role editor`를 실행하고, Neovim에서 `BadgeCategoryManagementListContainer.tsx`를 연다.
- `badge-category-lsp` pane이 `agent-role lsp --max-depth 2`를 실행하고 tree 출력을 남긴 뒤 shell을 유지한다.
- `agentctl status`에서 두 managed pane이 같은 task/tab 아래에 보여야 한다.

```bash
./bin/agentctl status --socket /tmp/agentd.sock
```

예상 출력 형태:

```text
panes:
- badge-category-editor role=editor task=badge-category-source-lsp status=...
- badge-category-lsp role=lsp task=badge-category-source-lsp status=...
```

## Cleanup

```bash
./bin/agentctl cleanup --socket /tmp/agentd.sock --task badge-category-source-lsp
```
