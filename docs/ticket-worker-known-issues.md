# Ticket Worker Known Issues

This document records accepted follow-up work for the initial ticket-worker pane-pool release. These limitations do not block a single workspace started against a clean daemon registry, but they should be addressed before relying on repeated or concurrent workspace launches.

## Repeated starts can collide on logical pane IDs

The initial implementation uses fixed logical IDs for the bootstrap panes and restarts worker launch sequences from one:

```text
ticket-worker-manager
ticket-worker-monitor
ticket-worker-slot-1-0001
```

The runtime registry does not reuse logical pane IDs, including IDs belonging to closed records. A first workspace can run normally against a clean daemon registry, but another `ticket-worker start` against the same daemon can fail when it tries to register an existing ID. This affects both concurrent workspaces and later starts after an earlier workspace finishes.

Follow-up direction:

- Include the generated ticket-worker session ID in manager, monitor, and worker logical pane IDs.
- Pass the session-qualified manager ID as the `same_tab_as_pane_id` anchor.
- Add a test that starts two ticket-worker workspaces against one runtime registry.
- Add restart/adoption behavior separately; surviving worker adoption remains outside the initial scope.

## Dashboard capacity includes bootstrap panes

The task-filtered dashboard currently computes `active/capacity` from every active pane in the task. The manager and monitoring panes share the worker task ID, so a three-worker pool can display `active=5/3` even though the manager correctly limits worker panes to three.

This is a display error, not a pool-capacity violation. The manager still creates a worker command only when a worker slot is empty.

Follow-up direction:

- Compute capacity usage from active panes whose role is exactly `ticket-worker`.
- Test a real composition containing manager, monitor, active workers, and closed workers.

## Invalid marker requests return an internal error

The generic marker-wait endpoint validates empty or multiline markers with an untyped runtime error. Transport therefore maps malformed marker requests to HTTP 500 instead of HTTP 400. A valid project configuration cannot normally produce this request, but direct API callers can.

Follow-up direction:

- Add a runtime sentinel error for invalid output markers.
- Map it to transport `bad_request` and add runtime/server/client coverage.

## Additional YAML documents are ignored

Configuration parsing uses strict known-field validation for the first YAML document but does not verify that the input ends afterward. A second `---` document is ignored instead of rejected.

Follow-up direction:

- Decode once more after the first document and require `io.EOF`.
- Reject a second mapping document while accepting whitespace and comment-only tails.

## Real-Zellij end-to-end validation remains manual

Unit, race, full-suite, build, binary-registration, and dry-run plan checks cover the initial implementation. The dry-run verifies that bootstrap contains exactly manager and monitoring panes and no worker pane before manager startup. A complete real-Zellij flow—create worker, observe the completion marker, close it, and refill the slot—remains a manual verification item.

