# Debate Coordinator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `zellij-agent ctl debate` MVP that starts multiple coding-agent panes, sends a topic, waits for per-agent completion markers from the event stream, collects snapshots, and prints a round summary payload.

**Architecture:** Implement the first version as a client-side coordinator in `internal/cli/ctl`, using existing transport APIs: `SubmitExecutionPlan`, `SendMessage`, `StreamEvents`, and `SnapshotOutput`. The coordinator does not call Zellij directly and does not add new runtime primitives in this pass.

**Tech Stack:** Go standard library, existing `internal/transport` client interfaces, existing runtime event stream.

---

## File Structure

- Modify `internal/cli/ctl/ctl.go`: add `debate` command parsing and a small coordinator implementation.
- Modify `cmd/agentctl/main_test.go`: extend the fake transport client and add focused CLI tests.
- Optional modify `README.md`: add a short usage example after behavior is verified.

## Task 1: Add Debate CLI Parsing

**Files:**
- Modify: `internal/cli/ctl/ctl.go`
- Test: `cmd/agentctl/main_test.go`

- [ ] **Step 1: Write the failing test**

Add a test that calls:

```go
code := run([]string{
	"debate",
	"--topic", "Should we use markers?",
	"--agents", "a,b,c",
	"--cwd", "/repo",
	"--agent-role-bin", "/bin/zellij-agent",
	"--timeout", "5s",
}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))
```

Assert that the fake client receives one execution plan with panes `debate-coordinator`, `debate-a`, `debate-b`, and `debate-c`.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateSubmitsPlan -count=1
```

Expected: FAIL because `debate` is an unknown command.

- [ ] **Step 3: Implement minimal parsing**

Add `case "debate": return runDebate(...)` in `Run`, add usage text, and implement flags:

```text
--topic <text> required
--agents <csv> default a,b,c
--cwd <path> default .
--agent-role-bin <path> default current executable role command
--timeout <duration> default 10m
```

For this task, only submit the execution plan and print pane ids.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateSubmitsPlan -count=1
```

Expected: PASS.

## Task 2: Send Round Prompt With Unique Markers

**Files:**
- Modify: `internal/cli/ctl/ctl.go`
- Test: `cmd/agentctl/main_test.go`

- [ ] **Step 1: Write the failing test**

Add a test that runs `debate` with agents `a,b` and verifies `SendMessage` is called once per agent from `debate-coordinator` to `debate-a` and `debate-b`.

Each message body must include:

```text
Round: 1
Topic: marker test
Completion marker:
<<<AGENT_DEBATE_DONE
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateSendsRoundPromptWithMarkers -count=1
```

Expected: FAIL because no debate prompt messages are sent.

- [ ] **Step 3: Implement prompt dispatch**

After plan submission, generate one marker per agent:

```text
<<<AGENT_DEBATE_DONE debate=<request-id> round=1 agent=<agent> token=<token>>>
```

Send a structured prompt to each agent through `SendMessage`.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateSendsRoundPromptWithMarkers -count=1
```

Expected: PASS.

## Task 3: Wait For Marker Events And Snapshot Results

**Files:**
- Modify: `internal/cli/ctl/ctl.go`
- Test: `cmd/agentctl/main_test.go`

- [ ] **Step 1: Write the failing test**

Add a test with a fake event stream that emits raw output events:

```go
transport.Event{Type: "raw_output", PaneID: "debate-a", Message: markerA}
transport.Event{Type: "raw_output", PaneID: "debate-b", Message: markerB}
```

Assert that `SnapshotOutput` is called for both panes after markers are observed and stdout contains both snapshot outputs.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateWaitsForMarkersAndSnapshots -count=1
```

Expected: FAIL because marker waiting and snapshots are not implemented.

- [ ] **Step 3: Implement marker waiting**

Open `StreamEvents`, track completion by pane id, and stop when all expected markers are seen or the debate timeout expires.

On success, snapshot all agent panes with `Full: true`.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateWaitsForMarkersAndSnapshots -count=1
```

Expected: PASS.

## Task 4: Timeout And Error Handling

**Files:**
- Modify: `internal/cli/ctl/ctl.go`
- Test: `cmd/agentctl/main_test.go`

- [ ] **Step 1: Write the failing test**

Add a test where only one of two agents emits its marker and the command timeout is short. Assert exit code `1` and stderr lists the missing agent pane.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateTimesOutWhenMarkerMissing -count=1
```

Expected: FAIL because timeout behavior is missing.

- [ ] **Step 3: Implement timeout reporting**

Return non-zero when not all markers are observed before timeout. Include seen and missing panes in stderr.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/agentctl -run TestRunDebateTimesOutWhenMarkerMissing -count=1
```

Expected: PASS.

## Task 5: Verification And Registration

**Files:**
- Modify: `README.md` if usage docs are added.

- [ ] **Step 1: Run focused tests**

```bash
go test ./cmd/agentctl ./internal/cli/ctl -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build and install updated unified binary**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Expected: both commands exit 0.

## Self-Review

- Spec coverage: covers pane creation, prompt broadcast, completion-marker detection, snapshots, and timeout handling.
- Placeholder scan: no TBD/TODO entries remain.
- Type consistency: all work stays on existing `transport.AgentClient` methods exposed through `internal/cli/ctl`.
