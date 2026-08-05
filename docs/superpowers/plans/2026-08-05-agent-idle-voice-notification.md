# Agent Idle Voice Notification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add an opt-in agent start --notify-idle flag that makes the daemon announce each non-idle-to-idle coding-agent state transition through its existing serialized voice queue.

**Architecture:** Carry the opt-in flag from the CLI request into the daemon-owned coding-agent record. Keep state detection unchanged; a separate daemon event subscriber filters agent_state_changed events, resolves the record preference, and enqueues a direct speech message. Extend the existing voice notification model with a backwards-compatible direct-message path while retaining ticket formatting.

**Tech Stack:** Go, standard testing package, daemon event bus, Unix-socket transport, existing internal/voice native speech queue.

## Global Constraints

- --notify-idle is optional, defaults to false, and must appear before the -- passthrough separator.
- Announce only transitions whose previous state is not idle and current state is idle, including unknown, working, and blocked sources.
- Keep notification handling out of the state monitor and dashboard; use a separate daemon event subscriber.
- Voice failures are best effort and must not change agent state or terminate the daemon.
- Preserve the existing ticket-worker voice HTTP contract and ticket speech formatting.
- Do not persist notification preferences or queue contents across daemon restarts.
- This background feature does not create an agent role.
- Commit messages must be written in Korean.
- Repository instructions prohibit subagents for this work, so execute this plan inline.

---

### Task 1: Carry the opt-in flag into the coding-agent record

**Files:**
- Modify: internal/cli/agent/agent_test.go
- Modify: internal/cli/agent/agent.go
- Modify: internal/transport/types_test.go
- Modify: internal/transport/types.go
- Modify: internal/codingagent/service_test.go
- Modify: internal/codingagent/service.go
- Modify: internal/codingagent/types.go

**Interfaces:**
- Produces: transport.StartAgentRequest.NotifyOnIdle bool
- Produces: codingagent.StartAgentRequest.NotifyOnIdle bool
- Produces: codingagent.Record.NotifyOnIdle bool
- Consumes: the existing start CLI parsing and request conversion path.

- [ ] **Step 1: Write failing CLI, transport, and service tests**

Update TestRunStartSendsValidatedRequest so the command includes
--notify-idle before -- and the literal expected request contains
NotifyOnIdle: true. Add a successful start case without the flag that asserts
client.request.NotifyOnIdle is false.

Extend TestAgentStartRequestRoundTripPreservesSourceAndArguments with a JSON
payload containing notify_on_idle: true and assert both conversion directions
preserve it.

In TestServiceStartAgentClaimsCurrentPaneWithProfileCommand, set
NotifyOnIdle: true on the request and expected Record.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

~~~bash
go test ./internal/cli/agent ./internal/transport ./internal/codingagent -run 'TestRunStart|TestAgentStartRequestRoundTrip|TestServiceStartAgentClaims' -count=1
~~~

Expected: compilation fails because the three NotifyOnIdle fields do not exist.

- [ ] **Step 3: Implement minimal flag and data propagation**

Add a NotifyOnIdle bool with JSON name notify_on_idle and omitempty to the
transport request. Add NotifyOnIdle bool to codingagent.StartAgentRequest and
codingagent.Record. Copy it in both transport conversion directions and into
the Record created by codingagent.Service.StartAgent.

Extend startOptions and parseStartOptions:

~~~go
type startOptions struct {
    cwd          string
    socket       string
    timeout      time.Duration
    notifyOnIdle bool
}

case "--notify-idle":
    if hasValue {
        return startOptions{}, fmt.Errorf("--notify-idle does not accept a value")
    }
    opts.notifyOnIdle = true
~~~

Set NotifyOnIdle: opts.notifyOnIdle in the CLI request and document the flag
in printStartUsage.

- [ ] **Step 4: Run package tests and verify GREEN**

Run:

~~~bash
gofmt -w internal/cli/agent/agent.go internal/cli/agent/agent_test.go internal/transport/types.go internal/transport/types_test.go internal/codingagent/types.go internal/codingagent/service.go internal/codingagent/service_test.go
go test ./internal/cli/agent ./internal/transport ./internal/codingagent -count=1
~~~

Expected: all three packages pass.

- [ ] **Step 5: Commit the request/data-model slice**

~~~bash
git add internal/cli/agent internal/transport/types.go internal/transport/types_test.go internal/codingagent/types.go internal/codingagent/service.go internal/codingagent/service_test.go
git commit -m "feat: 에이전트 idle 알림 옵션 전달"
~~~

### Task 2: Add backwards-compatible direct speech messages

**Files:**
- Modify: internal/voice/service_test.go
- Modify: internal/voice/service.go

**Interfaces:**
- Produces: voice.Notification.Message string
- Produces: formatMessage behavior that prefers direct messages while preserving ticket formatting.
- Consumes: normalizeSummary sanitation and the 120-rune limit.

- [ ] **Step 1: Write failing direct-message formatting tests**

Add literal cases to TestServiceFormatsMessage:

~~~go
{
    name: "direct agent message",
    n: Notification{
        Message: "  Codex\tagent-3\x00 작업이\n완료되었습니다  ",
        Prefix: "ignored",
        TicketID: 99,
    },
    want: "Codex agent-3 작업이 완료되었습니다",
},
{
    name: "truncates direct message to 120 runes",
    n: Notification{Message: strings.Repeat("나", MaxSpokenSummaryRunes+1)},
    want: strings.Repeat("나", MaxSpokenSummaryRunes),
},
~~~

Keep existing ticket cases unchanged to protect compatibility.

- [ ] **Step 2: Run formatting test and verify RED**

~~~bash
go test ./internal/voice -run '^TestServiceFormatsMessage$' -count=1
~~~

Expected: compilation fails because Notification.Message does not exist.

- [ ] **Step 3: Implement direct-message formatting**

Add Message string to voice.Notification and make it the explicit branch:

~~~go
func formatMessage(notification Notification) string {
    if notification.Message != "" {
        return normalizeSummary(notification.Message)
    }
    base := fmt.Sprintf("%s %d 완료", strings.TrimSpace(notification.Prefix), notification.TicketID)
    summary := normalizeSummary(notification.Summary)
    if summary == "" {
        return base
    }
    return base + ". " + summary
}
~~~

- [ ] **Step 4: Run voice tests and verify GREEN**

~~~bash
gofmt -w internal/voice/service.go internal/voice/service_test.go
go test ./internal/voice -count=1
~~~

Expected: all voice tests pass, including existing FIFO and ticket formatting.

- [ ] **Step 5: Commit the voice model slice**

~~~bash
git add internal/voice/service.go internal/voice/service_test.go
git commit -m "feat: 음성 큐에 직접 메시지 지원"
~~~

### Task 3: Implement the daemon idle-state event subscriber

**Files:**
- Create: internal/cli/daemon/agent_idle_voice.go
- Create: internal/cli/daemon/agent_idle_voice_test.go

**Interfaces:**
- Consumes: eventbus.Event, codingagent Store.Get, codingagent.LookupProfile, and voice queue Enqueue.
- Produces: runAgentIdleVoiceLoop(context.Context, events channel, agentIdleVoiceStore, agentIdleVoiceQueue, io.Writer).
- Produces: handleAgentIdleVoiceEvent(eventbus.Event, agentIdleVoiceStore, agentIdleVoiceQueue, io.Writer).

- [ ] **Step 1: Write failing subscriber behavior tests**

Create focused store and queue fakes. Table-test enabled unknown, working, and
blocked to idle transitions; idle to idle; disabled records; raw_output; empty
agent ID; and codingagent.ErrNotFound.

The enabled success case must assert this literal:

~~~go
voice.Notification{
    RequestID: "agent-idle:agent-3:2000000123",
    Message:   "Codex agent-3 작업이 완료되었습니다",
}
~~~

Use StateChangedAt: time.Unix(2, 123) in the record. Add an enqueue-error test
that asserts a log line containing agent-3 while a following event is still
processed. Add loop tests for context cancellation and channel closure.

- [ ] **Step 2: Run subscriber tests and verify RED**

~~~bash
go test ./internal/cli/daemon -run '^TestAgentIdleVoice' -count=1
~~~

Expected: compilation fails because the subscriber functions do not exist.

- [ ] **Step 3: Implement filtering, lookup, message construction, and loop lifecycle**

Define narrow interfaces:

~~~go
type agentIdleVoiceStore interface {
    Get(codingagent.ID) (codingagent.Record, error)
}

type agentIdleVoiceQueue interface {
    Enqueue(voice.Notification) (voice.EnqueueStatus, error)
}
~~~

Filter non-agent events, empty agent IDs, idle-to-idle events, and destinations
other than idle. Resolve the record; ignore codingagent.ErrNotFound, log other
lookup errors, and return when NotifyOnIdle is false.

Resolve the profile display name and enqueue:

~~~go
voice.Notification{
    RequestID: fmt.Sprintf(
        "agent-idle:%s:%d",
        record.ID,
        record.StateChangedAt.UnixNano(),
    ),
    Message: fmt.Sprintf(
        "%s %s 작업이 완료되었습니다",
        displayName,
        record.ID,
    ),
}
~~~

Log enqueue failures without retrying or terminating the loop.

- [ ] **Step 4: Run daemon tests and verify GREEN**

~~~bash
gofmt -w internal/cli/daemon/agent_idle_voice.go internal/cli/daemon/agent_idle_voice_test.go
go test ./internal/cli/daemon -count=1
~~~

Expected: all daemon CLI tests pass.

- [ ] **Step 5: Commit the subscriber slice**

~~~bash
git add internal/cli/daemon/agent_idle_voice.go internal/cli/daemon/agent_idle_voice_test.go
git commit -m "feat: idle 상태 음성 알림 구독자 추가"
~~~

### Task 4: Wire subscriber ownership into daemon serve lifecycle

**Files:**
- Modify: internal/cli/daemon/daemon.go
- Modify: internal/cli/daemon/daemon_test.go

**Interfaces:**
- Consumes: Task 3 runAgentIdleVoiceLoop and the daemon voice service.
- Produces: daemonRuntimeBundle with service transport.ServerRuntime, bus event bus pointer, and store codingagent.Store.
- Preserves: newRuntimeService returning transport.ServerRuntime and error for existing callers.

- [ ] **Step 1: Write failing bundle and lifecycle tests**

Add TestNewRuntimeBundleRetainsSharedEventBusAndStore, asserting the returned
bus and store are the exact factory-produced instances and the service still
uses them.

Extend daemon serve lifecycle coverage with a real bus and memory store:
create an opted-in Record, publish a literal idle transition from the fake
server, wait for the exact direct Notification, return from ListenAndServe,
and assert subscriber shutdown occurs before voice Close.

- [ ] **Step 2: Run lifecycle tests and verify RED**

~~~bash
go test ./internal/cli/daemon -run 'TestNewRuntimeBundle|TestDaemon.*IdleVoice' -count=1
~~~

Expected: compilation fails because daemonRuntimeBundle/newRuntimeBundle do not
exist, or behavior fails because serve does not start the subscriber.

- [ ] **Step 3: Refactor construction and wire lifecycle**

Introduce:

~~~go
type daemonRuntimeBundle struct {
    service transport.ServerRuntime
    bus     *eventbus.Bus
    store   codingagent.Store
}

func newRuntimeService() (transport.ServerRuntime, error) {
    bundle, err := newRuntimeBundle()
    if err != nil {
        return nil, err
    }
    return bundle.service, nil
}
~~~

Move the existing constructor body into newRuntimeBundle. In serve, pass
bundle.service to transport and reconciliation. Subscribe to bundle.bus and
start the idle voice loop before server.ListenAndServe. After the server
returns, cancel serveCtx and wait for reconciliation and idle-voice goroutines
before returning, so deferred voice Close runs last.

- [ ] **Step 4: Run affected tests and verify GREEN**

~~~bash
gofmt -w internal/cli/daemon/daemon.go internal/cli/daemon/daemon_test.go
go test ./internal/cli/daemon ./internal/codingagent ./internal/voice -count=1
~~~

Expected: all affected packages pass.

- [ ] **Step 5: Commit lifecycle wiring**

~~~bash
git add internal/cli/daemon/daemon.go internal/cli/daemon/daemon_test.go
git commit -m "feat: daemon에 idle 음성 알림 연결"
~~~

### Task 5: Document, verify, build, and install

**Files:**
- Modify: README.md
- Modify: docs/zellij-agent-quickstart.md

**Interfaces:**
- Consumes: completed CLI behavior from Tasks 1-4.
- Produces: user-facing syntax and a verified unified binary.

- [ ] **Step 1: Document option and placement**

Add this example near existing agent start documentation:

~~~bash
zellij-agent agent start codex --notify-idle -- "Implement the requested change."
~~~

State that the flag is opt-in, announces each non-idle-to-idle transition, and
must appear before --.

- [ ] **Step 2: Run formatting and diff checks**

~~~bash
gofmt -w $(git diff --name-only -- '*.go')
git diff --check
~~~

Expected: both commands exit 0 without diagnostics.

- [ ] **Step 3: Run focused race tests**

~~~bash
go test -race ./internal/cli/daemon ./internal/codingagent ./internal/voice -count=1
~~~

Expected: all affected packages pass under the race detector.

- [ ] **Step 4: Run full test suite**

~~~bash
go test ./... -count=1
~~~

Expected: every package passes with zero failures.

- [ ] **Step 5: Build and atomically install unified binary**

~~~bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
~~~

Expected: all commands exit 0. Do not restart an already-running daemon; report
that it must be restarted later to load the new binary.

- [ ] **Step 6: Commit documentation**

~~~bash
git add README.md docs/zellij-agent-quickstart.md
git commit -m "docs: idle 음성 알림 사용법 추가"
~~~

- [ ] **Step 7: Inspect final state**

~~~bash
git status --short --branch
git log -6 --oneline
~~~

Expected: the feature branch is clean and Korean implementation commits follow
the design and plan commits.
