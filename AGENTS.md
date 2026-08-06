# Repository Guidelines

## Project Structure & Module Organization

This is a Go module for a Zellij-backed agent runtime. Command entrypoints live in `cmd/`, including `cmd/zellij-agent` for the current unified binary and compatibility commands such as `cmd/agentd`, `cmd/agentctl`, and `cmd/agent-role`. Shared implementation code lives in `internal/`: runtime orchestration in `internal/runtime`, Zellij CLI integration in `internal/zellij`, local transport in `internal/transport`, planner support in `internal/planner`, and role metadata in `internal/roles`. Commands under `internal/cli` are compositions of roles and form the presentation layer in domain-driven design terms; keep domain behavior in the roles and use CLI commands to expose and coordinate them. Documentation and design notes are under `docs/`; runnable plan examples are under `examples/plans/`; smoke-test scripts are in `scripts/` and the repository root.

## Build, Test, and Development Commands

- `go test ./...`: runs the normal unit test suite.
- `go build -o bin/zellij-agent ./cmd/zellij-agent`: builds the preferred local CLI/daemon binary.
- After changes that require rebuilding the unified binary, run `go build -o bin/zellij-agent ./cmd/zellij-agent`, then register it atomically on the custom-cli PATH with `cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new`, `chmod 755 ~/.config/custom-cli/.zellij-agent.new`, and `mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent`. Do not overwrite the existing executable in place because macOS may kill the replaced binary due to its cached code-signing vnode state.
- `go build -o bin/agentd ./cmd/agentd` and `go build -o bin/agentctl ./cmd/agentctl`: builds legacy compatibility entrypoints.
- `./bin/zellij-agent daemon serve`: starts the local JSON HTTP transport on the default Unix socket, `/tmp/agentd.sock`.
- `./scripts/smoke-agentctl.sh`: runs the daemon/client smoke flow; requires built binaries, Zellij, and Neovim.

## Agent Next Bridge Plugin

- The pane-less `Alt+o`/`Alt+p` Zellij bridge source lives in `plugins/agent-next-bridge/`. Its WASI entrypoint is `plugins/agent-next-bridge/src/main.rs`, and its pure behavior model is `plugins/agent-next-bridge/src/model.rs`.
- Build, test, and atomically install the plugin with `cargo test --manifest-path plugins/agent-next-bridge/Cargo.toml` followed by `./scripts/install-agent-next-bridge.sh`.
- The installer writes the runtime artifact to `~/.config/zellij/plugins/agent-next-bridge.wasm`. Keep Zellij configuration pointed at that installed artifact; do not reference a worktree `target/` path.
- The plugin must remain a `wasm32-wasip1` executable binary exporting `_start`. Building it as a `cdylib`/`rlib` causes Zellij to fail with `could not find exported function`.
- The bridge must invoke the public `zellij-agent agent next` CLI and must not create a transient pane or bypass the daemon/runtime boundary.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on edited Go files and keep imports organized by `go fmt`/`goimports` conventions. Package names are short lowercase nouns, and tests sit beside production files with `_test.go` suffixes. Prefer explicit daemon-owned identifiers such as `PaneID`, `task_id`, and `request_id` when crossing package or transport boundaries. Keep shell scripts strict with `set -euo pipefail`, matching existing scripts.

## Testing Guidelines

Unit tests use Go's standard `testing` package and are named `Test...`. Run `go test ./...` before submitting code. Real Zellij tests are opt-in: use `AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/runtime -run '^TestIntegration' -v -count=1` for integration coverage, and `AGENTD_ZELLIJ_E2E=1` only for manual E2E flows that may leave panes open for inspection.

## Commit & Pull Request Guidelines

Recent history mostly uses short `feat:` commits, sometimes with Korean descriptions. Prefer concise conventional prefixes such as `feat:`, `fix:`, `test:`, or `docs:` followed by a specific summary. Pull requests should describe the runtime behavior changed, list verification commands run, link related docs or issues, and include screenshots or terminal output when pane layout, TUI, or Zellij-visible behavior changes.

## Agent-Specific Instructions

Do not bypass the runtime boundary by calling Zellij directly from planners or clients. Route pane creation, input, snapshots, events, reconciliation, and cleanup through `RuntimeService` or the local transport wrappers.

Except for background logic, every feature addition must begin by creating a default role.
