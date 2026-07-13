# Work Goal Prefill Design

Updated: 2026-07-13

## Goal

Make `zellij-agent work "<goal>"` open its interactive coder pane with the supplied goal already visible in the Codex input field. The text must not be submitted automatically: the user reviews it and presses Enter.

This is the first focused increment of the project-adaptive launcher in P2 of `docs/next-steps-todolist.md`. Project detection, command selection, optional-tool checks, flag expansion, and the deterministic feedback loop remain outside this increment.

## User Experience

Given:

```bash
zellij-agent work "fix the parser"
```

the managed coder pane starts the existing interactive Codex role in the selected repository. Its input field contains exactly:

```text
fix the parser
```

The launcher does not append instructions, a newline, or an Enter key. The other work panes retain their existing behavior.

`--dry-run` includes the coder pane's `initial_input` and `initial_input_ready_text` in the execution-plan JSON but does not create a pane or send input.

## Architecture

Initial input is a declarative property of an execution-plan pane rather than an imperative follow-up action owned by the `work` CLI.

- `transport.ExecutionPlanPane` gains an optional `InitialInput` field serialized as `initial_input`.
- `transport.ExecutionPlanPane` also gains an optional `InitialInputReadyText` field serialized as `initial_input_ready_text`.
- `runtime.ExecutionPlanPaneSpec` gains both corresponding fields.
- Transport conversion preserves both fields unchanged.
- `work.BuildPlan` sets `InitialInput` and the Codex input-prompt marker `›` only on the `coder` pane, using the same trimmed goal already used to build the plan.
- `RuntimeService.ApplyExecutionPlan` creates each pane through the existing runtime path, waits until a configured readiness marker appears in the pane snapshot, and then sends non-empty initial input through `RuntimeService.SendInput`.

Readiness waiting is necessary because real-Zellij smoke testing showed that pane creation can complete before the Codex input UI is able to retain pasted text. Immediate delivery is therefore not reliable enough for this feature.

The CLI, planner, and runtime never call Zellij directly. The existing `coding-agent` role continues to launch interactive `codex` without a prompt argument; passing the goal as a Codex positional argument would submit it immediately and violate the review-before-submit requirement.

## Data Flow

1. `internal/cli/work` joins positional goal arguments and trims surrounding whitespace.
2. `internal/work.BuildPlan` places that goal in the coder pane's `InitialInput` field.
3. The execution-plan payload crosses the existing Unix-socket transport.
4. Transport converts `InitialInput` and `InitialInputReadyText` to the runtime pane spec without modifying them.
5. Runtime creates the managed pane through `CreatePane`.
6. When `InitialInputReadyText` is non-empty, runtime polls the pane snapshot every 50 milliseconds until that text is visible or the request context expires.
7. After readiness succeeds, runtime calls `SendInput` once with the exact initial-input string.
8. Because the string has no trailing newline, Codex displays it for review instead of submitting it.

Empty `InitialInput` means no readiness wait or input call. Empty `InitialInputReadyText` retains immediate-delivery behavior for other execution-plan producers. No goal normalization or newline handling belongs in the generic runtime layer.

The work CLI uses a 15-second default request timeout. Its existing `--timeout` override controls the complete request, including pane creation and readiness waiting.

## Lifecycle and Error Handling

Initial-input delivery is part of applying the execution plan. A pane is not considered successfully applied until its non-empty initial input has been sent.

- If pane creation fails, existing execution-plan rollback behavior remains unchanged.
- If the readiness marker does not appear before request cancellation or timeout, applying the plan fails and all panes created for that plan are rolled back.
- Snapshot errors during readiness polling are treated as transient until the context expires; the final error includes the last snapshot failure when one exists.
- If initial-input delivery fails, applying the plan fails and all panes created for that plan are rolled back through runtime cleanup.
- For panes created concurrently within a tab, each worker completes its configured readiness wait and input delivery after creation. Any readiness or delivery failure cancels the remaining work and participates in the same rollback path as a creation failure.
- The returned error identifies the pane whose readiness or initial input failed and wraps the underlying context or runtime error.

The runtime reports successful terminal input delivery, not application-level interpretation. A real-Zellij smoke check covers the Codex-specific display behavior.

## Compatibility

`initial_input` and `initial_input_ready_text` are optional and use `omitempty`, so existing execution-plan producers and stored examples retain their current JSON shape. Existing plans with an absent or empty input do not wait or trigger `SendInput`; plans with input but no readiness marker retain immediate delivery.

The fields are generic because other managed interactive panes may need reviewable startup text later, but this increment populates them only for the work coder pane.

## Testing

Unit tests cover:

- `work.BuildPlan` places the trimmed goal only in the coder pane and does not append a newline.
- `work.BuildPlan` places the Codex prompt marker only in the coder pane.
- Work CLI dry-run output contains `initial_input` and `initial_input_ready_text` on the coder pane.
- Transport-to-runtime conversion preserves both initial-input fields.
- Runtime does not send initial input before the readiness marker appears.
- Runtime sends non-empty initial input exactly once after readiness.
- Runtime does not send input for empty values.
- Runtime rolls back all panes when readiness times out.
- Runtime rolls back all panes when initial-input delivery fails.
- Zellij paste command construction treats leading-hyphen text as positional input rather than CLI options.
- Existing work, transport, runtime, and full repository tests remain green.

The manual Zellij smoke flow builds and installs the unified binary, starts the daemon, and verifies both `zellij-agent work "fix the parser"` and `zellij-agent work -- --help`. In both cases, the coder pane must show the exact text without beginning a Codex response before the user presses Enter.

## Success Criteria

- The exact trimmed goal is visible in the coder pane's Codex input field.
- Codex does not begin processing the goal until the user presses Enter.
- Pane creation waits for the configured Codex readiness marker instead of relying on startup timing.
- A missing readiness marker fails within the request timeout and rolls back the partial workspace.
- Dry-run output accurately describes the initial input.
- Initial-input delivery stays inside the transport and `RuntimeService` boundary.
- A delivery failure cannot leave a successfully reported partial workspace.
