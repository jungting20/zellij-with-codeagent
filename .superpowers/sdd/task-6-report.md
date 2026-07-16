# Task 6 Report: User-Facing Plan Submission Commands

## Outcome

Implemented physical Zellij-session propagation for immediate work, Chrome, planner page/TUI, planner file submission, ctl plan, and ctl debate flows.

Generated-plan commands now resolve `--zellij-session` through `cli.ResolveZellijSession`, fall back to `ZELLIJ_SESSION_NAME`, and pass the resolved value into their builders. File-plan commands strictly decode first, select explicit flag over the file value, resolve with caller-environment fallback, semantically validate, and rebuild the envelope payload.

## RED Evidence

Command:

```text
go test ./internal/cli/work ./internal/cli/chrome ./internal/cli/planner ./internal/cli/ctl ./internal/debate -run 'Test.*ZellijSession' -count=1
```

Observed failures before implementation:

- work and Chrome generated envelopes failed semantic validation because `payload.zellij_session` was missing;
- missing-session tests reached semantic validation instead of returning the shared resolver error;
- debate tests did not compile because `executionPlan` had no Zellij-session parameter.

## GREEN Evidence

Focused command selector:

```text
go test ./internal/cli/work ./internal/cli/chrome ./internal/cli/planner ./internal/cli/ctl ./internal/debate -run 'Test.*ZellijSession' -count=1
```

Result: PASS for work, Chrome, and debate; planner/ctl implementation packages report no local test files because their established command harnesses live under `cmd/`.

Affected command and package suites:

```text
go test ./cmd/agent-planner ./cmd/agentctl -count=1
go test ./internal/cli/work ./internal/work ./internal/cli/chrome ./internal/chrome ./internal/cli/planner ./internal/planner ./internal/cli/ctl ./internal/debate -count=1
```

Result: PASS.

Build/registration:

```text
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Result: PASS.

## Full Suite

Ran exactly once:

```text
go test ./...
```

Result: FAIL only in three `internal/cli/ticketworker` tests because the deferred ticket-worker generated-plan path still omits `payload.zellij_session`:

- `TestRunStartDryRunPrintsValidatedPlanWithExplicitMaxWorkers`
- `TestRunStartPreservesConfiguredMaxWorkersWithoutOverride`
- `TestRunStartSubmitsPlan`

Ticket-worker/role propagation is explicitly assigned to Task 7, so this task did not modify that path.

## Files Changed

- `internal/cli/work/work.go`, `internal/cli/work/work_test.go`
- `internal/work/work.go`, `internal/work/work_test.go`
- `internal/cli/chrome/chrome.go`, `internal/cli/chrome/chrome_test.go`
- `internal/chrome/chrome.go`, `internal/chrome/chrome_test.go`
- `internal/cli/planner/planner.go`, `internal/planner/page.go`, `internal/planner/page_test.go`
- `internal/cli/ctl/ctl.go`
- `internal/debate/debate.go`, `internal/debate/debate_test.go`
- `cmd/agent-planner/main_test.go`, `cmd/agentctl/main_test.go` (existing planner/ctl CLI test harness locations)

## Self-Review

- Confirmed generated payloads receive the resolved physical session through builder request/options rather than post-build patching.
- Confirmed planner submit and ctl plan use strict decode -> flag/file/env resolution -> semantic validation -> envelope rebuild ordering.
- Confirmed `planner validate` still uses strict parse plus semantic validation and therefore rejects a missing `zellij_session`.
- Confirmed raw runtime HTTP validation remains unchanged.
- Confirmed Chrome changes stop at top-level builder/payload plumbing; delayed role-created plan plumbing remains for Task 7.
- Confirmed help coverage includes every Task 6 exposed command and `git diff --check` is clean.

## Concerns

The repository-wide suite cannot be green until Task 7 adds Zellij-session propagation to ticket-worker generated plans. No other concern found in the Task 6 scope.

## Review Fix Wave

Commit-wave changes after Task 6 review:

- Added shared `debate.ValidateOptions` validation and invoked it before resolving the physical session in ctl, preserving exit code `2` and established topic/round/agent-timeout errors outside Zellij.
- Moved planner TUI session resolution after goal, URL, and source validation and immediately before page-plan construction.
- Restored ctl plan compatibility with both canonical request envelopes and raw execution-plan payloads. Both shapes are strictly decoded, then use flag > file > environment session precedence before semantic validation.
- Added strict raw-payload decoding through `planner.DecodeExecutionPlanPayload`, including null-object and unknown-field rejection behavior inherited from the shared strict decoder.
- Added internal planner/ctl CLI regression harnesses for validation ordering, raw compatibility, unknown fields, file-over-environment precedence, and missing-session errors.
- Added an integration regression for `examples/plans/agent-role-demo.json`.
- Made work and Chrome success/error-path tests hermetic by explicitly setting their Zellij session rather than inheriting the developer shell.

Focused RED evidence before the review fixes:

```text
env -u ZELLIJ_SESSION_NAME go test ./internal/cli/planner ./internal/cli/ctl -run 'TestRun(TUIInvalid|DebateInvalid|PlanAcceptsStrictRaw|PlanRejectsUnknownRaw)' -count=1
```

Observed failures:

- planner TUI returned exit `1` with the missing-session resolver error instead of exit `2` with URL validation;
- ctl debate returned exit `1` with the missing-session resolver error instead of exit `2` with debate validation;
- ctl plan rejected a raw payload as an envelope with unknown field `session`.

Required hermetic verification command:

```text
env -u ZELLIJ_SESSION_NAME go test ./internal/cli/work ./internal/cli/chrome ./internal/cli/planner ./internal/cli/ctl ./internal/debate -count=1
```

Exact passing output:

```text
ok  	zellij-with-codeagent/internal/cli/work	0.582s
ok  	zellij-with-codeagent/internal/cli/chrome	0.732s
ok  	zellij-with-codeagent/internal/cli/planner	0.902s
ok  	zellij-with-codeagent/internal/cli/ctl	1.081s
ok  	zellij-with-codeagent/internal/debate	1.253s
```

Additional compatibility verification:

```text
env -u ZELLIJ_SESSION_NAME go test ./cmd/agent-planner ./cmd/agentctl ./internal/cli/work ./internal/cli/chrome ./internal/cli/planner ./internal/cli/ctl ./internal/debate ./internal/planner -count=1
```

Result: PASS for every listed package.
