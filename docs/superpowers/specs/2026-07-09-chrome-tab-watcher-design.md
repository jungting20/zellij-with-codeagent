# Chrome Tab Watcher Design

## Goal

`zellij-agent chrome` should start a Chrome tab watcher pane. The watcher launches Chrome with a remote debugging port by default, attaches to that endpoint, treats page targets that already exist at startup as a baseline, and creates a new managed `tab-network` pane only for Chrome tabs opened after the watcher starts.

The watcher must not call Zellij directly. It creates panes by submitting execution plans to the daemon through the local transport client.

## User-Facing Behavior

- `zellij-agent chrome` starts the automation in the current repository directory.
- Chrome is launched by the watcher unless `--no-launch` is passed after `--`.
- Existing Chrome page targets are ignored for pane creation.
- Each newly created Chrome page target gets one `tab-network` pane.
- The generated `tab-network` command attaches to the existing Chrome instance:
  - `zellij-agent role tab-network --port <port> --no-launch --target-id <target-id>`
- Duplicate target events do not create duplicate panes.
- Watcher output should be concise and useful for debugging:
  - startup port/socket/cwd
  - baseline target count
  - new target IDs submitted
  - daemon submission failures

## CLI Shape

Add a new role:

```text
zellij-agent role tab-watcher [options]
```

Options:

- `--port int`: Chrome remote debugging port. Defaults to `9222`.
- `--socket string`: agentd Unix socket path. Defaults to `/tmp/agentd.sock`.
- `--cwd string`: working directory for generated `tab-network` panes. Defaults to the watcher process working directory.
- `--session string`: execution session/task id for generated tab panes. Defaults to `chrome-tabs`.
- `--role-bin string`: executable used to run roles. Defaults to `zellij-agent`; generated pane commands use `<role-bin> role tab-network ...`.
- `--chrome-path string`: Chrome executable path.
- `--user-data-dir string`: Chrome profile directory used when launching Chrome. Defaults to `/tmp/chrome-debug-network-tracker`.
- `--no-launch`: attach to an already running Chrome debug port.
- `--poll-interval duration`: fallback target polling interval. Defaults to `500ms`.

The existing `zellij-agent chrome [options] [-- Chrome watcher options]` remains the entrypoint. Its default plan creates a watcher pane. The arguments after `--` configure Chrome launch or attachment for the watcher. `zellij-agent chrome --no-watch [-- tab-network options]` preserves the current one-pane `tab-network` behavior for direct debugging.

## Architecture

### `cmd/agent-role/tabwatcher`

Create a new role package with `Run(args []string) int`.

Responsibilities:

- Parse watcher options.
- Resolve the working directory.
- Launch Chrome by default using the same path resolution and launch arguments currently used by `tab-network`.
- Connect to Chrome using `chromedp.NewRemoteAllocator`.
- Read startup page targets and store their IDs as baseline.
- Detect newly created page targets after baseline.
- Build one-pane execution plans for new targets.
- Submit those plans through `transport.Client.SubmitExecutionPlan`.
- Keep running until interrupted or its parent context is canceled.

The role should be testable without launching Chrome or daemon by isolating:

- option parsing
- baseline/new target filtering
- execution plan construction
- target event handling with injected target source and submitter interfaces

### `internal/chrome`

Extend the Chrome plan builder so `zellij-agent chrome` can build a watcher plan.

Default plan:

- session: `chrome` unless `--session` is provided
- layout: `single-tab`
- tab name: `chrome`
- pane role: `tab-watcher`
- pane command: configured role command plus `tab-watcher` and watcher options

Generated `tab-network` plans from the watcher should use deterministic IDs derived from target IDs:

```text
chrome-tab-network-<short-target-id>
```

Request IDs should be target-specific:

```text
req_chrome-tab-network-<short-target-id>
```

### `internal/cli/chrome`

`zellij-agent chrome` should continue to:

- resolve cwd
- support `--socket`, `--timeout`, `--cwd`, `--session`, and `--dry-run`
- validate the generated execution plan envelope before submission

The command passes socket, cwd, session, role binary path, and Chrome connection options to the watcher pane.

### Role Catalog And Dispatch

Add `tab-watcher` to:

- `internal/roles/roles.go`
- `internal/cli/role/role.go`
- role tests and external role documentation

## Target Detection

The first implementation uses polling with `chromedp.Targets` every `--poll-interval`. Polling is simple, reliable enough for this workflow, and easy to test. Browser-level CDP target events are out of scope for the first implementation.

Target handling rules:

- Only targets with `Type == "page"` are eligible.
- Startup targets are recorded as seen and never submitted.
- A new target is submitted once, even if it appears in multiple polls.
- Closed targets stay in the seen set so target ID reuse does not create duplicate panes in the same watcher process.

## Daemon Submission Flow

For each new target:

1. Build an execution plan with one tab and one pane.
2. Pane command runs `tab-network` with `--no-launch`, `--port`, and `--target-id`.
3. Submit through `transport.Client.SubmitExecutionPlan`.
4. Log success or failure to watcher stdout/stderr.

The watcher should use the same socket path provided by `zellij-agent chrome`, so it targets the same daemon instance as the parent command.

## Error Handling

- Invalid flags return non-zero and print a concise error.
- Chrome connection failures return non-zero during startup.
- Target polling errors are logged and retried while the context is alive.
- Daemon submission failures for one target are logged but do not stop the watcher.
- If an execution plan submission fails and the same target remains open, the watcher does not retry automatically in the first version. This avoids repeated pane creation attempts and noisy logs. A later retry policy can be added deliberately.

## Compatibility

Existing direct usage of `zellij-agent role tab-network` remains unchanged.

The default `zellij-agent chrome` behavior changes from opening one `tab-network` pane for the first/current target to opening one watcher pane that creates `tab-network` panes for new tabs. `zellij-agent chrome --no-watch` keeps the current behavior and routes arguments after `--` directly to `tab-network`.

## Testing

Add tests before implementation:

- `cmd/agent-role/tabwatcher`
  - parses defaults and custom options
  - marks startup targets as baseline without submitting
  - submits exactly one plan for a new page target
  - ignores non-page targets
  - prevents duplicate submissions for repeated target sightings
  - builds `tab-network --no-launch --target-id <id>` commands
- `internal/chrome`
  - default Chrome plan creates a `tab-watcher` pane
  - `--no-watch` compatibility path creates the existing `tab-network` pane
  - watcher command includes socket, cwd, session, and port
- `internal/cli/chrome`
  - dry-run emits a valid watcher execution plan envelope
  - custom `--socket`, `--cwd`, `--session`, and `-- --port` values are passed through
- `internal/roles` and `internal/cli/role`
  - catalog includes `tab-watcher`
  - role dispatch validates options without launching Chrome

Verification commands:

```sh
go test ./cmd/agent-role/tabwatcher ./internal/chrome ./internal/cli/chrome ./internal/roles ./internal/cli/role
go test ./...
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Manual smoke test:

```sh
zellij-agent chrome --session chrome-watch -- --port 49335
```

Then open a new Chrome tab in the launched/debuggable Chrome instance and verify:

- watcher logs a new target ID
- daemon status shows a new `tab-network` pane for that target
- the pane command includes `--target-id <new-target-id>`
