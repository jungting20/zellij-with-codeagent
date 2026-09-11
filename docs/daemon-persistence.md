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
