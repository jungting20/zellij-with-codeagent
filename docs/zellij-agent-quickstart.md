# Zellij Agent Quickstart

This flow verifies the unified `zellij-agent` CLI from build to daemon startup, TUI submission, pane creation, inspection, and cleanup.

## Prerequisites

- Go 1.22 or newer
- Zellij available as `zellij`
- Commands run from the repository root
- A Zellij session for real pane creation `nvim` available for the editor role

## 1. Build The Unified CLI

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
```

Optional PATH setup:

```bash
export PATH="$PWD/bin:$PATH"
```

## 2. Start A Zellij Session

```bash
zellij -s zellij-agent-smoke
```

Run the remaining commands from panes or terminals attached to this session.

## 3. Start The Daemon

In one Zellij pane:

```bash
export ZELLIJ_SESSION_NAME=zellij-agent-smoke
./bin/zellij-agent daemon serve
```

Expected output:

```text
agentd serving on unix socket /tmp/agentd.sock
```

Leave this command running.

## 4. Check The Daemon

In another Zellij pane:

```bash
./bin/zellij-agent ctl health
./bin/zellij-agent ctl status
```

Expected health output:

```text
agentd ok (dev)
```

### Optional: Start An Agent With An Idle Voice Notification

From a pane attached to Zellij, opt in for one agent with:

```bash
./bin/zellij-agent agent start codex --notify-idle -- "Implement the requested change."
```

`--notify-idle` must appear before `--`; arguments after `--` are passed to the
coding agent. The daemon speaks once whenever this managed agent changes from a
non-idle state to `idle`. Agents started without the option do not produce this
notification.

## 5. Submit A JSON Plan With ctl

Create a normal JSON request envelope:

```bash
cat >/tmp/zellij-agent-quickstart.json <<'JSON'
{
  "type": "execution_plan",
  "request_id": "req_quickstart_json",
  "payload": {
    "session": "quickstart-json",
    "layout": "triple-horizontal",
    "tabs": [
      {
        "name": "quickstart-json",
        "panes": [
          {
            "id": "quickstart-shell",
            "role": "shell",
            "command": [
              "sh",
              "-lc",
              "printf 'quickstart json pane ready\\n'; exec sh"
            ]
          },
          {
            "id": "quickstart-worker",
            "role": "worker",
            "command": [
              "sh",
              "-lc",
              "printf 'quickstart worker ready\\n'; exec sh"
            ]
          }
        ]
      }
    ]
  }
}
JSON
```

Submit it through the local daemon with `ctl plan`:

```bash
./bin/zellij-agent ctl plan --file /tmp/zellij-agent-quickstart.json
```

Expected output:

```text
request=req_quickstart_json session=quickstart-json layout=triple-horizontal
```

`ctl plan` also accepts a raw execution plan payload without the outer `type`, `request_id`, and `payload` envelope. Use `--request-id` when submitting a raw payload and you want a stable request ID.

## 6. Preview The TUI Plan Without Creating Panes

```bash
./bin/zellij-agent planner tui \
  --goal "https://example.com 페이지 소스 열고 네트워크/콘솔 확인해줘" \
  --dry-run
```

The JSON should contain commands that call back into the same binary:

```text
zellij-agent role editor
zellij-agent role network-tracker
zellij-agent role console-tracker
```

## 7. Run TUI And Create Panes

Non-interactive smoke command:

```bash
./bin/zellij-agent planner tui \
  --goal "https://example.com 페이지 소스 열고 네트워크/콘솔 확인해줘" \
  --auto-submit
```

Interactive version:

```bash
./bin/zellij-agent planner tui
```

When prompted, enter:

```text
https://example.com 페이지 소스 열고 네트워크/콘솔 확인해줘
```

Then answer `y` to submit.

This creates managed Zellij panes for:

```text
page-editor
page-lsp
page-network
page-console
```

## 8. Inspect Created Panes

```bash
./bin/zellij-agent ctl status
./bin/zellij-agent ctl events --limit 20
```

Snapshot a JSON-created pane:

```bash
./bin/zellij-agent ctl snapshot quickstart-shell --full
```

Or snapshot a TUI-created pane:

```bash
./bin/zellij-agent ctl snapshot page-console --full
```

Send input to a pane if needed:

```bash
./bin/zellij-agent ctl input page-editor --text $':q\n'
```

## 9. Cleanup

The example URL path `/` maps to the task/session `page-root`.

```bash
./bin/zellij-agent ctl cleanup --task quickstart-json
./bin/zellij-agent ctl cleanup --task page-root
./bin/zellij-agent ctl status
```

Stop the daemon with `Ctrl-C` in the daemon pane.

## Compact Command List

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
zellij -s zellij-agent-smoke
export ZELLIJ_SESSION_NAME=zellij-agent-smoke
./bin/zellij-agent daemon serve
```

In another pane:

```bash
./bin/zellij-agent ctl health
cat >/tmp/zellij-agent-quickstart.json <<'JSON'
{
  "type": "execution_plan",
  "request_id": "req_quickstart_json",
  "payload": {
    "session": "quickstart-json",
    "layout": "triple-horizontal",
    "tabs": [
      {
        "name": "quickstart-json",
        "panes": [
          {
            "id": "quickstart-shell",
            "role": "shell",
            "command": ["sh", "-lc", "printf 'quickstart json pane ready\\n'; exec sh"]
          },
          {
            "id": "quickstart-worker",
            "role": "worker",
            "command": ["sh", "-lc", "printf 'quickstart worker ready\\n'; exec sh"]
          }
        ]
      }
    ]
  }
}
JSON
./bin/zellij-agent ctl plan --file /tmp/zellij-agent-quickstart.json
./bin/zellij-agent planner tui --goal "https://example.com 페이지 소스 열고 네트워크/콘솔 확인해줘" --dry-run
./bin/zellij-agent planner tui --goal "https://example.com 페이지 소스 열고 네트워크/콘솔 확인해줘" --auto-submit
./bin/zellij-agent ctl status
./bin/zellij-agent ctl events --limit 20
./bin/zellij-agent ctl cleanup --task quickstart-json
./bin/zellij-agent ctl cleanup --task page-root
```
