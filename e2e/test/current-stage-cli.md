# Current Stage CLI Test

Updated: 2026-06-15

## Current Stage

The project is at the local runtime client stage.

- `agentd serve --socket <path>` runs the daemon transport over a Unix domain socket.
- `internal/transport.Client` wraps the JSON HTTP API.
- `cmd/agentctl` is the testable CLI added for manual client validation.
- Supported `agentctl` commands:
  - `health`
  - `status`
  - `plan`
  - `events`
  - `events --follow`
  - `input`
  - `snapshot`
  - `message`
  - `forward-snapshot`
  - `cleanup`
- The latest full verification command passed:

```bash
go test ./...
```

## Prerequisites

- Go is installed.
- Zellij is installed and available as `zellij`.
- Run commands from the repository root.
- For real pane creation, run inside or against an available Zellij session.

## 1. Start Agentd

Run this in one terminal:

```bash
go run ./cmd/agentd serve --socket /tmp/agentd.sock
```

Expected output:

```text
agentd serving on unix socket /tmp/agentd.sock
```

Keep this process running while using `agentctl`.

## 2. Check Health

Run this in another terminal:

```bash
go run ./cmd/agentctl health --socket /tmp/agentd.sock
```

Expected output shape:

```text
agentd ok (dev)
```

## 3. Inspect Empty Or Current Runtime State

```bash
go run ./cmd/agentctl status --socket /tmp/agentd.sock
```

Expected output shape before creating panes:

```text
no managed panes
managed=0 active=0 terminal=0 running=0 starting=0 error=0
panes: none
```

## 4. Submit A Minimal Execution Plan

This creates two managed panes in a Zellij tab named `agentctl-demo`.

```bash
cat <<'JSON' | go run ./cmd/agentctl plan --socket /tmp/agentd.sock --file -
{
  "session": "agentctl-demo",
  "layout": "triple-horizontal",
  "tabs": [
    {
      "name": "agentctl-demo",
      "panes": [
        {
          "id": "planner",
          "role": "planner",
          "command": ["sh", "-lc", "echo planner-ready; exec sh"]
        },
        {
          "id": "tester",
          "role": "test",
          "command": ["sh", "-lc", "echo test-ready; exec sh"]
        }
      ]
    }
  ]
}
JSON
```

Expected output shape:

```text
request=req_<timestamp> session=agentctl-demo layout=triple-horizontal
tab=agentctl-demo panes=2
- planner role=planner status=starting zellij=terminal_...
- tester role=test status=starting zellij=terminal_...
```

## 5. Inspect Runtime After Plan Creation

```bash
go run ./cmd/agentctl status --socket /tmp/agentd.sock
```

Expected output shape:

```text
managed=2 active=2 terminal=0 running=... starting=... error=0
panes:
- planner role=planner task=agentctl-demo status=...
- tester role=test task=agentctl-demo status=...
```

## 6. Send Input And Snapshot A Pane

```bash
go run ./cmd/agentctl input --socket /tmp/agentd.sock planner --text $'echo input-ready\n'
go run ./cmd/agentctl snapshot --socket /tmp/agentd.sock planner --full
```

Expected output shape from the snapshot:

```text
planner-ready
input-ready
```

## 7. Check Recent Events

```bash
go run ./cmd/agentctl events --socket /tmp/agentd.sock --limit 20
```

Optional event type filter:

```bash
go run ./cmd/agentctl events --socket /tmp/agentd.sock --limit 20 --type raw_output
```

Expected output shape depends on whether Zellij subscription events have arrived:

```text
2026-06-04T... type=raw_output pane=planner task=agentctl-demo message=...
```

or:

```text
events: none
```

Live stream mode is also available:

```bash
go run ./cmd/agentctl events --socket /tmp/agentd.sock --follow --type raw_output
```

Stop it with Ctrl-C after observing the events you need.

## 8. Cleanup Managed Panes

```bash
go run ./cmd/agentctl cleanup --socket /tmp/agentd.sock --task agentctl-demo
```

Expected output shape:

```text
closed=2 failed=0 skipped=0
- closed planner status=closed
- closed tester status=closed
```

## 9. Confirm Cleanup

```bash
go run ./cmd/agentctl status --socket /tmp/agentd.sock
```

Expected result:

- Panes from `agentctl-demo` should be `closed`, `exited`, or otherwise no longer active.
- Unmanaged user panes must not be closed by cleanup.

## Notes

- `agentctl plan --file -` reads JSON from stdin.
- `agentctl plan --file <path>` reads JSON from a file.
- `agentctl plan` accepts either a raw `ExecutionPlanPayload` or a full `/v1/requests` envelope with `type: "execution_plan"`.
