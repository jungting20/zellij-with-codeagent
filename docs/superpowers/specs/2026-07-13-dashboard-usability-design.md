# Runtime Dashboard Usability Design

## Goal

Improve the existing `zellij-agent dashboard` so a user can understand runtime
health, inspect pane output, and perform safe runtime actions without losing
their place. This is a presentation and interaction upgrade to the current
transport-backed Bubble Tea dashboard; it does not add daemon endpoints or
bypass `RuntimeService`.

The design optimizes for routine supervision rather than only output viewing or
only task summaries. It keeps the existing `session -> task -> tab -> pane`
hierarchy and all current actions.

## Chosen Approach

Use an operations-focused, three-region layout:

```text
+ Runtime Dashboard -- LIVE -- 4 panes / 3 running / 1 error -----+
| Runtime [FOCUSED]       | Pane: coder -- running                 |
| v session-a             | [Output]  Events                       |
|   v task-a              |                                        |
|     v code              | $ go test ./...                        |
|       * coder running   | ok                                     |
|       x tester error    |                              18/42 v   |
|-------------------------+----------------------------------------|
| Refreshed 2s ago                         connection: healthy     |
| up/down move  tab focus  enter toggle  i input  ? help  q quit  |
+------------------------------------------------------------------+
```

This approach preserves the dashboard's familiar split view while making
focus, health, scrolling, and available actions explicit. A task-card redesign
would weaken hierarchy browsing, while an output-first redesign would obscure
runtime-wide health.

## Information Hierarchy

### Header

The header contains the product title, connection state, pane count, and a
compact lifecycle summary derived from the latest successful runtime read.
Statuses are grouped into active (`starting`, `running`), problem (`lost`,
`error`), and inactive (`exited`, `closed`) counts while the individual status
remains visible on pane rows. Unknown statuses contribute to a separate
`unknown` count rather than disappearing.

Connection state uses text and a symbol as well as color. The supported labels
are `CONNECTING`, `LIVE`, and `DEGRADED`.

### Runtime Tree

The left panel keeps the current hierarchy. Group rows show disclosure markers
and child counts. Pane rows show a status symbol, logical pane ID, role, and
status. Selection and keyboard focus are separate concepts: reverse video marks
the selected row, while a highlighted border and `[FOCUSED]` label identify the
panel that receives navigation keys.

Status symbols are textual and therefore useful without color:

- `~` starting
- `*` running
- `!` lost or error
- `-` exited or closed
- `?` unknown

### Detail Panel

The right panel has `Output` and `Events` tabs. Only one tab body is rendered at
a time so pane output receives enough vertical space. The panel title always
shows the selected pane ID, role, and lifecycle status.

Output and events have independent vertical scroll offsets and a `current/total`
position indicator. Selecting a different pane resets its output viewport to
the bottom, which matches terminal-tail usage. Refreshing the same pane keeps
the viewport pinned to the bottom only when it was already at the bottom;
otherwise the visible position remains stable so new output does not interrupt
reading. Event highlighting for the selected pane remains, and `raw_output`
events remain excluded.

### Status and Key Hints

The first footer line shows the latest refresh age, connection health, action
progress or result, and errors. Failed reads retain the last successful data and
display `DEGRADED` with the failure reason. Successful later reads clear the
read error but do not erase a recent action result immediately.

The second footer line shows only keys valid in the current mode and focused
panel. `?` opens a complete help overlay.

## Interaction Model

Normal mode uses these keys:

- `tab`: move focus between the runtime tree and detail panel.
- `j`/`k` or down/up: move the selected row when the tree is focused; scroll the
  active detail tab when the detail panel is focused.
- `pageup`/`pagedown`, `g`, `G`: page, jump to top, or jump to bottom in the
  focused detail tab.
- `enter`: expand or collapse a selected group in the tree.
- `h`/`l` or left/right: switch between Output and Events while detail is
  focused.
- `s`: refresh the selected pane snapshot.
- `i`: open input for an active selected pane.
- `r`: reconcile runtime state.
- `x`: request cleanup for the selected pane's task.
- `R`: refresh runtime status and events.
- `?`: open the help overlay.
- `q` or `ctrl+c`: quit.

Input and cleanup confirmation use bordered overlays over the dashboard rather
than an easy-to-miss footer prompt. The input overlay names the target pane and
shows `Enter send` and `Esc cancel`. The cleanup overlay names the exact task,
shows the pane count, describes the scope, and accepts only `y` to confirm;
`n` or `Esc` cancels. Existing action eligibility and duplicate-submission
guards remain unchanged.

## Responsive Layout

At widths of 90 columns or more, the tree and detail panel render side by side.
The tree receives approximately 36 percent of the available width with a
minimum of 30 columns; the detail panel receives the remainder.

Below 90 columns, only the focused panel body is shown. `tab` switches between
`Runtime` and `Detail`, and the header indicates which view is active. This
avoids vertically stacking two panels into unusably short regions. At very
small heights, the header and footer collapse to one line each before panel
content is sacrificed. Rendering continues to use ANSI-aware clipping and must
not panic at zero dimensions.

## Architecture and State

The transport boundary does not change. `internal/dashboard` continues to use
its narrow `Client` interface for all reads and mutations.

The model gains presentation state with no backend coupling:

- focused panel (`tree` or `detail`)
- selected detail tab (`output` or `events`)
- independent output and event viewport offsets
- whether each viewport is following the bottom
- help-overlay visibility
- timestamp of the last successful refresh

Layout calculations and viewport clipping are isolated from action commands.
`view.go` composes header, panels, overlays, and footer. A focused
`viewport.go` owns visible-line slicing, offset clamping, bottom-follow
behavior, and position labels. `model.go` owns focus and tab transitions while
`actions.go` retains transport mutations and confirmation rules.

Runtime refreshes continue to rebuild the hierarchy and semantic event list.
After state changes, offsets are clamped against the new content length. Pane
selection still triggers a snapshot request. Snapshot completion updates only
the matching pane's content and viewport state, so a late response cannot
move the viewport for a newly selected pane.

## Error Handling

- Initial load renders an explicit loading state instead of an empty runtime.
- An actually empty successful runtime renders `No managed panes` and a short
  next-step hint.
- Refresh and snapshot failures preserve the last successful hierarchy,
  events, output, selection, expansion, tab, focus, and viewport positions.
- A closed event stream marks the header `DEGRADED`; polling and actions remain
  available as they do today.
- Overlays do not disappear on invalid keys. Action failure closes the overlay,
  leaves runtime state unchanged, and displays the error in the footer.
- The UI never relies on color alone to communicate status, focus, or failure.

## Testing

Model tests cover focus switching, panel-specific navigation, output/event tab
switching, help open/close behavior, overlay key handling, and preservation of
focus/tab/viewport state across refreshes.

Viewport tests cover top/bottom jumps, paging, clamping after content shrinks,
bottom-follow behavior after content grows, and independent offsets for output
and events.

View tests cover lifecycle summaries and symbols, focused-panel markers,
selected detail tabs, position indicators, loading and empty states, degraded
errors with retained content, overlay content, wide split layout, narrow
single-panel layout, and zero/tiny dimensions. Assertions continue to verify
that every rendered line fits the configured width and the total output fits
the configured height.

The existing dashboard, CLI, and full Go suites remain regression gates. A
manual Zellij smoke test verifies keyboard focus, scrolling, tab switching,
input, cleanup confirmation, responsive behavior, and degraded polling.

Verification commands are:

```bash
go test ./internal/dashboard ./internal/cli/dashboard ./cmd/zellij-agent
go test ./...
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

The binary copy immediately follows a successful rebuild, as required by the
repository guidelines.

## Scope Boundaries

This change does not add search, filtering, mouse support, persisted
preferences, event replay, stream reconnection, pane creation, or direct Zellij
calls. It does not change cleanup scope or input eligibility. Those additions
can be designed separately after the core supervision workflow is comfortable
and reliable.
