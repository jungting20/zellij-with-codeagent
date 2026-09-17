# Zellij Agent Runtime

`zellij-with-codeagent` is an MVP Go runtime that lets an agent manage Zellij panes through a daemon-owned boundary. Zellij remains the terminal execution fabric, but `agentd` owns logical pane IDs, registry state, subscriptions, events, reconciliation, cleanup, and introspection.

## Current MVP

- `cmd/agentd` starts the daemon skeleton and wires the in-process runtime service.
- `internal/runtime.RuntimeService` is the primary boundary for callers and future planners.
- `internal/zellij` is the only package that shells out to the Zellij CLI.
- `internal/registry` is the system of record for daemon-managed panes.
- `internal/eventbus` publishes normalized runtime events and retains recent event history.
- `internal/supervisor` builds a read-only status view from runtime introspection.
- `internal/transport` exposes the runtime over local JSON HTTP on a Unix domain socket.

The transport is local-only and still intended for developer validation, but external clients no longer need to call the Go service in process.

## Requirements

- Go 1.22 or newer
- Zellij installed and available as `zellij`
- A running Zellij environment for integration or E2E tests

## Run

For a command-by-command smoke flow covering daemon startup, TUI submission, pane creation, and cleanup, see `docs/zellij-agent-quickstart.md`.

From the repository root:

```bash
go test ./...
```

Build the unified local binary:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
```

The legacy entrypoints are still available as compatibility wrappers when needed:

```bash
go build -o bin/agentd ./cmd/agentd
go build -o bin/agentctl ./cmd/agentctl
go build -o bin/agent-role ./cmd/agent-role
```

Start the current daemon entrypoint:

```bash
./bin/zellij-agent daemon
```

Without subcommands, the entrypoint still prints `agentd daemon skeleton` for the original smoke path.

Start the local transport daemon:

```bash
./bin/zellij-agent daemon serve
```

The `serve` command exposes JSON HTTP over the default Unix socket `/tmp/agentd.sock`. It does not bind a TCP port. Pass `--socket <path>` only when you need an override.
Use `zellij-agent ctl` as the thin command-line client for the local socket:

```bash
./bin/zellij-agent ctl health
./bin/zellij-agent ctl status
./bin/zellij-agent ctl plan --file examples/plans/agent-role-demo.json
./bin/zellij-agent ctl events --limit 20
./bin/zellij-agent ctl events --follow --type raw_output
./bin/zellij-agent ctl input coder --text $'go test ./...\n'
./bin/zellij-agent ctl snapshot coder --full
./bin/zellij-agent ctl cleanup --task feature-auth
```

### Coding Agent Dashboard

Run coding agents through the unified CLI. `agent start` registers the current
Zellij context, runs the selected coding agent in the current terminal, and
closes that pane when the agent exits. It rejects a pane that is already
managed. These commands include each tool's permission-bypass option by
default:

```bash
./bin/zellij-agent agent start codex
./bin/zellij-agent agent start claude --cwd /path/to/project
./bin/zellij-agent agent start gemini -- --model gemini-3
./bin/zellij-agent agent start cursor
./bin/zellij-agent agent start codex --notify-idle -- "Implement the requested change."
```

Pass `--notify-idle` before `--` to opt in to a native voice notification for
that agent. The daemon announces the agent when its detected state changes
from any non-idle state to `idle`. Starts without this option remain silent.

The configured executables are `codex`, `claude`, `agy` for Gemini, and
`agent` for Cursor. Only agents started through this runtime appear in the
dedicated dashboard:

```bash
./bin/zellij-agent agent dashboard
```

`ticket-worker start` is unchanged: it still creates ticket-manager and worker
panes through execution plans and `CreatePane` requests.

Press `w` on a dashboard agent to open the worktree menu: `a` creates a
worktree and selects its coding agent, `s` executes a shell command only in the selected agent's active direct
child worktree directories (excluding the parent and other agents' worktrees), and
`m` opens the existing merge flow, and `g` focuses an active direct child in its
`worktree-agent` tab through the existing agent focus API. If no child is
available, the menu shows a message. The shell command runs with `$SHELL -c`
(or `/bin/sh -c`) in each directory, with a five-minute total timeout.
Results show each directory's output and failure status; ↑/↓ scrolls and Esc closes.
The separate repository-wide operation, including the main checkout, is available as
`zellij-agent role agent-worktree-send [--timeout DURATION] <path> <shell-command>`.
All dashboard worktree agents open in the `worktree-agent` Zellij session, in a
new tab named after the parent pane's current title (or its project directory
name if the title is empty). The runtime creates the session in the background
when needed. The first child of each parent creates a tab; later children of that same
parent open as panes in the existing tab. Different parents get separate tabs,
even when their titles match. The parent/child relationship
is retained across sessions and daemon restarts.
The dashboard shows descendants beneath their parent across session/tab boundaries,
in the root parent's PINNED/UNPINNED panel. It hides the `worktree-agent` session
and tab headings; children whose parent is absent remain visible with the parent ID.
Existing sessions are reused. When the target session has no connected client,
the runtime temporarily attaches a sized client while creating the tab, waits
for its terminal pane to appear, and releases that client automatically.

Select a parent agent and press `w`, then `m` to request a child worktree merge
(the direct `m` shortcut also remains available).
If there are multiple child agents, choose one with ↑/↓ and press Enter;
Esc cancels. A single child is selected automatically. Both agents must be
running and idle. The dashboard rechecks the relationship and Git branches,
then sends a merge prompt to the parent through the daemon. “merge 요청 전송됨”
means the request was delivered, not that Git integration has completed.
The prompt asks the parent to report uncommitted changes, resolve conflicts
according to intent, run relevant tests, and retain the worktree and branch.
Only children still registered as agents are selectable. Repeated input is
blocked while sending and for two seconds afterward.

To preview the same prompt without sending it:
`zellij-agent role agent-worktree-merge <parent-path> <child-path>` (requires Git).

Select an agent and press `t` to open its project ticket menu: `ts` starts the
worker-agent picker before starting the ticket manager in the selected agent's
session. The picker preselects the project's `default_agent`; use ↑/↓ and Enter
to start, or Esc to cancel. The choice applies to every worker in that manager
run without rewriting the project config. `ta` opens a prompt
popup (Enter adds; Alt+Enter inserts a newline), and `tl` opens a scrollable
list (arrow keys/PgUp/PgDn; `r` refreshes). Esc closes the popup. Ticket actions
use the selected working directory and require prior `ticket-worker init`.
Added tickets record the selected agent kind as registration metadata, derive title/summary from the first
prompt line, and default to running in the project root without a worktree.

The dashboard separates pinned agents on the left (35%) from the regular
list on the right (65%), with independent selection and scrolling. Use
`Tab` or `Shift+Tab` to switch areas and `Space` to pin or unpin an agent;
the cursor stays in the current area. Press `i` on a selected active agent to
open a prompt popup near that row, `Enter` to send it, or `Esc` to cancel. The dashboard stays open
after sending; failed sends keep the prompt for retry. Press `Shift+I` (`I`) on a
selected agent to compose in Neovim (`nvim` on PATH). Save and quit with `:wq`
to load the saved multiline text into the input popup, then press `Enter` to send
or `Esc` to cancel. `:q!` discards unsaved edits; `:cq` returns an editor error. Press `g` to open lazygit
in the selected pane’s working directory; exit lazygit to return to the dashboard.
The `lazygit` executable must be available on PATH. In the regular list, press `d` to close
the selected agent's pane and terminate its process. This key is inactive
in the pinned area. Below 100 terminal columns, only the
active area is shown, with the same area-switching keys.

Use `1`–`9` to select the correspondingly numbered agent across both areas,
starting with pinned agents and continuing through unpinned agents. Selecting
a number also activates its area. Group headings do not count, and scrolling
does not renumber entries. Use `j`/`k` or the up/down arrow
keys to reach the remaining agents. Press `Enter` to switch to the selected agent's
session and focus its pane, `R` to refresh, and `q` to quit. The dashboard
shows the detected agent state (`idle`, `working`, `blocked`, or `unknown`),
agent kind, project, and time in the current state.

Press `a` on an agent in either area to choose a fixed task alias:
미지정, 구현, 버그 수정, 리팩터링, 테스트, 리뷰, 문서, or 조사.
Use up/down or `j`/`k`, then `Enter` to apply or `Esc` to cancel. Choosing
미지정 clears the alias. The alias appears before the project name, independently
of the detected agent state, and is retained across dashboard refreshes and
reopening while the daemon is running. Like pins, aliases are stored in daemon
memory and reset when the daemon restarts.

The bottom area shows the latest three state changes observed while the
dashboard is open, such as `api-server: 작업 중 → 입력 대기 · 2분 전`.
Events and polling updates are deduplicated, and history survives refreshes
and agent removal for the current dashboard session. Short windows show fewer
entries to keep the selected agent visible.

To cycle directly between managed agents, run `zellij-agent agent next` or
`zellij-agent agent prev` from an attached Zellij pane. By default they visit
every managed agent in forward or reverse creation order. Add `--idle-only`
to visit only agents whose detected state is `idle`, or `--pinned-only` to
visit only pinned agents. Use `--unpinned-only` to skip pinned agents.
Either pin filter can be combined with `--idle-only`; `--pinned-only` and
`--unpinned-only` cannot be combined. A filtered mode silently does nothing when no matching
agent exists. The daemon
keeps one in-memory cursor shared by all clients, so a navigation request from
any session advances the same sequence.

The local Zellij bindings cycle forward: `Alt+u` visits pinned agents,
`Alt+i` visits pinned idle agents, `Alt+o` visits unpinned agents, and
`Alt+p` visits unpinned idle agents.
Outside these shortcuts, `Tab` keeps its normal application behavior.

### Pane-less Agent Navigation Bridge

Install the background bridge plugin from the repository root:

```bash
./scripts/install-agent-next-bridge.sh
```

The installer builds with Rustup toolchain `1.88.0` and the official
`wasm32-wasip1` target, then atomically installs
`agent-next-bridge.wasm` at
`${ZELLIJ_PLUGIN_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/zellij/plugins}/agent-next-bridge.wasm`.
It prints the final absolute path and does not edit Zellij configuration. If
the required Rustup toolchain or target is unavailable, it reports the exact
installation command to run.

Configure one background plugin identity using that absolute `file:` URL in
both `load_plugins` and each `MessagePlugin` binding, with the same
`executable_path`. Use `name "agent-next"` and set `payload` per binding:

| Shortcut | Payload | CLI flags for `zellij-agent agent next` |
| --- | --- | --- |
| `Alt+u` | `pinned-only` | `--pinned-only` |
| `Alt+i` | `idle-and-pinned` | `--pinned-only --idle-only` |
| `Alt+o` | `unpinned-only` | `--unpinned-only` |
| `Alt+p` | `idle-and-unpinned` | `--unpinned-only --idle-only` |

An absent payload or `all` visits all agents; `idle-only` filters by idle state.
Unknown payloads and other message names are ignored. The first load requests
`RunCommands` and `ReadApplicationState`, enforced by Zellij. Before executing,
the bridge checks connected clients and lets only the instance for the lowest
connected client ID run the CLI. This prevents duplicate navigation from plugin
instances Zellij retains after a client disconnects.

When reloading, pass the same configuration as the keybindings, including any
`bridge_revision`. For example, with the local configuration:

```bash
zellij --session "$session" action start-or-reload-plugin \
  "file:$HOME/.config/zellij/plugins/agent-next-bridge.wasm" \
  --configuration "executable_path=$HOME/.config/custom-cli/zellij-agent,bridge_revision=navigation-filters-v2"
```

Attach a client to a detached session before reloading it. Zellij can return
exit status zero while logging `No connected clients, cannot reload plugin`;
check the plugin log rather than treating the CLI exit status as confirmation.

The hidden bridge creates no terminal panes. It briefly queues requests while
checking connected clients, without waiting for command completion or overriding
environment variables.
`agent next` does not read `ZELLIJ_SESSION_NAME` or `ZELLIJ_PANE_ID`.
The daemon selects the next managed agent and the runtime finds the single
connected Zellij client to switch to that agent's pane. If no client or multiple
clients are connected, navigation reports an error instead of choosing a client.
CLI filter flags remain available for direct callers.

Coding-agent records are in-memory. A pane close notification removes its
record immediately. In addition, the daemon reconciles Zellij every two
seconds; if a managed pane no longer exists, runtime reconciliation triggers
the same close observer cleanup. Listing agents also removes any remaining
orphan whose runtime pane is absent.

### Personal Work Launcher

`zellij-agent work` starts a daemon-managed mixed coding workspace for the current repository:

```bash
./bin/zellij-agent work "implement the mixed work command"
```

`--zellij-session` selects the physical Zellij session. When omitted, the CLI
uses its own `ZELLIJ_SESSION_NAME`. The logical `--session` flag remains the
execution task ID. Commands fail before submission when neither source names a
physical Zellij session.

The command creates one Zellij tab with five panes:

- `coder`: interactive Codex session through `zellij-agent role coding-agent <cwd>`, with the goal prefilled for review; press Enter to submit it.
- `test`: test shell prepared with a default command detected from root project markers (Go, npm/pnpm/Yarn, or Rust).
- `review`: non-interactive Codex review assistant seeded with the goal.
- `lazygit`: repository Git UI through `lazygit`.
- `notes`: session notes and useful `zellij-agent ctl` commands.

The runtime waits for the Codex input prompt, up to `--timeout` (15 seconds by default), before pasting the exact trimmed goal without an Enter key. Review or edit the text in Codex, then press Enter when you want the coding session to begin. `--dry-run` exposes the value and readiness marker as the coder pane's `initial_input` and `initial_input_ready_text` without creating a workspace.

Project detection reads only known marker files in the selected working-directory root. By default the test pane suggests the detected command without running it. `--auto-test` runs that detected command once when the pane starts. If markers conflict, `package.json` is malformed, or no Node `test` script exists, the workspace still opens with feedback disabled and an actionable reason in the test and notes panes.

Useful options:

```bash
./bin/zellij-agent work --dry-run --session work-command --zellij-session physical-a "implement the mixed work command"
./bin/zellij-agent work --session work-command --zellij-session physical-a "implement the mixed work command"
./bin/zellij-agent work --cwd /path/to/repo "implement the mixed work command"
./bin/zellij-agent work --socket /tmp/agentd.sock "implement the mixed work command"
./bin/zellij-agent work --timeout 30s "implement the mixed work command"
./bin/zellij-agent work --auto-test "implement the mixed work command"
```

The dry-run envelope makes the distinction visible in its payload:

```json
{
  "session": "work-command",
  "zellij_session": "physical-a"
}
```

The daemon must be running before non-dry-run submission:

```bash
./bin/zellij-agent daemon serve
```

### Chrome Tab Watcher

`zellij-agent chrome` submits a watcher pane that tracks newly opened Chrome
tabs. `--zellij-session` selects the physical Zellij session. When omitted, the
CLI uses its own `ZELLIJ_SESSION_NAME`. The logical `--session` flag remains the
execution task ID. Commands fail before submission when neither source names a
physical Zellij session.

Use a dry run to inspect both values without contacting the daemon:

```bash
./bin/zellij-agent chrome --dry-run --session chrome-debug --zellij-session physical-a
```

```json
{
  "session": "chrome-debug",
  "zellij_session": "physical-a"
}
```

Arguments after `--` configure the watcher, for example `--port 9333
--no-launch`. Use `--no-watch` before `--` to create one `tab-network` pane
instead of watching for new tabs.

### Ticket Worker SQLite Queue

`zellij-agent ticket-worker` manages a ticket queue stored inside each Git
project. Initialize it from the project root or any nested directory:

```bash
./bin/zellij-agent ticket-worker init
```

Initialization creates `.zellij-agent/ticket-worker/tickets.db` and
`.zellij-agent/worker/config.yaml` at the Git root, then adds
`.zellij-agent/ticket-worker/` and `.worktrees/` to the root `.gitignore`. It
is idempotent: running it again preserves existing tickets, does not duplicate
the ignore entries, and never overwrites an existing worker config.

The generated worker config contains the coding-agent capacity and polling
cadence:

```yaml
version: 1
default_agent: codex
max_workers: 3
poll_interval: 30s
```

To regenerate the defaults, delete only `.zellij-agent/worker/config.yaml` and
run `ticket-worker init` again. Other ticket commands never create a database
implicitly and report an initialization error until `init` succeeds.

Register a ticket directly:

```bash
./bin/zellij-agent ticket-worker add \
  --title "Add search" \
  --summary "Implement indexed search" \
  --worktree-branch feat/search \
  --worktree \
  --agent claude \
  --prompt $'Implement indexed search.\nRun the complete test suite.'
```

The worktree branch name is required. Pass `--worktree` to run the ticket in a
dedicated worktree on that branch; the option defaults to `false`, so tickets
otherwise run from the repository root. The required prompt is stored with the
ticket and used as the coding-agent instruction. The manager appends its
completion-marker instruction automatically. `--agent` selects `codex`,
`claude`, `gemini`, `cursor`, or `hermes`; it defaults to `codex` when omitted.
This field remains registration metadata; worker execution uses the agent
selected at manager startup.
Queue and lifecycle commands are:

```bash
./bin/zellij-agent ticket-worker list [--status ready] [--no-prompt]
./bin/zellij-agent ticket-worker next
./bin/zellij-agent ticket-worker show ID
./bin/zellij-agent ticket-worker start [--zellij-session NAME] [--default-agent KIND]
./bin/zellij-agent ticket-worker done ID
./bin/zellij-agent ticket-worker cancel ID
./bin/zellij-agent ticket-worker reopen ID
```

If `.zellij-agent/worker/config.yaml` is missing, `start` first runs the same
initialization as `init`, creating the database, default config, and `.gitignore`
entries while preserving existing tickets. Invalid existing configs report an error.

`start` creates one runtime-managed `ticket-manager` pane in a new
`ticket-worker` tab. With no active workers, the manager fills the tab. While
workers are active, the manager occupies the top 50% and all coding-agent
workers share the bottom 50% side by side; Zellij reflows that row whenever a
worker opens or closes. A borderless `zellij:compact-bar` pane remains at the
bottom in both layouts. The manager claims the oldest `ready` tickets, starts
up to `max_workers` coding-agent panes, and continues polling for new tickets.
Tickets without the `worktree` option run directly from the repository root.
For tickets registered with `--worktree`, the manager creates a persistent Git
worktree at `.worktrees/ticket-<ID>` on the ticket's `worktree_branch`. A
missing branch is created from the repository's current `HEAD`; an existing
branch is attached when it is not already checked out elsewhere. A matching
existing worktree is reused after retries or manager restarts. Worktrees and
branches are preserved after completion for review and integration.
Preparation failures requeue the ticket without starting a coding-agent pane.
Every coding-agent created by the manager runs in YOLO mode using the agent
selected at startup: `start --default-agent KIND` overrides `default_agent` in
the worker config. Configs without that field default to `codex`. This choice
applies to all tickets claimed by the manager, including existing queued tickets.
Changing the config affects subsequent starts; it does not change a running
manager. The CLI snapshots the resolved agent in the manager command. The complete ticket instruction, including its
completion marker, is passed to the selected coding agent as its initial CLI
prompt argument; the manager does not paste the prompt or synthesize an Enter
keypress. The manager uses `--zellij-session` when supplied or
`ZELLIJ_SESSION_NAME` when run inside Zellij. The unified CLI automatically
starts the local daemon when needed.

`next` remains the explicit manual claim operation: it atomically moves the
oldest `ready` ticket to `in_progress`. Add `--json` to ticket data commands
other than `init` and `start` for machine-readable output and structured
errors.

### Execution Plan Commands

`zellij-agent ctl plan` accepts either a raw execution plan payload or a full
`/v1/requests` envelope, validates it, and submits it to the runtime. Use
`--zellij-session` to override the physical Zellij session in the input plan.

## Runtime Service Shape

Future planners and developer harnesses should call `RuntimeService`, not Zellij directly:

```go
service := runtime.NewService(runtime.Options{
    Registry:           registry.New(),
    Backend:            zellij.NewBackend(zellij.Options{}),
    SubscriptionRunner: runtime.ExecSubscriptionRunner{},
})

created, err := service.CreatePane(ctx, runtime.CreatePaneRequest{
    ID:            "pane-1",
    TaskID:        "task-1",
    ZellijSession: "physical-a",
    Role:          runtime.PaneRoleTest,
    Command:       []string{"go", "test", "./..."},
    InitialInput:          "Run the assigned task.\n",
    InitialInputReadyText: "›",
})
```

When `InitialInput` is set, `CreatePane` returns success only after the pane
shows `InitialInputReadyText` (when provided) and the runtime delivers the
input. Initialization failure rolls back the new pane; a `cleanup_partial`
error means callers must inspect and finish cleanup.

The core operations are:

- `CreatePane`, `SendInput`, `SnapshotOutput`, and `ClosePane` for managed pane control.
- `SubscribeEvents` and `RecentEvents` for raw output, semantic matcher events, pane close events, subscribe errors, and health changes.
- `InspectPane`, `ListPanes`, and `InspectRuntime` for current daemon-owned state.
- `Reconcile` to align registry state with live Zellij pane metadata.
- `Cleanup` to close daemon-managed panes while preserving unmanaged panes in the same session.

## Daemon persistence

The daemon restores agent settings and pane records from SQLite on startup and
saves memory changes asynchronously. The default database is
`$XDG_STATE_HOME/zellij-agent/daemon.db`, falling back to
`~/.local/state/zellij-agent/daemon.db`. Override it with `daemon serve --db PATH`.
For an isolated daemon, specify both a separate `--socket` and `--db`.
See [persistence behavior and recovery limits](docs/daemon-persistence.md).

## Transport API

`zellij-agent daemon serve` exposes these local endpoints on `/tmp/agentd.sock` by default:

- `GET /v1/health`
- `POST /v1/requests`
- `POST /v1/panes`
- `GET /v1/panes`
- `POST /v1/panes/{pane_id}/input`
- `POST /v1/panes/{pane_id}/snapshot`
- `GET /v1/runtime`
- `GET /v1/events/recent`
- `GET /v1/events/stream`
- `POST /v1/reconcile`
- `POST /v1/cleanup`

Requests and responses use logical daemon IDs (`pane_id`, `task_id`, `agent_id`) as the contract identifiers. Zellij pane IDs are returned only as backend metadata for debugging.

`POST /v1/requests` accepts typed envelopes. The `execution_plan` type creates all panes for one logical session across one or more Zellij tabs:

```json
{
  "type": "execution_plan",
  "request_id": "req_123",
  "payload": {
    "session": "feature-auth",
    "zellij_session": "physical-a",
    "layout": "triple-horizontal",
    "tabs": [
      {
        "name": "feature-auth",
        "panes": [
          { "id": "planner", "role": "planner" },
          { "id": "frontend", "role": "react-dev" }
        ]
      }
    ]
  }
}
```

In v1, `session` is used as `task_id`; `zellij_session` selects the physical
Zellij session; and a tab name defaults to the logical session when omitted.
`layout` is validated metadata (`triple-horizontal` today); physical layout
forcing is deferred.

## Zellij Session Selection

Each execution plan carries its physical Zellij target as `zellij_session`.
At the CLI boundary, explicit `--zellij-session` wins; otherwise the CLI reads
its own `ZELLIJ_SESSION_NAME`. A missing physical session is rejected before
submission. The plan's logical `session` remains the daemon task ID and does
not select a Zellij instance.

`zellij.NewBackend(zellij.Options{Session: "name"})` adds `--session name` to Zellij CLI calls. Tests also honor `ZELLIJ_SESSION_NAME` for real-Zellij integration and E2E runs:

```bash
ZELLIJ_SESSION_NAME=my-session AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1
```

Runtime pane creation rejects requests that omit the physical Zellij session.

## Manual Verification

Automatic real-Zellij integration tests create panes and clean them up:

```bash
AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1
```

Manual E2E tests intentionally leave panes open for observation:

```bash
AGENTD_ZELLIJ_E2E=1 go test ./internal/runtime -run '^TestE2ECreateTabAndFourPanesPrintRegistry$' -v -count=1
```

See `docs/runtime-e2e-test.md` for the close-on-input E2E flow and cleanup notes.

For a current manual CLI flow, see `docs/manual-smoke-test.md`.

## Invariants

- Planners and clients must not invoke Zellij directly. They request outcomes through `RuntimeService`.
- External clients should use the local transport or compatible client wrapper, which still delegates to `RuntimeService`.
- `agentd` is the only owner of Zellij mutations for managed panes: create, input, subscribe, snapshot, reconcile, close, and cleanup.
- Logical `PaneID` values are daemon-owned and stable. Zellij pane IDs are backend identifiers and may disappear or be reused.
- The registry is the system of record for managed runtime state. Zellij is the execution runtime, not the durable state source.
- Unmanaged live Zellij panes may be reported by reconciliation, but they are not adopted or closed by default.
- Subscription lifecycles must follow pane lifecycles. Lost, exited, closed, and cleanup-closed panes should not keep subscribe processes alive.
- Debug views and future transports should expose the same runtime state that planner integrations use.

## Current Limitations

- Local-only, in-memory runtime state.
- No restart persistence beyond what can be rediscovered through reconciliation.
- Rule-based semantic event matchers only.
- Dashboard state is not restored after a daemon restart.
# zellij-with-codeagent
