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

`--dry-run` includes the coder pane's `initial_input` in the execution-plan JSON but does not create a pane or send input.

## Architecture

Initial input is a declarative property of an execution-plan pane rather than an imperative follow-up action owned by the `work` CLI.

- `transport.ExecutionPlanPane` gains an optional `InitialInput` field serialized as `initial_input`.
- `runtime.ExecutionPlanPaneSpec` gains the corresponding field.
- Transport conversion preserves the field unchanged.
- `work.BuildPlan` sets `InitialInput` only on the `coder` pane, using the same trimmed goal already used to build the plan.
- `RuntimeService.ApplyExecutionPlan` creates each pane through the existing runtime path and then sends non-empty initial input through `RuntimeService.SendInput`.

The CLI, planner, and runtime never call Zellij directly. The existing `coding-agent` role continues to launch interactive `codex` without a prompt argument; passing the goal as a Codex positional argument would submit it immediately and violate the review-before-submit requirement.

## Data Flow

1. `internal/cli/work` joins positional goal arguments and trims surrounding whitespace.
2. `internal/work.BuildPlan` places that goal in the coder pane's `InitialInput` field.
3. The execution-plan payload crosses the existing Unix-socket transport.
4. Transport converts `InitialInput` to the runtime pane spec without modifying it.
5. Runtime creates the managed pane through `CreatePane`.
6. After creation succeeds, runtime calls `SendInput` once with the exact initial-input string.
7. Because the string has no trailing newline, Codex displays it for review instead of submitting it.

Empty `InitialInput` means no input call. No normalization or newline handling belongs in the generic runtime layer.

## Lifecycle and Error Handling

Initial-input delivery is part of applying the execution plan. A pane is not considered successfully applied until its non-empty initial input has been sent.

- If pane creation fails, existing execution-plan rollback behavior remains unchanged.
- If initial-input delivery fails, applying the plan fails and all panes created for that plan are rolled back through runtime cleanup.
- For panes created concurrently within a tab, each worker sends its pane's initial input immediately after that pane is created. Any delivery failure cancels the remaining work and participates in the same rollback path as a creation failure.
- The returned error identifies the pane whose initial input could not be delivered and wraps the underlying runtime error.

The runtime reports successful terminal input delivery, not application-level interpretation. A real-Zellij smoke check covers the Codex-specific display behavior.

## Compatibility

`initial_input` is optional and uses `omitempty`, so existing execution-plan producers and stored examples retain their current JSON shape. Existing plans with an absent or empty value do not trigger `SendInput`.

The field is generic because other managed interactive panes may need reviewable startup text later, but this increment populates it only for the work coder pane.

## Testing

Unit tests cover:

- `work.BuildPlan` places the trimmed goal only in the coder pane and does not append a newline.
- Work CLI dry-run output contains `initial_input` on the coder pane.
- Transport-to-runtime conversion preserves `InitialInput`.
- Runtime sends non-empty initial input exactly once after pane creation.
- Runtime does not send input for empty values.
- Runtime rolls back all panes when initial-input delivery fails.
- Existing work, transport, runtime, and full repository tests remain green.

The manual Zellij smoke flow builds and installs the unified binary, starts the daemon, runs `zellij-agent work "fix the parser"`, and confirms that the coder pane shows the text without beginning a Codex response before the user presses Enter.

## Success Criteria

- The exact trimmed goal is visible in the coder pane's Codex input field.
- Codex does not begin processing the goal until the user presses Enter.
- Dry-run output accurately describes the initial input.
- Initial-input delivery stays inside the transport and `RuntimeService` boundary.
- A delivery failure cannot leave a successfully reported partial workspace.
