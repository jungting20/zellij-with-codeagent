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
