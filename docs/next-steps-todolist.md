# Next Steps Todo List

Updated: 2026-06-15

## Current Baseline

- [x] `agentd serve --socket <path>` exposes the local Unix socket transport.
- [x] `transport.Client` wraps the JSON HTTP API.
- [x] `agentctl` provides a thin CLI for `health`, `status`, `plan`, `events`, `events --follow`, `input`, `snapshot`, `message`, `forward-snapshot`, and `cleanup`.
- [x] `go test ./...` passes.

## P0. Finish The Current CLI Slice

- [ ] Review and commit the current CLI/runtime documentation slice.
  - Files: `cmd/agentctl/main.go`, `cmd/agentctl/main_test.go`, `README.md`
  - Verify: `go test ./...`

- [x] Add repository hygiene for generated local files.
  - Add `.gitignore` entries for `.DS_Store`, `*.test`, and local socket/log artifacts.
  - Next commit should remove tracked local artifacts with `git rm --cached` in a normal non-destructive flow.
  - Verify: `git status --short` only shows intended source/doc changes and staged artifact removals.

## P1. Make The CLI Easy To Use Manually

- [x] Add a checked-in sample execution plan.
  - Suggested file: `examples/plans/agent-role-demo.json`
  - Move the current `run_planner_test.sh` payload into JSON using `tabs[].panes[]`.
  - Include panes for `coder`, `network-tracker`, `console-tracker`, and `editor`.
  - Verify: `go run ./cmd/agentctl plan --file examples/plans/agent-role-demo.json`

- [x] Replace or simplify `run_planner_test.sh` to call `agentctl plan`.
  - Keep the script as a convenience wrapper, not the source of the API payload.
  - Verify: start `agentd`, run the script, then inspect with `agentctl status`.

- [x] Add build instructions for all local binaries.
  - Commands:
    - `go build -o bin/agentd ./cmd/agentd`
    - `go build -o bin/agentctl ./cmd/agentctl`
    - `go build -o bin/agent-role ./cmd/agent-role`
  - Update `README.md` or `how_to_test.md`.

## P2. Add A Real Manual Smoke Flow

- [x] Create a single manual smoke document.
  - Suggested file: `docs/manual-smoke-test.md`
  - Cover:
    - start Zellij session
    - start `agentd`
    - submit sample plan through `agentctl`
    - inspect status
    - inspect recent events
    - cleanup managed panes
  - Verify the documented commands from a clean terminal.

- [x] Add a smoke script that fails clearly when prerequisites are missing.
  - Suggested file: `scripts/smoke-agentctl.sh`
  - Check for `zellij`, built binaries, and socket availability.
  - Avoid closing unmanaged user panes.

## P3. Improve Runtime Observability

- [x] Add `agentctl events --follow`.
  - Use `transport.Client.StreamEvents`.
  - Print newline summaries as events arrive.
  - Support `--type` filters if the server API gains stream filtering; otherwise filter client-side.
  - Verify with a fake client unit test and a manual real-Zellij event flow.

- [x] Add `agentctl snapshot <pane-id>`.
  - Wrap `POST /v1/panes/{pane_id}/snapshot`.
  - Support `--full` and `--ansi`.
  - Verify with unit tests and a manual pane snapshot.

- [x] Add `agentctl input <pane-id> --text ...`.
  - Wrap `POST /v1/panes/{pane_id}/input`.
  - Keep text handling explicit; avoid shell-escaping surprises.
  - Verify by sending a marker line to a managed pane.

## P4. Prepare For Planner Integration

- [ ] Define a minimal planner adapter contract.
  - Input: natural language goal plus optional working directory.
  - Output: `ExecutionPlanPayload`.
  - Keep LLM reasoning outside `agentd`; only submit structured plans through the transport.

- [ ] Add a deterministic local planner preset before using an LLM.
  - Example: `agentctl plan-preset web-debug --url <url> --cwd <path>`
  - Generate the same payload shape as `examples/plans/agent-role-demo.json`.
  - Verify without any network or LLM dependency.

- [ ] After preset flow is stable, add the first LLM-backed planner experiment.
  - The LLM should produce only a validated execution plan.
  - `agentctl` or a separate planner binary should submit the plan through `transport.Client`.
  - Preserve the invariant: planners and clients never call Zellij directly.

## Recommended Next Task To Start

- [ ] Start with **P4: Define a minimal planner adapter contract**.
  - Input: natural language goal plus optional working directory.
  - Output: `ExecutionPlanPayload`.
  - Keep LLM reasoning outside `agentd`; only submit structured plans through the transport.
