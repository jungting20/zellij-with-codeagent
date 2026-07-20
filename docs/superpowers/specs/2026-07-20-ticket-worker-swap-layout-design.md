# Ticket Worker Swap Layout Design

## Goal

Make the `ticket-worker` tab keep its existing `ticket-manager` pane above all
dynamic coding-agent worker panes. The manager occupies the top 50% of the tab.
All currently open workers share the bottom 50% horizontally, and Zellij
reflows that row whenever a worker pane opens or closes.

When no worker is open, the manager uses the whole tab. This change affects
only tabs launched by `zellij-agent ticket-worker start` and does not change
ticket scheduling, worker capacity, prompt delivery, completion detection, or
cleanup behavior.

## Layout Behavior

The ticket-worker tab uses a one-pane base layout and one tiled swap layout.
The intended KDL shape is:

```kdl
layout {
    pane name="ticket-manager"

    swap_tiled_layout name="ticket-worker" {
        tab min_panes=2 split_direction="horizontal" {
            pane size="50%"
            pane size="50%" split_direction="vertical" {
                children
            }
        }
    }
}
```

The base layout has an implicit `exact_panes=1` constraint, so the manager
fills the tab while it is the only tiled pane. Once at least one worker exists,
the `min_panes=2` swap layout places the first pane in the top half and all
remaining panes in the lower `children` container. The outer horizontal split
creates the top and bottom regions; the lower vertical split arranges workers
side by side.

The ticket manager remains the first pane because the execution plan creates
it as the initial command in the new tab. Workers continue to be added later
through `RuntimeService.CreatePane` with `SameTabAsPaneID` pointing to that
manager. Closing the final worker lets Zellij return automatically to the
one-pane base layout.

## Architecture

Pane and layout operations remain behind the existing runtime boundary. The
ticket-worker CLI and manager must not invoke Zellij directly.

Add an optional inline-layout field to the execution-plan tab data path:

```text
ticket-worker plan builder
  -> transport.ExecutionPlanTab.LayoutString
  -> runtime.ExecutionPlanTabSpec.LayoutString
  -> runtime.CreatePaneRequest.LayoutString for the first pane/new tab
  -> zellij.CreateTabRequest.LayoutString
  -> zellij action new-tab --layout-string <KDL> -- <manager command>
```

Only new-tab creation consumes this field. Existing-pane creation does not
accept or apply a layout. The field is optional, so all existing execution
plans retain their current behavior when it is empty.

The existing `ticket-manager` role is the default role for this feature. Its
CLI contract and role catalog metadata do not change.

## Ticket-Worker Plan

`internal/ticketworker` owns the ticket-worker-specific layout string because
the arrangement is part of the ticket-worker launch contract, not a global
runtime default. `BuildStartPlan` attaches it to the single `ticket-worker`
tab while preserving the existing manager command, stable IDs, working
directory, physical Zellij session, and logical task identity.

The plan continues to contain exactly one initial pane. The layout's bare base
pane receives the initial manager command supplied to `zellij action new-tab`;
the layout does not duplicate or construct that command itself.

## Transport and Runtime Changes

Add `LayoutString` to the transport execution-plan tab payload and its runtime
equivalent. Use `layout_string` with `omitempty` in JSON so older payloads and
responses remain unchanged when no inline layout is requested.

During `ApplyExecutionPlan`, copy the first tab specification's layout string
into the first `CreatePaneRequest`. `createBackendPane` forwards it only when
`NewTab` is true. The Zellij backend adds `--layout-string` to `new-tab` before
the optional initial command separator.

The global execution-plan `Layout` field remains a logical plan label such as
`single-tab`; it is not repurposed as KDL. Keeping the new value tab-scoped
also avoids ambiguity for execution plans containing multiple tabs with
different layouts.

## Worker Lifecycle

No manager scheduling code changes are required. The existing lifecycle stays:

1. the execution plan creates and registers the manager anchor in a new tab;
2. the manager claims ready tickets up to `max_workers`;
3. each worker is created in the manager's tab through `SameTabAsPaneID`;
4. Zellij activates or refreshes the swap layout as the tiled-pane count
   changes;
5. completed or interrupted workers are closed through the runtime service;
6. Zellij returns to the base layout after the final worker closes.

The generated default configuration currently limits workers to three, but
the `children` placeholder intentionally supports any positive worker count.

## Error Handling

An empty layout string means the caller requested no custom layout and is not
an error. A non-empty malformed KDL string is rejected by Zellij during tab
creation and propagates through the existing backend, runtime, transport, and
CLI error paths. The runtime then uses its existing execution-plan rollback
behavior.

The ticket-worker layout string is a compile-time constant covered by tests,
so normal users do not provide or parse KDL themselves. No fallback to a
different layout is attempted because silently creating a wrongly arranged tab
would hide a launch-contract failure.

## Testing

Add focused tests at each boundary:

- ticket-worker plan tests assert that its tab contains the expected base pane,
  `min_panes=2`, 50/50 sizes, lower vertical split, and `children` placeholder;
- transport conversion and strict JSON tests assert `layout_string` is accepted,
  forwarded, and omitted when empty;
- runtime execution-plan tests assert only the first new-tab pane receives the
  inline layout and that it reaches `zellij.CreateTabRequest`;
- Zellij command tests assert `new-tab` includes `--layout-string` before the
  manager command and remains unchanged when the field is empty;
- existing ticket-manager tests continue to prove workers target the manager's
  tab and close through the runtime client;
- `go test ./...` and the unified binary build provide regression coverage.

A manual Zellij smoke check may launch a ticket-worker project with zero, one,
two, and three ready tickets and visually confirm automatic reflow, but it is
not required for the normal unit suite because it creates real panes.

## Documentation

Update the README ticket-worker section to describe the manager-above-workers
layout, the 50/50 vertical allocation, and automatic worker reflow. The
external agent-role summary does not need an update because the
`ticket-manager` role name, arguments, purpose, and runtime requirements do not
change.

## Out of Scope

This change does not add configurable layout ratios, worker grids or stacking,
floating panes, user-supplied KDL, runtime layout overrides for existing tabs,
new role commands, changes to `max_workers`, or changes to ticket state
transitions.
