# Manual Smoke Test

This flow verifies the current `agentd` + `agentctl` CLI path against a real Zellij session.

## Prerequisites

- Go 1.22 or newer
- Zellij available as `zellij`
- Commands run from the repository root
- `nvim` available if you use the default `examples/plans/agent-role-demo.json`

## 1. Build Local Binaries

```bash
go build -o bin/agentd ./cmd/agentd
go build -o bin/agentctl ./cmd/agentctl
go build -o bin/agent-role ./cmd/agent-role
```

## 2. Start Zellij

```bash
zellij -s agentd-smoke
```

## 3. Start Agentd

In a terminal attached to that session:

```bash
export ZELLIJ_SESSION_NAME=agentd-smoke
./bin/agentd serve --socket /tmp/agentd.sock
```

Expected output:

```text
agentd serving on unix socket /tmp/agentd.sock
```

## 4. Check The Socket

In another terminal:

```bash
./bin/agentctl health --socket /tmp/agentd.sock
./bin/agentctl status --socket /tmp/agentd.sock
```

## 5. Submit The Sample Plan

```bash
./bin/agentctl plan --socket /tmp/agentd.sock --file examples/plans/agent-role-demo.json
```

The plan creates managed panes for `coder`, `network-tracker`, `console-tracker`, and `editor`.

## 6. Inspect Runtime State And Events

```bash
./bin/agentctl status --socket /tmp/agentd.sock
./bin/agentctl events --socket /tmp/agentd.sock --limit 20
```

To watch events live:

```bash
./bin/agentctl events --socket /tmp/agentd.sock --follow
```

## 7. Exercise Pane Control

```bash
./bin/agentctl input --socket /tmp/agentd.sock coder --text $'echo smoke-input-ok\n'
./bin/agentctl snapshot --socket /tmp/agentd.sock coder --full
```

The snapshot should include `smoke-input-ok` after the pane processes the input.

## 8. Cleanup Managed Panes

```bash
./bin/agentctl cleanup --socket /tmp/agentd.sock --task zellij-with-code-agent
./bin/agentctl status --socket /tmp/agentd.sock
```

Cleanup targets only daemon-managed panes for the task. Unmanaged panes in the same Zellij session should remain open.

## Scripted Smoke

After building the local binaries, the same flow can be run with:

```bash
scripts/smoke-agentctl.sh
```

Run it inside a Zellij session or set `ZELLIJ_SESSION_NAME` before invoking the script.

## Runtime Dashboard Smoke

Build the unified binary and immediately register the rebuilt binary on the
custom-cli PATH:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Start a managed workspace in a real Zellij session, then launch the dashboard:

```bash
zellij-agent work --session dashboard-smoke --auto-test "verify the runtime dashboard"
zellij-agent dashboard --socket /tmp/agentd.sock
```

In the dashboard:

1. Use `j`/`k` or the arrow keys to select the `coder` pane.
2. Press `s` and verify that the selected pane output refreshes.
3. Press `i`, type `echo dashboard-smoke-ok`, and press Enter. Press `s` again
   and verify that the output contains `dashboard-smoke-ok`.
4. Press `r` and verify that the status line reports reconciled active/lost
   counts.
5. Press `x`, verify that the prompt names task `dashboard-smoke`, then press
   `y` and confirm that only panes for that task are cleaned up.

Unmanaged panes in the same Zellij session must remain open. If the event
stream closes, the dashboard must show `connection=degraded`; periodic polling,
manual refresh, and runtime actions remain available.
