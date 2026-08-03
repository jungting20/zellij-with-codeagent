# Agent Next Navigation Design

## Goal

Add a one-shot `zellij-agent agent next` command and bind it to `Tab` inside
Zellij's tab mode so one attached client can cycle through managed coding-agent
panes, including panes in different Zellij sessions.

## User Experience

The user enters Zellij tab mode with the existing `Alt+e` binding and presses
`Tab` repeatedly. Each press focuses the next managed coding agent in creation
order. The sequence wraps from the final agent back to the first agent.

Normal-mode `Tab` remains untouched, so completion and navigation inside
shells, Codex, Claude Code, Gemini, and Cursor Agent continue to work. The
existing tab-mode `Tab` binding for `ToggleTab` is replaced by agent cycling.
No previous-agent shortcut is added in this change.

## Architecture

The command is a transport client. It never invokes Zellij directly. A new
daemon endpoint accepts the current Zellij source session and pane and asks the
coding-agent service to focus the next agent. The service resolves the target
agent and delegates the actual session/pane change through `RuntimeService`,
preserving the existing runtime boundary.

Zellij's `Run` keybinding starts the command in a short-lived helper pane. That
helper pane is valid as the source context for switching the attached client,
but it is not a managed coding-agent pane. Therefore target selection does not
try to derive the current agent from `ZELLIJ_PANE_ID`.

Instead, the daemon owns one in-memory navigation cursor containing the most
recently focused agent ID. Both direct `FocusAgent` calls and successful
`FocusNextAgent` calls update that cursor. `FocusNextAgent` serializes target
selection and focus so concurrent requests cannot select the same stale next
position. If the cursor is empty or its agent has disappeared, the first agent
in creation order is selected. The cursor is intentionally daemon-wide; a
daemon restart resets it, and multiple attached clients share it.

## Components

### Coding-agent service

Extend `AgentService` with a next-focus operation that consumes only the Zellij
source context. The service lists agent records in the store's existing stable
order (`CreatedAt`, then agent ID), chooses the next record with wraparound, and
focuses its runtime pane through `RuntimeService.FocusPane`.

The cursor changes only after a successful focus. An empty agent list returns a
specific no-agents error. A target focus failure is returned without advancing
the cursor so callers receive the actual runtime failure.

### Transport

Add `POST /v1/agents/next` before the generic `/v1/agents/{id}/{action}` route.
The request contains `source_session` and `source_zellij_pane_id`; the response
uses the same agent-with-pane shape as the existing focus response. The typed
transport client exposes `FocusNextAgent`.

### CLI and default role

Add:

```text
zellij-agent agent next [--socket PATH --timeout DURATION]
```

The command requires `ZELLIJ_SESSION_NAME` and `ZELLIJ_PANE_ID`, normalizes
numeric pane IDs to `terminal_N`, calls the next-focus endpoint, and prints the
focused agent ID, kind, and logical pane ID on success.

Repository policy requires a default role for non-background features. Add the
one-shot `agent-next` role with matching socket and timeout options. Its role
implementation delegates to the same agent CLI behavior rather than duplicating
navigation logic. The Zellij keybinding still calls `zellij-agent agent next`,
which is the public command selected for this workflow.

### Zellij configuration

Update the local `~/.config/zellij/config.kdl` tab-mode binding from
`ToggleTab` to a short-lived `Run` action:

```kdl
bind "tab" {
    Run "zellij-agent" "agent" "next" {
        floating true
        close_on_exit true
        borderless true
    }
}
```

The binding remains in tab mode after each action, allowing repeated `Tab`
presses without re-entering the mode. The executable is rebuilt and registered
atomically at `~/.config/custom-cli/zellij-agent` before the binding is tested.

## Error Handling

- Missing Zellij environment variables are CLI usage errors and do not contact
  the daemon.
- No managed agents, invalid source context, daemon connection failures, and
  Zellij switching failures return a non-zero CLI exit code with a concise
  stderr message.
- A failed focus does not move the daemon cursor.
- The configuration edit is limited to the existing tab-mode `Tab` binding.

## Testing and Verification

- Coding-agent service tests cover first selection, wraparound, direct-focus
  cursor updates, deleted cursor targets, empty lists, focus failures, and
  serialized concurrent selection.
- Transport handler and client tests cover the new route, request conversion,
  response decoding, and error mapping.
- Agent CLI tests cover options, Zellij context normalization, successful
  output, validation failures, daemon failures, and help text.
- Role catalog, dispatch, and role package tests cover the required
  `agent-next` default role without launching a real Zellij session.
- Run focused package tests, `go test ./...`, build the unified binary, install
  it with the repository's atomic rename sequence, and verify the installed
  help output.
- Manually validate in Zellij 0.44.1 that `Alt+e`, followed by repeated `Tab`,
  cycles across agents in different sessions and wraps to the first agent.

## Non-Goals

- Previous-agent navigation or arbitrary sorting.
- Persistent cursor state across daemon restarts.
- Independent cursors for multiple attached clients.
- A Zellij plugin or changes to the agent dashboard's own key handling.
