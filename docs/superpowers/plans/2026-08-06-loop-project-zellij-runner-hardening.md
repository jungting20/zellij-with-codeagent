# Loop Project Zellij Runner 보안·복구 강화 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this final hardening task. Use superpowers:test-driven-development for Go changes and superpowers:writing-skills for global skill changes. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** read-only verifier가 daemon socket에 접근하지 않고도 검증 결과를 전달하게 만들고, hostile read-only 인자 주입·stale daemon 호환·logical pane ID 재사용 cleanup 위험·`VERIFICATION_PENDING` 재진입 결함을 모두 닫는다.

**Architecture:** read-only start는 arbitrary args 대신 typed positional prompt 하나만 받으며, CLI는 daemon health capability를 확인한 뒤 요청한다. Verifier는 구조화된 stdout block을 출력하고 host orchestrator가 bounded snapshot polling으로 검증·기록·worker relay를 수행한다. 모든 loop-owned pane은 daemon 발급 random ownership token으로 compare-and-close한다.

**Tech Stack:** Go 1.x 표준 라이브러리와 `testing`, Unix-socket JSON transport, Zellij, Codex CLI, Python 3 `unittest`, Markdown Agent Skill.

## Global Constraints

- 프로젝트 작업 경로는 `/Users/in05908_mac/zellij-with-codeagent/.worktrees/loop-project-zellij-runner`이고 시작 commit은 `8d701a8`이다.
- 전역 스킬 작업 경로는 `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner`이고 시작 commit은 `e153b7b`이다.
- 두 `.worktrees/loop-project-zellij-runner` 경로를 삭제하지 않는다.
- 기본 skills checkout의 기존 dirty 파일을 수정·stash·reset하지 않는다. 전역 스킬 commit 완료 뒤 active checkout은 clean-path fast-forward만 수행하고 기존 dirty 상태가 byte-identical하게 보존됐는지 확인한다.
- read-only verifier의 repository와 daemon socket write 차단을 완화하지 않는다.
- full access의 기존 arbitrary `Args` 동작은 유지하되 full request JSON에는 `access`를 보내지 않는다.
- 모든 Go 변경은 RED 확인 후 최소 GREEN 구현, `gofmt`, focused test, `go test ./...` 순으로 검증한다.
- 모든 skill 변경은 먼저 계약 테스트를 실패시키고, 문서·eval을 최소 수정한 뒤 전체 test를 실행한다.
- 커밋 메시지는 반드시 한글로 작성한다.
- 최종 live smoke cleanup은 이번 run이 발급받은 `(logical_id, ownership_token)`만 사용하고 session/tab 또는 broad task/role cleanup을 하지 않는다.

---

### Task 1: 최종 보안·복구 강화 수정

**Project files:**
- Modify: `internal/codingagent/profile.go`
- Modify: `internal/codingagent/profile_test.go`
- Modify: `internal/codingagent/service.go`
- Modify: `internal/codingagent/service_test.go`
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/types_test.go`
- Modify: `internal/transport/server.go`
- Modify: `internal/transport/server_test.go`
- Modify: `internal/transport/handlers_agents.go`
- Modify: `internal/transport/client_test.go`
- Modify: `internal/cli/agent/agent.go`
- Modify: `internal/cli/agent/agent_test.go`
- Modify: `internal/registry/types.go`
- Modify: `internal/registry/registry.go`
- Modify: `internal/registry/registry_test.go`
- Modify: `internal/runtime/types.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/runtime/cleanup.go`
- Modify: `internal/runtime/cleanup_test.go`
- Modify: `internal/transport/handlers_panes.go`
- Modify: `internal/cli/ctl/ctl.go`
- Modify: `internal/cli/ctl/ctl_test.go`
- Modify: `cmd/agent-role/loopprojectagent/loopprojectagent.go`
- Modify: `cmd/agent-role/loopprojectagent/loopprojectagent_test.go`
- Modify: `docs/zellij-agent-quickstart.md`

**Global skill files:**
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/SKILL.md`
- Create: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/references/agent-dispatch.md`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/references/pane-protocol.md`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/references/milestone-dispatch.md`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/references/verifier-dispatch.md`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/references/recovery.md`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/tests/test_contract_parity.py`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/tests/test_context_efficiency_contract.py`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/tests/test_pane_protocol.py`
- Modify: `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner/loop-project-zellij-runner/evals/evals.json`

**Interfaces:**

```go
const CapabilityAgentAccessReadOnlyV1 = "agent_access_read_only_v1"

type StartAgentRequest struct {
	Kind       Kind
	AccessMode AccessMode
	CWD        string
	Prompt     string
	ExtraArgs  []string // full access only
	// existing fields unchanged
}

func (p Profile) BuildManagedCommand(access AccessMode, prompt string, extra []string) ([]string, error)

type OwnershipToken string

type CleanupTarget struct {
	PaneID         PaneID
	OwnershipToken OwnershipToken
}
```

Transport mirrors `prompt`, `capabilities`, `ownership_token`, and token-qualified cleanup targets with `omitempty` where legacy compatibility requires it. `Pane` and registry records preserve the token across create/claim/list/snapshot/cleanup responses.

- [ ] **Step 1: typed read-only prompt RED tests**

Update `internal/codingagent/profile_test.go` and `service_test.go` so safe zero/one prompt produces exactly:

```go
[]string{"codex", "--sandbox", "read-only", "--ask-for-approval", "never", "Verify M1"}
```

Add table cases that reject read-only `ExtraArgs`, prompt values beginning with `-`, and explicit bypass/config/add-dir payloads before runtime registration, pane creation, store mutation, or monitor start. Keep a full-access case proving arbitrary args and bypass defaults still work.

Run: `go test ./internal/codingagent -run 'Test(ProfileBuildManagedCommand|ServiceStartAgent.*ReadOnly|ServiceStartAgent.*SideEffects)' -v`

Expected: FAIL because the current API appends read-only `ExtraArgs`.

- [ ] **Step 2: typed read-only prompt GREEN implementation**

Add `Prompt string` to the domain request. Change `BuildManagedCommand` so:

```go
switch access {
case AccessFull:
	if prompt != "" { return nil, ErrInvalidAccessMode }
	return p.BuildCommand(true, extra), nil
case AccessReadOnly:
	if p.Kind != KindCodex || len(extra) != 0 || strings.HasPrefix(prompt, "-") { /* reject */ }
	command := []string{p.Executable, "--sandbox", "read-only", "--ask-for-approval", "never"}
	if prompt != "" { command = append(command, prompt) }
	return command, nil
}
```

Validation must run before `registerStart`. Do not attempt a denylist-only solution; arbitrary read-only option passthrough must be structurally absent.

Run: `gofmt -w internal/codingagent && go test ./internal/codingagent -run 'Test(ProfileBuildManagedCommand|ServiceStartAgent)' -v`

Expected: PASS.

- [ ] **Step 3: transport compatibility and capability RED tests**

In `internal/transport/types_test.go`, assert JSON rules:

- full/default request omits `access` and `prompt`;
- read-only request contains `"access":"read-only"` and one `prompt`, never an `args` entry;
- `HealthResponse` round-trips `capabilities: ["agent_access_read_only_v1"]`.

In server/client tests, assert `/v1/health` advertises the constant. Add a legacy fixture that rejects unknown request fields and prove a full CLI start succeeds because no access field is sent.

Run: `go test ./internal/transport -run 'Test.*(Health|StartAgent|Legacy|JSON)' -v`

Expected: FAIL because capabilities/prompt are not modeled and full sends `access`.

- [ ] **Step 4: transport compatibility and capability GREEN implementation**

Add `Capabilities []string` to `HealthResponse`, advertise `CapabilityAgentAccessReadOnlyV1` in `Server.ServeHTTP`, and add typed `Prompt` conversion in agent handlers. Preserve empty access as domain `AccessFull`. Ensure full request creation uses empty transport access even when the CLI option canonicalizes to `full`.

Run: `gofmt -w internal/transport && go test ./internal/transport -v`

Expected: PASS.

- [ ] **Step 5: CLI capability gate and prompt parsing RED tests**

Extend the CLI fake client to record `Health` and `StartAgent` calls. Add cases:

- read-only with zero prompt calls health, requires capability, then starts;
- read-only with one positional prompt sends `Prompt` and empty `Args`;
- two values or an option-like value after `--` returns exit 2 before health/start;
- missing capability returns exit 1 with a drain/restart diagnostic and never calls `StartAgent`;
- default/explicit full skips health and sends empty `Access` plus unchanged `Args`;
- help says bypass defaults apply to full access only.

Run: `go test ./internal/cli/agent -run 'TestRunStart.*(Access|Prompt|Capability|Legacy|Help)' -v`

Expected: FAIL.

- [ ] **Step 6: CLI capability gate and prompt parsing GREEN implementation**

Add `Health(context.Context)` to the local agent client interface. Split payload according to access after option parsing. Read-only accepts at most one non-option positional prompt and performs bounded health lookup before `StartAgent`; full keeps passthrough args, omits `access`, and never depends on new health fields. The mismatch error must not auto-restart the daemon.

Run: `gofmt -w internal/cli/agent && go test ./internal/cli/agent -v`

Expected: PASS.

- [ ] **Step 7: ownership token registry/runtime RED tests**

Add registry/runtime tests proving:

- every new create/claim receives a non-empty opaque token and it survives `Pane` conversion and transport JSON;
- two separately created panes get different tokens;
- cleanup target with matching `(pane ID, token)` closes the physical pane;
- token mismatch returns the current pane in `Skipped`, makes no backend close call, and does not remove/update the record;
- legacy `PaneIDs` cleanup retains existing behavior for non-loop callers;
- a target absent by logical ID remains a normal failure, but widening to task/role never occurs.

Inject a token generator into `runtime.ServiceOptions` for deterministic tests. Production default must use `crypto/rand` and return an error rather than a predictable token when entropy fails.

Run: `go test ./internal/registry ./internal/runtime ./internal/transport -run 'Test.*(Ownership|Token|Cleanup)' -v`

Expected: FAIL because tokens and qualified targets do not exist.

- [ ] **Step 8: ownership token registry/runtime GREEN implementation**

Add token fields to registry registration/record and runtime/transport pane representations. Generate the token before backend mutation so generation failure has no side effects. Add `CleanupTargets []CleanupTarget` while preserving legacy `PaneIDs`. When qualified targets are present, match both fields; mismatches are safety skips, not partial failures. Expose repeatable paired flags in `ctl cleanup`, for example:

```text
zellij-agent ctl cleanup --pane <logical-id> --ownership-token <opaque-token>
```

Require equal non-zero counts and pair by position. Keep tokenless `--pane` as explicitly documented legacy behavior.

Run: `gofmt -w internal/registry internal/runtime internal/transport internal/cli/ctl && go test ./internal/registry ./internal/runtime ./internal/transport ./internal/cli/ctl -v`

Expected: PASS.

- [ ] **Step 9: role bootstrap RED then GREEN**

Change `loopprojectagent` tests to require the verifier bootstrap to forbid daemon socket access and outbound `ctl message`, and to require exactly one stdout result block:

```text
LOOP_VERIFY_RESULT_BEGIN
protocol_version: 1
project_id: <assigned project id>
milestone_id: <assigned milestone id>
run_id: <assigned run id>
verdict: APPROVE | REJECT | UNCERTAIN
next_action: <one bounded action>
LOOP_VERIFY_RESULT_END
```

The role command must pass the full bootstrap as the single typed read-only prompt. Worker bootstrap may still use host messaging but must use resolved logical-ID placeholders.

Run before implementation: `go test ./cmd/agent-role/loopprojectagent -run 'Test.*Verifier' -v`

Expected: FAIL due to current `ctl message` result instruction.

Implement the minimal bootstrap update and run:

`gofmt -w cmd/agent-role/loopprojectagent && go test ./cmd/agent-role/loopprojectagent -v`

Expected: PASS.

- [ ] **Step 10: skill protocol RED tests**

Read `superpowers:writing-skills` before editing the global skill. Extend the Python tests to require:

- no verifier outbound `ctl message` result command;
- exact begin/end block, required keys, exact-one-block validation, assignment identity/run ID/verdict validation;
- bounded host snapshot polling and evidence order `FINISHED → raw five-section output → VERIFIER_RAW_OUTPUT_END → APPEND_OK → RUNTIME_VALID → worker relay → token-qualified cleanup`;
- missing/duplicate/malformed marker records only `INTERRUPTED` or `TIMED_OUT`, never a manufactured verdict;
- executable `VERIFICATION_PENDING` entry without worker signal when durable inputs and pre-run `RUNTIME_VALID` exist, plus APPROVE/REJECT/UNCERTAIN transitions;
- ownership-token mismatch preserves an ambiguous orphan and never retries by logical ID alone;
- `references/agent-dispatch.md` exists and only routes to milestone/verifier dispatch;
- bash fenced worker commands reject verifier-role execution hidden by `$`, backticks, or `run` prefixes;
- command examples use resolved `<orchestrator-logical-id>`, `<worker-logical-id>`, `<verifier-logical-id>` and matching token placeholders.

Add a `VERIFICATION_PENDING` eval case to `evals/evals.json` and validate it parses.

Run: `python3 -m unittest discover -s loop-project-zellij-runner/tests -v`

Expected: FAIL on each new contract before documentation changes.

- [ ] **Step 11: skill protocol GREEN implementation**

Update `SKILL.md` and the four protocol references to implement the approved design. The orchestrator owns snapshot polling, structural validation, checkpoint append, runtime validation, worker relay, and exact cleanup. Verifier owns only read-only inspection and one stdout block. Add the compatibility shim without copying a second authoritative contract. Encode the `VERIFICATION_PENDING` preconditions and the token-mismatch orphan rule explicitly.

Run: `python3 -m unittest discover -s loop-project-zellij-runner/tests -v`

Run: `python3 -m json.tool loop-project-zellij-runner/evals/evals.json >/dev/null`

Expected: all skill tests PASS and eval JSON valid.

- [ ] **Step 12: project documentation and deployment contract**

Update `docs/zellij-agent-quickstart.md` with:

- read-only one-prompt syntax and hostile option rejection;
- health capability inspection and stale daemon drain/restart procedure;
- memory-backed registry consequence: logical IDs from before restart are invalid;
- token-qualified loop cleanup and tokenless legacy cleanup distinction;
- stdout verifier result relay.

Run: `rg -n 'agent_access_read_only_v1|ownership.token|drain|restart|LOOP_VERIFY_RESULT' docs/zellij-agent-quickstart.md`

Expected: every deployment/safety concept is present.

- [ ] **Step 13: project full verification and commit**

Run:

```bash
gofmt -w internal/codingagent internal/transport internal/cli/agent internal/registry internal/runtime internal/cli/ctl cmd/agent-role/loopprojectagent
go test ./internal/codingagent ./internal/transport ./internal/cli/agent ./internal/registry ./internal/runtime ./internal/cli/ctl ./cmd/agent-role/loopprojectagent -v
go test ./...
go build -o bin/zellij-agent ./cmd/zellij-agent
```

Expected: PASS.

Commit only project worktree changes:

```bash
git add internal/codingagent internal/transport internal/cli/agent internal/registry internal/runtime internal/cli/ctl cmd/agent-role/loopprojectagent docs/zellij-agent-quickstart.md
git commit -m "fix: 루프 프로젝트 실행 안전성 강화"
```

- [ ] **Step 14: global skill full verification, commit, and safe activation**

In `/Users/in05908_mac/.agents/skills/.worktrees/loop-project-zellij-runner` run the full skill tests and record the test count. Commit only `loop-project-zellij-runner/`:

```bash
git add loop-project-zellij-runner
git commit -m "fix: Zellij 루프 검증과 복구 계약 강화"
```

Capture default checkout `git status --porcelain=v1` and hashes of its dirty files before activation. Fast-forward its current branch to the canonical worktree commit only if Git can do so without touching those dirty paths. Recheck status and hashes byte-for-byte; abort activation on conflict instead of stashing/resetting user changes.

- [ ] **Step 15: atomic binary registration and daemon capability check**

Register the verified binary atomically:

```bash
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
```

Query current daemon health. If `agent_access_read_only_v1` is missing, inventory managed panes and report/drain them before an explicit daemon restart; never auto-restart or discard a live registry. Confirm the fresh daemon advertises the capability before continuing.

- [ ] **Step 16: bounded same-tab live smoke**

Run one fresh orchestrator/worker/verifier cycle using unique project/milestone/run IDs and record every returned logical ID, ownership token, session/tab, and physical pane ID.

Verify in order:

1. worker assignment reaches the same-tab pane and is visible in event/snapshot evidence;
2. verifier argv is exactly read-only fixed flags plus one prompt, with no bypass/config/add-dir option;
3. verifier repository write probe fails and leaves no file;
4. verifier cannot use the daemon socket;
5. verifier prints exactly one valid stdout result block;
6. host snapshot captures and validates it, appends matching FINISHED and full raw evidence, receives `APPEND_OK`, then `RUNTIME_VALID`;
7. host relays the bounded verdict to the worker;
8. token-qualified cleanup closes only the three task-owned panes;
9. all probes, temporary source artifacts, and task-owned pane records are absent while unrelated panes remain unchanged.

Do not treat a malformed/missing block as APPROVE. Do not broaden cleanup if a token mismatch occurs.

- [ ] **Step 17: report, ledger, and scoped re-review handoff**

Write `.superpowers/sdd/2026-08-06-loop-project-zellij-runner/final-fix-report.md` containing project/global base and head commits, RED/GREEN evidence, full-suite output summaries, activation preservation proof, daemon maintenance actions, live IDs/tokens, and cleanup proof. Append the completed fix wave to `progress.md`.

Request exactly one scoped re-review over project `8d701a8..HEAD`, global skill `e153b7b..HEAD`, the approved design, this plan, and the report. The reviewer must classify every prior Critical/Important/deferred-minor finding as `ADDRESSED` or still open and identify any new load-bearing regression. If a load-bearing finding remains, stop and report it; do not start a second unapproved fix wave.

---

## Completion Criteria

- hostile read-only payload cannot inject any option and is rejected before side effects;
- full start remains compatible with a daemon that does not know the `access` field;
- read-only start fails closed against a daemon lacking the capability;
- verifier result lifecycle completes without verifier socket access;
- `VERIFICATION_PENDING` has a tested fresh-verifier reentry path;
- cleanup token mismatch preserves the unrelated pane;
- project and global skill suites pass, binary/skill activation preserves unrelated user changes, bounded live smoke passes, and scoped re-review has no open Critical/Important finding.
