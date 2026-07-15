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

1. Confirm the header reports `LIVE`, the managed pane count, and active/problem
   lifecycle totals without relying on color.
2. Use `j`/`k` in the focused Runtime panel, then press `Tab` and verify focus
   moves to Detail without changing the selected pane.
3. In Detail, use `h`/`l` to switch Output and Events. Use `j`/`k`,
   `PageUp`/`PageDown`, and `g`/`G` to verify independent scrolling.
4. Resize below 90 columns and verify only the focused panel is visible; press
   `Tab` to switch the visible panel. Resize back and verify the selection, tab,
   and scroll position remain stable.
5. Press `?` and verify the help overlay. Close it with `Esc`.
6. Select `coder`, press `i`, verify the overlay names `coder`, enter
   `echo dashboard-smoke-ok`, and press Enter. Refresh the snapshot and confirm
   the output contains `dashboard-smoke-ok`.
7. Press `r` and verify the footer reports reconciled active/lost counts.
8. Press `x`, verify the cleanup overlay names task `dashboard-smoke`, shows its
   managed pane count and task-only scope, then press `y`. Confirm only that
   task's managed panes are cleaned up.
9. If the event stream closes, confirm the header reports `DEGRADED` while
   periodic polling, manual refresh, and runtime actions remain available.

Unmanaged panes in the same Zellij session must remain open.

## Work Goal Prefill Smoke

Build and register the unified binary as described above, then run these commands inside a real Zellij session with the daemon serving `/tmp/agentd.sock`:

```bash
zellij-agent work --dry-run --session goal-prefill-smoke "fix the parser"
zellij-agent work --session goal-prefill-smoke "fix the parser"
zellij-agent work --session hyphen-goal-smoke -- --help
```

In the dry-run JSON, confirm the `coder` pane contains `"initial_input": "fix the parser"` and the other panes omit `initial_input`.

In the created coder pane, confirm `fix the parser` is visible in the Codex input field and no response begins automatically. Edit the text if desired, press Enter once, and confirm Codex begins only then.

Repeat the coder-pane check for `hyphen-goal-smoke`: confirm `--help` is visible as input rather than being interpreted as a Zellij option, and confirm no response begins automatically.

Clean up the managed workspace:

```bash
zellij-agent ctl cleanup --task goal-prefill-smoke
zellij-agent ctl cleanup --task hyphen-goal-smoke
```

### Cleanup and Reuse the Same Work IDs

Use one daemon for both submissions:

```bash
zellij-agent work --socket /tmp/agentd.sock --session work-reuse-smoke "inspect this project"
zellij-agent dashboard --socket /tmp/agentd.sock
```

In the dashboard, select a pane in task `work-reuse-smoke`, press `x`, confirm
with `y`, and wait until the task disappears. Quit the dashboard with `q`, then
submit the same command again without restarting the daemon:

```bash
zellij-agent work --socket /tmp/agentd.sock --session work-reuse-smoke "inspect this project"
```

Confirm the second submission creates all work panes and does not report
`registry record already exists`.

### Project Detection Dry Runs

Project detection reads only marker files at the supplied working-directory
root. These dry runs print execution-plan JSON and do not run project commands
or submit work to the daemon.

```bash
root=$(mktemp -d)
printf 'module example.com/demo\n' > "$root/go.mod"
zellij-agent work --cwd "$root" --dry-run "inspect go defaults"

root=$(mktemp -d)
printf '{"scripts":{"test":"vitest","build":"vite build"}}\n' > "$root/package.json"
printf "lockfileVersion: '9.0'\n" > "$root/pnpm-lock.yaml"
zellij-agent work --cwd "$root" --dry-run "inspect pnpm defaults"

root=$(mktemp -d)
printf '[package]\nname = "demo"\nversion = "0.1.0"\n' > "$root/Cargo.toml"
zellij-agent work --cwd "$root" --dry-run "inspect rust defaults"
```

In each payload, inspect the `test` pane command and the `notes` pane script.
They should show `go test ./...` / `go build ./...`, `pnpm test` / `pnpm
build`, and `cargo test` / `cargo check`, respectively.

Confirm a mixed root disables feedback instead of choosing a profile:

```bash
root=$(mktemp -d)
printf 'module example.com/demo\n' > "$root/go.mod"
printf '{"scripts":{"test":"vitest"}}\n' > "$root/package.json"
zellij-agent work --cwd "$root" --dry-run "inspect mixed markers"
```

The test and notes scripts should contain `multiple project families detected`
and must not contain an executable test command.
