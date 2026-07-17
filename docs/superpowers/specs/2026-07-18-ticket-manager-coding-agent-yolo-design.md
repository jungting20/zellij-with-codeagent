# Ticket Manager Coding-Agent YOLO Design

## Goal

Make every coding-agent pane created by the `ticket-manager` role run Codex in
YOLO mode, while preserving the current safe default for coding-agent roles
started by other commands or by users directly.

## CLI Contract

Extend the existing role with one optional flag:

```text
agent-role coding-agent [--yolo] <path>
```

`coding-agent <path>` continues to launch Codex without changing its approval
or sandbox settings. `coding-agent --yolo <path>` launches Codex with
`--dangerously-bypass-approvals-and-sandbox`.

The user-facing role option is named `--yolo` to express the requested workflow
without exposing the long Codex-specific flag throughout runtime code. Unknown
flags, a missing path, or extra positional arguments produce the role's usage
error and a non-zero exit code.

## Ticket Manager Integration

The ticket manager continues to create worker panes through `ManagerClient` and
the runtime boundary. Its generated command changes from:

```text
zellij-agent role coding-agent <root>
```

to:

```text
zellij-agent role coding-agent --yolo <root>
```

This behavior is unconditional for ticket-manager workers. No new
`ticket-worker start`, `ticket-manager`, or project configuration option is
added. Other coding-agent consumers retain their existing commands and safe
defaults.

## Implementation Boundaries

Argument parsing and Codex command construction remain in
`cmd/agent-role/codingagent`, following the role boundary. The ticket manager
only selects the role option when constructing its runtime pane request; it does
not invoke Codex or Zellij directly.

Update the role catalog usage and argument metadata so `agent-role roles`
documents the optional `--yolo` flag. Because the role's public arguments
change, update the external agent-role summary document required by the
repository's role workflow.

## Error Handling and Safety

Repository resolution and missing-Codex errors remain unchanged. YOLO mode is
activated only when `--yolo` is explicitly present in a coding-agent role
invocation. The ticket manager is the only existing caller changed to supply
that flag.

The Codex flag deliberately disables approvals and sandboxing. This is accepted
for ticket-manager-created agents by the requested contract and must not become
the default for unrelated coding-agent launches.

## Verification

Focused tests will verify that:

- `coding-agent <path>` builds the existing plain Codex command;
- `coding-agent --yolo <path>` adds exactly
  `--dangerously-bypass-approvals-and-sandbox`;
- invalid coding-agent option and argument combinations fail;
- every worker pane command constructed by the ticket manager includes
  `coding-agent --yolo` before the repository path;
- the role catalog advertises the updated usage and optional flag.

Run focused package tests, `go test ./...`, build the unified
`bin/zellij-agent`, and register it atomically on the custom-cli path according
to `AGENTS.md` because the executable behavior changes.
