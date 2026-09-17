# Daemon SQLite persistence

`zellij-agent daemon serve` restores agent records and the session/tab/pane
registry from SQLite before accepting requests. It keeps serving normal reads
from memory. State changes append immutable records to a shared FIFO under the
producer's memory lock; a single goroutine serializes and commits them in order.
There is no database I/O or JSON serialization in the request's enqueue step.
This is daemon background behavior, so it does not introduce a role command.

## Configuration

The database path is resolved in this order:

1. `zellij-agent daemon serve --db /absolute/path/daemon.db`
2. `$XDG_STATE_HOME/zellij-agent/daemon.db` (absolute XDG path)
3. `~/.local/state/zellij-agent/daemon.db`

The database is separate from the ticket-worker database and Unix socket. WAL
mode creates adjacent `-wal` and `-shm` files. The database and its `.lock` file
are created with mode 0600; new parent directories use 0700. An exclusive lock
prevents two daemons from loading independent memory copies of the same database.
Use separate `--db` and `--socket` paths for isolated daemon instances.

Before opening SQLite or recovering panes, the daemon also takes an exclusive
`<socket>.daemon.lock` for its entire lifetime, including shutdown. Manual and
automatic starts using the same socket fail immediately while that lock is held,
even with different databases or a missing socket. The OS releases the lock on
process exit, including a crash; the lock file remains and must not be deleted.
This is separate from the client's temporary `<socket>.start.lock`. Existing
socket paths are removed only when they are sockets and connecting returns
connection refused; timeouts and other errors do not justify replacing them.

## Stored data

Schema version 1 uses four data tables and one internal metadata table:

| Table | Payload |
| --- | --- |
| `sessions` | Logical ID and timestamps |
| `tabs` | Logical ID, name, timestamps; `parent_id` identifies the session |
| `panes` | Logical and Zellij IDs, tab/session, command, CWD, role, task/agent, status, ownership token, generation, timestamps |
| `agents` | Kind, access mode, PaneID, CWD, state, pin, alias, idle notification preference, detection reason/rule and timestamps |
| `metadata` | Generation high-water mark, preserved after pane deletion |

Rows have a primary ID, parent ID, pane ID and JSON record payload. Agent PaneIDs
have a unique index. Tab storage IDs encode both session and logical tab ID.
Hierarchy and lookup indexes are rebuilt in memory on startup. `PRAGMA
user_version` controls schema compatibility; a newer schema is rejected.

Output snapshots, event history, timers, channels, locks, in-flight requests and
UI selections are not stored. Output-only updates do not enqueue database writes.

## Recovery and shutdown

Agent state starts as `unknown` and is determined again from fresh output.
Recovery checks active sessions (including detached sessions), pane existence,
terminal state, tab identity and the stored ownership token. Missing or mismatched
panes become `lost`; exited panes are removed. Orphaned agent records from an
interrupted registration are removed. Recovery never creates, closes or sends
input to a Zellij pane. Output subscriptions and monitor generations are rebound
for surviving panes. Database records use restart-safe UUID-based agent/pane IDs.

Zellij does not expose the daemon ownership token: it remains a daemon-side
cleanup credential. Session/pane/tab checks cannot distinguish a completely
recreated Zellij session that reuses all three identifiers. This version does not
claim a durable external process identity. Backend inspection failures abort
startup with an error rather than treating unavailable inspection as deletion.
A crash between creating an external pane and registering it can leave an
unmanaged pane; recovery does not claim or close it automatically.

Shutdown stops incoming requests, joins request handlers and reconciliation,
stops subscriptions and monitor timers, then drains the writer before closing
SQLite. Drain has a ten-second deadline; an expired deadline or persistent save
failure is reported and the daemon exits unsuccessfully. Panes remain open.

## Delivery and overload policy

Request success acknowledges the memory change and queue registration, not the
SQLite commit. Commits use transactions of up to 256 changes. Failed batches
stay ahead of later changes and retry every second with a diagnostic. Upserts
and deletes are idempotent. Producer locks are never acquired by the DB writer.

The FIFO is dynamically allocated: producers do not block on a fixed queue
capacity and changes are not dropped on saturation. Consequently a prolonged
storage outage can grow daemon memory usage. This first version has no hard
memory ceiling or overload rejection policy. Abrupt termination can lose all
uncommitted changes. Cross-store registration and external Zellij operations are
not a single atomic transaction; startup reconciliation handles partial records.

Existing daemons must be restarted to use the new binary. Their pre-upgrade
in-memory records are not automatically imported into the new database.

## Validation

`go test ./...` covers the regular suite. Persistence tests exercise concurrent
updates, ordered retries under an actual SQLite write lock, delete/recreate,
restart restoration, generation preservation, exclusive database ownership,
shutdown deadlines, missing/reused panes and inspection failures. Race checks:

```sh
go test -race ./internal/persistence ./internal/registry ./internal/codingagent ./internal/runtime ./internal/cli/daemon ./internal/transport ./internal/zellij
```

## Worktree child panes

The pane JSON payload persists optional `ParentPaneID`, exposed by transport as
`parent_pane_id`. It identifies the managed pane from which the worktree agent
was launched. This relationship is separate from the SQL `parent_id` column,
which continues to identify the containing session/tab. The existing immutable
pane change queue saves it, and startup restores it with the rest of the pane.
Agent records reference their pane through `PaneID`; no duplicate parent field
or parent index is maintained in the agent store. Recovery retains this metadata
when rebinding surviving agents and panes.

This additive optional payload field needs no schema migration: older JSON
records decode with an empty parent (a root agent), so schema version 1 and all
existing data remain valid. Closing a parent does not close its children or
delete their worktrees; the parent ID remains as provenance. Dashboard selection,
popup state and worktree creation progress remain transient.

Dashboard worktree children live in the `worktree-agent` session. Their
`SessionID`, `TabID`, `ZellijTabID`, and parent-derived `TabName` use the existing
pane payload and session/tab indexes; `ParentPaneID` may refer to a pane in
another session. No schema migration is needed. The start request's
`target_session` and runtime `EnsureSession` are transient launch options, not
additional stored fields. Recovery uses the persisted destination location.
Subsequent worktree launches reuse a live child's tab for the same parent and
destination session. This grouping is reconstructed from persisted
`ParentPaneID` and pane location records, then checked against live Zellij panes;
there is no extra stored mapping or migration. `ReuseParentTab` and the runtime
creation lock are transient. Concurrent launches serialize sibling lookup and
registration so the first children do not create duplicate tabs. If no live
child remains, the next launch creates a new tab.

## Ticket worker agent selection

`ticket-worker start` resolves `--default-agent` over the project's
`default_agent` config (legacy configs default to `codex`) and includes the
resolved value in the ticket-manager pane command. Existing execution-plan and
pane-command persistence retain this argument; no new registry field or database
schema is introduced. The manager uses this agent for all newly claimed tickets.
Ticket `agent` values remain unchanged as registration metadata. Existing worker
panes retain their original commands. Dashboard picker state is transient, and
choosing an agent does not rewrite the project config or ticket records.
