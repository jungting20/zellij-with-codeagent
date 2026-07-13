# Work Feedback Loop Design

**Date:** 2026-07-13

## Goal

Extend `zellij-agent work "<goal>"` from a goal-prefilled workspace launcher into a project-aware, bounded validation workflow. The runtime must select and display an explicit test configuration, wait for the user to submit the initial coder prompt, run validation only after the coder reports readiness, forward concrete failure evidence to the coder, and stop on a deterministic terminal condition.

The first implementation must support this complete Go-project flow:

```text
detect project
-> display preflight configuration
-> user submits the coder goal
-> coder reports ready
-> run configured test
-> forward failure evidence
-> coder reports ready again
-> rerun test
-> stop on pass or a configured bound
```

## Design Decisions

- The runtime owns feedback-loop configuration and state transitions. An LLM does not choose commands, retry limits, or stop conditions.
- No additional test-planner agent pane is created. The coder owns repository changes, including test source changes required by the goal.
- The test pane only executes the command selected in the preflight configuration.
- The existing notes pane becomes a user-visible preflight/status pane. It is not an agent and does not edit the repository.
- The initial coder goal remains prefilled without Enter. The user starts coding by reviewing the goal and pressing Enter.
- After the user starts the task, failure evidence may be submitted automatically to the coder as part of the approved loop.
- A successful validation does not automatically clean up panes. The user retains the workspace for inspection and performs cleanup explicitly.
- Version one runs one configured test command per attempt. Multi-stage test/build pipelines are deferred.

## Non-Goals

- A general LLM planner that invents runtime operations.
- A separate agent that writes tests concurrently with the coder.
- Inferring coder readiness from arbitrary screen text or inactivity.
- Unlimited retries or automatic retry after a test timeout.
- Automatically committing, merging, or cleaning up a successful task.
- Durable loop recovery after daemon restart.
- Multiple ordered validation stages in one attempt.

## Components

### Project Detector

The work planner inspects only known project marker files in the requested working directory. It does not recursively scan the repository or execute project code during detection.

Initial profiles are:

| Profile | Marker | Default test command | Detected build command |
| --- | --- | --- | --- |
| Go | `go.work` or `go.mod` | `go test ./...` | `go build ./...` |
| Node/npm | `package.json`, no alternate lockfile | `npm test` when a `test` script exists | `npm run build` when a `build` script exists |
| Node/pnpm | `package.json` and `pnpm-lock.yaml` | `pnpm test` when a `test` script exists | `pnpm build` when a `build` script exists |
| Node/Yarn | `package.json` and `yarn.lock` | `yarn test` when a `test` script exists | `yarn build` when a `build` script exists |
| Rust | `Cargo.toml` | `cargo test` | `cargo check` |

If markers for more than one project family exist at the root, automatic feedback is disabled until the user supplies `--profile` or `--test-command`. If multiple Node lockfiles exist, automatic feedback is also disabled instead of guessing a package manager.

Detection has no side effects. In particular, it does not install dependencies, download modules, run package scripts, or create files.

### Configuration Resolution

Configuration precedence is:

```text
explicit CLI override
-> detected project profile
-> feedback disabled with an actionable reason
```

The supported user-facing settings are:

- `--profile PROFILE`: override project detection.
- `--test-command COMMAND`: replace the profile test command.
- `--max-attempts N`: maximum number of validation runs; default `3`.
- `--test-timeout DURATION`: timeout for one validation run; default `5m`.
- `--overall-timeout DURATION`: bound the complete loop; default `30m`.
- `--no-review`: omit the read-only review pane.
- `--no-lazygit`: omit the lazygit pane.

`max_attempts` counts actual test executions. A duplicate readiness signal received while a test is already running does not consume an attempt.

When no test command can be resolved, `work` still creates a goal-oriented workspace but marks feedback as disabled. It prints the reason and the exact `--test-command` override needed to enable it.

### Feedback Manifest

The resolved configuration becomes an immutable manifest associated with the work task:

```yaml
task_id: work-login-validation
cwd: /workspace
goal: add email validation to login requests
profile: go
test:
  argv: [go, test, ./...]
  timeout: 5m
loop:
  max_attempts: 3
  overall_timeout: 30m
  evidence_limit_bytes: 32768
  auto_forward_failure: true
optional_panes:
  review: true
  lazygit: true
```

Profile-derived commands are stored as argument vectors. An explicit `--test-command` is an intentionally user-supplied shell command and is displayed verbatim before it can run.

The build command is detected and shown as useful project information, but it is not part of the version-one pass/fail loop.

### Preflight Pane

The current notes pane is expanded into a `preflight` role. It prints:

- goal and working directory;
- detected profile and marker files;
- exact test and build commands;
- test timeout, overall timeout, and maximum attempts;
- whether review and lazygit panes are enabled;
- feedback enabled/disabled state and any warning;
- current loop state, attempt count, and last result;
- commands for dashboard, status, snapshot, cancellation, and cleanup.

The pane runs a transport-backed read-only watcher so state, attempt count, and last result refresh without rewriting files. It never invokes an LLM and never edits the repository. The dashboard shows the same manifest and state through the transport boundary.

### Coder Pane and Readiness Protocol

The coder pane starts in the project directory and receives the exact trimmed user goal as its unsubmitted initial input. Feedback protocol instructions are supplied through coder-role task metadata rather than appended to or altering the visible goal. The execution plan supplies `AGENTD_SOCKET`, `AGENTD_TASK_ID`, and `AGENTD_PANE_ID` to the coder role so a readiness request identifies its source without asking the model to reconstruct identifiers.

The coder owns implementation files and any test source changes. When it considers the repository ready for validation, it reports a structured readiness signal:

```bash
zellij-agent ctl ready
```

The command reads the task, pane, and socket defaults from those environment variables and sends a typed transport request. Explicit CLI flags remain available for manual intervention. The runtime verifies that:

- the task exists and has feedback enabled;
- the caller identifies the task's coder pane;
- the task is in `waiting_for_coder`;
- the loop has not been canceled, timed out, passed, or exhausted.

Readiness is not inferred from arbitrary terminal output. A signal received during `testing` is acknowledged as already in progress and does not start another test.

### Test Runner

The test pane is a runtime-controlled shell in the project directory. For each attempt, the runtime assigns a unique run ID and invokes a unified-binary `test-runner` helper. The helper starts the configured command as a child process, enforces the per-test timeout with a context, terminates the child process group on timeout, and emits structured start and result markers before returning to the shell.

Conceptually, a Go attempt is:

```sh
zellij-agent role test-runner execute \
  --run-id "$run_id" \
  --timeout 5m \
  -- go test ./...
```

Profile argument vectors remain separate arguments after `--`. An explicit shell command runs as the helper child `sh -lc <command>`. The helper prints `agentd_test_started`, then one terminal marker containing the run ID and either `passed`, `failed` with an exit status, or `timed_out`. The marker, run ID, process exit status, and helper timeout determine the outcome; an LLM does not interpret whether a test passed.

The runtime permits only one active test run per task.

### Evidence Collection and Forwarding

On a non-zero exit status, the runtime:

1. records the attempt number, command, exit status, and duration;
2. captures the test pane snapshot;
3. extracts output for the current run when its markers are available;
4. retains at most the last 32 KiB of evidence;
5. sends a structured failure message to the coder;
6. returns the task to `waiting_for_coder`.

The submitted message is formatted as:

```text
[agentd validation failure]

Attempt: 1/3
Command: go test ./...
Exit code: 1

Output:
<captured output>

Fix the failure and report readiness again when validation should rerun.
```

Only evidence generated by the configured test run is forwarded. The runtime does not invent a diagnosis or suggest arbitrary code changes.

### Feedback Controller

Each task has one in-memory controller. Its states are:

```text
prepared
waiting_for_coder
testing
paused
passed
exhausted
timed_out
canceled
configuration_error
runtime_error
```

The transition table is:

| Current state | Input | Next state | Action |
| --- | --- | --- | --- |
| `prepared` | workspace created | `waiting_for_coder` | Display manifest; run nothing |
| `waiting_for_coder` | valid readiness | `testing` | Increment attempt and start test |
| `testing` | exit `0` | `passed` | Record success and stop |
| `testing` | non-zero exit, attempts remain | `waiting_for_coder` | Capture and forward evidence |
| `testing` | non-zero exit, limit reached | `exhausted` | Record final failure and stop |
| `testing` | per-test timeout | `timed_out` | Stop the test and do not retry |
| active state | overall timeout | `timed_out` | Cancel active work and stop |
| `waiting_for_coder` | user pause | `paused` | Prevent new test starts and evidence delivery |
| `paused` | user resume | `waiting_for_coder` | Resume without consuming an attempt |
| non-terminal state | user cancel | `canceled` | Stop controller; cleanup remains explicit |
| any active state | runtime/transport failure | `runtime_error` | Record the error and require intervention |

Pause is rejected while `testing`; the user may let the bounded run finish or cancel it. Canceling during `testing` cancels the helper context and terminates its child process group. Terminal states never initiate another test automatically.

The overall timeout starts when workspace creation succeeds and the controller enters `waiting_for_coder`. It therefore includes time spent reviewing the preflight display, the initial coder work, subsequent coder waits, and all test executions. The preflight and dashboard show the absolute deadline so the user can choose a larger override before starting a long task.

## End-to-End User Flow

1. The user runs `zellij-agent work "<goal>"` in a project directory.
2. The CLI resolves the directory, detects a profile, checks optional tool availability, and builds the feedback manifest.
3. The daemon creates the coder pane first, waits for the Codex prompt marker, and prefills the exact goal without Enter.
4. The daemon creates the test, optional review, optional lazygit, and preflight panes.
5. The preflight pane and dashboard display the exact validation configuration. No test runs yet.
6. The user reviews the goal and configuration, then presses Enter in the coder pane.
7. The coder edits implementation and test files.
8. The coder sends the typed readiness signal.
9. The controller runs attempt 1 in the test pane.
10. On failure, the controller forwards bounded evidence and waits for another readiness signal.
11. The coder fixes the failure and reports readiness again.
12. The controller runs attempt 2.
13. On success, the controller enters `passed` and leaves every pane open for inspection.
14. The user reviews the result and explicitly cleans up the task from the dashboard or CLI.

## Dashboard Behavior

The dashboard task detail includes:

- feedback state;
- profile and exact test command;
- current/maximum attempt count;
- per-test and overall timeouts;
- current run ID and elapsed time while testing;
- last exit status and failure summary;
- feedback enabled/disabled reason.

Dashboard actions are:

- `t`: request a test immediately when the task is waiting;
- `p`: pause or resume feedback processing;
- `s`: inspect coder or test snapshot;
- `i`: send manual pane input through the existing transport;
- `x`: cancel the controller, then request confirmed task cleanup.

Dashboard actions never call Zellij directly.

## Tool Availability

Before submitting the plan, the CLI checks tools needed by enabled panes:

- missing `codex` is a fatal configuration error because the coder cannot start;
- missing `lazygit` disables that optional pane with a visible warning unless explicitly required later;
- missing project test executables disable feedback with an actionable warning;
- missing optional review capability disables review without blocking the coder workspace.

Checks use executable lookup only. They do not run project scripts.

## Error Handling and Bounds

- A test timeout stops the loop and requires user intervention; it is not counted as an ordinary assertion failure and is not retried automatically.
- The overall timeout begins at successful workspace creation and includes preflight review, coder wait, and test execution time.
- Duplicate readiness requests are idempotent while a run is active.
- Failure snapshots are bounded to 32 KiB and labeled when truncated.
- A failed evidence delivery enters `runtime_error`; it does not silently consume another attempt.
- Canceling a loop stops future actions but does not close unmanaged or managed panes implicitly.
- Daemon restart loses version-one controller state. Durable task and request recovery remains a later reliability project.

## Registry Lifecycle Prerequisite

The feedback loop must not ship before the current cleanup/restart collision is fixed. Today, successful dashboard cleanup can leave terminal logical records such as `coder` in the in-memory registry, while a subsequent `work` run reuses the same logical IDs and receives `registry record already exists`.

The prerequisite fix must make successful task cleanup release logical pane IDs consistently and must handle the race between runtime cleanup and subscription-driven pane-close removal. Its regression flow is:

```text
submit work
-> clean the task through the dashboard/runtime
-> submit work again through the same daemon
-> second submission succeeds
```

This is a lifecycle prerequisite, not part of project detection or feedback decision logic.

## Testing Strategy

### Project Detection

- Go, npm, pnpm, Yarn, and Rust marker fixtures select the documented profile.
- Explicit profile and command overrides win over detection.
- Polyglot roots and multiple Node lockfiles disable automatic feedback with a specific reason.
- Detection performs no process execution or writes.

### Manifest and Work Planning

- The manifest contains the exact goal, canonical working directory, commands, limits, and optional-pane choices.
- The coder initial input remains exact and unsubmitted.
- Disabled optional panes are absent from the execution plan.
- Missing required and optional tools produce the documented outcomes.

### Controller State Machine

- `ready -> failed -> evidence delivered -> ready -> passed` completes in two attempts.
- Duplicate readiness during `testing` creates no second run.
- A final failed attempt enters `exhausted` without forwarding another automatic retry request.
- Per-test timeout and overall timeout enter `timed_out` and run no further command.
- Pause, resume, cancel, and runtime-error transitions preserve attempt accounting.
- Evidence is run-scoped, bounded, and labeled when truncated.

### Transport and Dashboard

- Typed readiness and loop-control requests validate task and pane identity.
- Dashboard renders configuration, state, attempts, timing, and disabled reasons.
- Dashboard controls call only transport client methods.
- Cleanup cancellation and partial failures remain visible.

### Real-Zellij Smoke Flow

Use a temporary Go project whose first validation fails. Start `work`, verify the prefilled goal and preflight manifest, submit the coder goal, signal readiness, observe failure evidence in coder, make the deterministic fix, signal readiness again, and confirm the second run passes. Clean up from the dashboard and submit the same work again through the same daemon to cover the registry prerequisite.

## Rollout Order

1. Fix registry cleanup and same-daemon `work` rerun.
2. Add project detection, manifest resolution, and preflight rendering.
3. Add typed coder readiness and the bounded controller state machine.
4. Add test execution markers, outcome parsing, and evidence forwarding.
5. Add dashboard feedback state and controls.
6. Complete unit, race, and real-Zellij smoke verification.
