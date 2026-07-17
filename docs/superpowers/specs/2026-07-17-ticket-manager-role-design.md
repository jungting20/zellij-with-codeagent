# Ticket Manager Role Design

## Goal

Add a `ticket-manager` agent role that continuously fills a bounded pool of
coding-agent panes from the project-local ticket queue. The manager renders the
configured per-ticket prompt, subscribes to worker output, recognizes a safe
ticket-specific completion marker, marks the ticket done, closes the pane, and
refills the released slot.

This change creates the role and its runtime behavior. It does not add a new
top-level `ticket-worker start` command or planner-generated launch flow.

## Role Contract

The role is available through both compatible role entrypoints:

```text
agent-role ticket-manager [options] <path>
zellij-agent role ticket-manager [options] <path>
```

`<path>` is required and may identify a file or directory inside the target Git
repository. The role resolves the repository root and uses its existing
`.zellij-agent/ticket-worker/tickets.db` and
`.zellij-agent/worker/config.yaml`.

Options are:

- `--socket`: agentd Unix socket path; defaults to the shared CLI default.
- `--task`: required logical runtime task ID for the manager and its workers.
- `--anchor-pane`: required logical pane ID of the manager pane. New coding
  panes are created in the same Zellij tab as this pane.
- `--zellij-session`: physical Zellij session. An explicit value wins; otherwise
  the existing `ZELLIJ_SESSION_NAME` resolution applies.
- `--role-bin`: executable used to launch child roles; defaults to
  `zellij-agent`.
- `--startup-timeout`: maximum time to wait for the manager anchor and each
  coding-agent input prompt; defaults to `15s` and must be positive.

The role handles interrupt and SIGTERM through a cancelable context. Invalid
arguments, missing initialization artifacts, invalid config, unavailable
runtime transport, or missing required runtime identity produce a concise
stderr diagnostic and a nonzero exit code.

## Repository Surfaces

The role follows the existing role boundaries:

- `internal/roles` owns the `RoleTicketManager` constant and user-facing
  catalog metadata.
- `internal/cli/role` dispatches the role name directly to the role package.
- `cmd/agent-role/ticketmanager` owns flag parsing, repository/config/store
  setup, transport wiring, signals, and process exit mapping.
- `internal/ticketworker` owns prompt rendering, ticket-manager lifecycle,
  queue state changes, slot state, and retry behavior.

Role-specific argument parsing does not enter the shared dispatcher. The role
does not invoke Zellij directly. Pane creation, input, snapshots, event streams,
inspection, and close all use the local transport client and therefore remain
behind `RuntimeService`.

## Manager Startup

Startup proceeds in this order:

1. Resolve the Git root from `<path>`.
2. Open the existing ticket store without creating it implicitly.
3. Load and validate the existing worker config.
4. Create the local transport client.
5. Wait until runtime inspection contains the anchor pane with the requested
   logical pane ID, task ID, physical session, and status `starting` or
   `running`.
6. Establish one runtime event stream.
7. Only after the stream is established, claim tickets and fill worker slots.

The stream is manager-owned and covers all runtime events. The manager filters
`raw_output` by the logical pane IDs in its active slots. Runtime already owns
the underlying per-pane Zellij subscriptions created for managed panes.

No ticket is claimed before anchor readiness and event-stream readiness. A
startup timeout or cancellation creates no coding panes and claims no tickets.

## Slot and Queue Lifecycle

The manager allocates exactly `config.max_workers` slots. Each slot records its
ticket ID, deterministic logical pane ID, and one of these states:

- `empty`
- `starting`
- `working`
- `completing`
- `closing`
- `cleanup_failed`

Initial fill happens immediately after startup. Later fill and retry work runs
on `config.poll_interval`. Filling stops normally when the store reports no
ready tickets.

For an empty slot:

1. Atomically claim the oldest ready ticket with the existing FIFO `Next`
   operation. The ticket becomes `in_progress`.
2. Render `config.prompt_template` with `ID`, `Title`, `Summary`, `SpecPath`, and
   `PlanPath`.
3. Derive the exact marker `ZELLIJ_AGENT_TICKET_DONE <ID>`.
4. Append one manager-owned instruction:

   ```text
   작업을 모두 완료한 뒤 마지막 줄에 따옴표 없이 "ZELLIJ_AGENT_TICKET_DONE <ID>"만 출력하세요.
   ```

5. Create logical pane `ticket-coding-<ID>` in the anchor's tab with role
   `coding-agent`, the requested task and physical session, repository-root
   working directory, and command:

   ```text
   <role-bin> role coding-agent <repository-root>
   ```

6. Poll snapshots until the Codex input marker `›` appears or the startup
   timeout expires.
7. Send the complete rendered prompt through the transport input operation.
8. Mark the slot `working` and continue watching its filtered raw output.

Ticket prompt rendering occurs before pane creation, so a template execution
failure can requeue the ticket without creating a process.

## Completion Detection Without Prompt-Echo False Positives

Completion is accepted only when a trimmed output line equals the entire
ticket-specific marker:

```text
ZELLIJ_AGENT_TICKET_DONE <ID>
```

Prefix and substring matches are forbidden. The prompt contains the marker
inside double quotes and inside an instruction sentence, never as a standalone
line. Therefore the terminal echo of the submitted prompt does not satisfy the
exact-line predicate. The LLM is explicitly told to omit the quotes when it
prints the final standalone marker.

Repeated full-snapshot `raw_output` events are harmless: once a slot leaves
`working`, duplicate marker observations for that pane are ignored.

On a valid marker:

1. Move the slot to `completing`.
2. transition the matching ticket from `in_progress` to `done`;
3. move the slot to `closing`;
4. close the matching logical pane through transport;
5. clear the slot only after close succeeds or runtime inspection proves that
   the expected pane is already absent;
6. refill the empty slot on the next poll tick.

The pane ID, slot ticket ID, exact marker ticket ID, task ID, and physical
session must agree before completion changes persistent state.

## Failure Handling and Retry Rules

The store gains a manager-only requeue operation that atomically changes one
specific `in_progress` ticket back to `ready`, clears `started_at`, and updates
`updated_at`. It is not exposed as a new user-facing queue command.

- Render or pane-create failure requeues the claimed ticket immediately because
  no worker pane remains active.
- Readiness or input failure first closes the created pane. Only confirmed close
  or confirmed runtime absence permits requeue and slot release.
- If worker cleanup cannot be confirmed, the slot remains occupied and the
  ticket remains `in_progress`; polling retries cleanup without exceeding
  capacity.
- A database failure while marking `done` leaves the pane open and retries the
  transition. It never closes a worker whose completion was not persisted.
- A close failure after a successful `done` transition retains the slot and
  retries close. Confirmed runtime absence releases the slot without parsing
  transport error strings.
- Event-stream loss pauses new claims. The manager reconnects on later polling
  ticks. After reconnecting, it snapshots every active worker and applies the
  same exact-line marker check before resuming allocation.
- A store error other than an empty queue is logged and retried on a later tick;
  it does not terminate existing workers.

All retries are serialized through the manager event loop. Worker output
watching and transport calls may complete asynchronously, but a result is
accepted only when it still matches the slot's current ticket, pane, and state.

## Shutdown and Recovery Scope

On interrupt or SIGTERM, the manager stops claiming tickets. It attempts to
close each active coding pane. A not-yet-done ticket is requeued only after its
pane is confirmed closed or absent. Tickets already persisted as `done` remain
done. Shutdown reports cleanup failures and exits nonzero when it cannot safely
close and requeue all active work.

Unexpected process death cannot run this cleanup. Version one deliberately does
not persist manager ownership or adopt `in_progress` tickets and panes after a
restart. The operational contract is one manager per project, and a restarted
manager does not claim or alter pre-existing `in_progress` tickets. Persistent
ownership and crash adoption require a separate schema and reconciliation
design.

## Tests

`internal/ticketworker` tests use fake stores, clocks, ticks, event streams, and
runtime clients to cover:

- no claim before matching anchor and stream readiness;
- rejection of wrong task/session anchor panes;
- immediate FIFO fill up to, but never beyond, `max_workers`;
- exact prompt rendering and ticket-specific completion instruction;
- prompt echo containing a quoted marker not completing the ticket;
- exact unquoted marker completing only the matching ticket;
- `done` persistence before close and next-tick refill;
- render/create/readiness/input failure requeue rules;
- cleanup failure retaining capacity and retrying;
- database completion failure retaining the pane and retrying;
- close failure with present versus absent runtime pane;
- event-stream loss pausing allocation, reconnecting, and recovering a marker
  from snapshots;
- graceful shutdown close/requeue behavior;
- stale asynchronous results being ignored.

Role package tests use fake executables or injected dependencies and do not
start interactive Codex. Catalog and dispatcher tests verify metadata, argument
requirements, dispatch, and child exit-code preservation.

Documentation adds the role to
`/Users/in05908_mac/.config/pi/docs/agent-roles.md` as required by the repository
role workflow, creating that file if it does not exist. It records exact usage,
options, purpose, config/database requirements, agentd and Zellij requirements,
and the completion-marker contract.

Final verification runs focused package tests, `go test ./...`, builds
`bin/agent-role`, confirms `./bin/agent-role roles` lists `ticket-manager`, then
builds `bin/zellij-agent` and atomically registers it at
`~/.config/custom-cli/zellij-agent` according to the repository instructions.
