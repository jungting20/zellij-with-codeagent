# Idle Agent Next Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `zellij-agent agent next` cycle only through idle managed agents and silently succeed without changing focus when none are idle.

**Architecture:** Keep filtering, selection, runtime focus, and cursor advancement inside the daemon's existing focus mutex. Add an explicit `Focused` result bit through the domain and HTTP response so the CLI can distinguish a successful focus from a successful no-op without using an error or inspecting zero-value agent fields.

**Tech Stack:** Go 1.22+, standard `testing`, daemon-owned `codingagent.Service`, Unix-socket JSON HTTP transport, Zellij KDL integration.

## Global Constraints

- Reuse the existing `agent-next` default role; do not add another role.
- Only `codingagent.StateIdle` records are eligible; `working`, `blocked`, and `unknown` are excluded.
- Preserve creation-order sorting and wraparound among eligible records.
- Preserve the daemon-wide in-memory cursor and serialize selection through runtime focus with the existing focus mutex.
- A zero-idle request returns success, does not call runtime focus, does not change the cursor, and writes nothing on stdout or stderr.
- Direct `FocusAgent` behavior remains state-independent.
- Clients and roles must not call Zellij directly; focus remains routed through `RuntimeService`.
- Commit messages must be concise and written in Korean.
- Build and install the unified binary atomically; never overwrite the installed executable in place.

---

### Task 1: Filter daemon navigation to idle records

**Files:**
- Modify: `internal/codingagent/service.go:18-66,253-326`
- Test: `internal/codingagent/service_test.go:867-1084`

**Interfaces:**
- Consumes: ordered `[]codingagent.Record` from `Store.List()` and the existing `Service.lastFocusedID` cursor.
- Produces: `FocusNextAgentResponse{Focused bool, Agent AgentWithPane}` and `nextIdleAgentRecord(records []Record, current ID) (Record, bool)`.

- [ ] **Step 1: Write failing mixed-state and no-op service tests**

Update `seedFocusRecords` so the existing navigation tests explicitly create idle records instead of relying on `StateUnknown`, then add tests with these assertions:

```go
func TestServiceFocusNextAgentSelectsOnlyIdleAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateIdle, StateBlocked, StateIdle})
	runtimeService := successfulFocusRuntime()
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	request := FocusNextAgentRequest{SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1"}

	for call, wantID := range []ID{"agent-2", "agent-4", "agent-2"} {
		response, err := service.FocusNextAgent(context.Background(), request)
		if err != nil {
			t.Fatalf("FocusNextAgent() call %d error = %v", call+1, err)
		}
		if !response.Focused || response.Agent.Agent.ID != wantID {
			t.Fatalf("FocusNextAgent() call %d response = %#v, want focused %q", call+1, response, wantID)
		}
	}
}

func TestServiceFocusNextAgentDoesNothingWithoutIdleAgents(t *testing.T) {
	store := NewMemoryStore(nil)
	seedRecordsWithStates(t, store, []State{StateWorking, StateBlocked, StateUnknown})
	runtimeService := &serviceFakeRuntime{}
	service := NewService(ServiceOptions{RuntimeService: runtimeService, Store: store, LifecycleMonitor: &serviceFakeMonitor{}})
	service.lastFocusedID = "agent-2"

	response, err := service.FocusNextAgent(context.Background(), FocusNextAgentRequest{
		SourceZellijSession: "dashboard", SourceZellijPaneID: "terminal_1",
	})
	if err != nil || response.Focused || response.Agent.Agent.ID != "" {
		t.Fatalf("FocusNextAgent() response=%#v error=%v, want successful no-op", response, err)
	}
	if len(runtimeService.focused) != 0 || service.lastFocusedID != "agent-2" {
		t.Fatalf("no-op focus calls=%d cursor=%q", len(runtimeService.focused), service.lastFocusedID)
	}
}

func seedRecordsWithStates(t *testing.T, store Store, states []State) {
	t.Helper()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	kinds := []Kind{KindCodex, KindClaude, KindGemini, KindCursor}
	for index, state := range states {
		id := ID(fmt.Sprintf("agent-%d", index+1))
		_, err := store.Create(Record{
			ID: id, Kind: kinds[index%len(kinds)], PaneID: runtime.PaneID(fmt.Sprintf("pane-%d", index+1)),
			State: state, CreatedAt: base.Add(time.Duration(index) * time.Second),
			StateChangedAt: base.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatalf("Create(%q) error = %v", id, err)
		}
	}
}

func successfulFocusRuntime() *serviceFakeRuntime {
	return &serviceFakeRuntime{focusFn: func(_ context.Context, request runtime.FocusPaneRequest) (runtime.FocusPaneResponse, error) {
		return runtime.FocusPaneResponse{Pane: runtime.Pane{ID: request.PaneID, Status: runtime.PaneStatusRunning}}, nil
	}}
}
```

Also replace `TestServiceFocusNextAgentReturnsNoAgentsForEmptyStore` with an empty-store successful no-op test. Add coverage for a directly focused cursor record changing from idle to working, proving the next request restarts at the first idle record. Assert `Focused == true` in all existing successful next-focus tests.

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

```bash
go test ./internal/codingagent -run 'FocusNextAgent|FocusAgentUpdatesCursor' -count=1
```

Expected: compilation fails because `FocusNextAgentResponse.Focused` and idle-only selection do not exist, or behavior assertions show non-idle records being focused.

- [ ] **Step 3: Add the response bit and idle selection**

Remove `ErrNoAgents`, add the explicit result bit, and replace the selection helper:

```go
type FocusNextAgentResponse struct {
	Focused bool
	Agent   AgentWithPane
}

func nextIdleAgentRecord(records []Record, current ID) (Record, bool) {
	idle := make([]Record, 0, len(records))
	for _, record := range records {
		if record.State == StateIdle {
			idle = append(idle, record)
		}
	}
	if len(idle) == 0 {
		return Record{}, false
	}
	for index := range idle {
		if idle[index].ID == current {
			return idle[(index+1)%len(idle)], true
		}
	}
	return idle[0], true
}
```

Use it inside the existing mutex-protected method:

```go
	record, ok := nextIdleAgentRecord(records, s.lastFocusedID)
	if !ok {
		return FocusNextAgentResponse{Focused: false}, nil
	}
	response, err := s.focusAgentLocked(ctx, FocusAgentRequest{
		AgentID: record.ID, SourceZellijSession: sourceSession, SourceZellijPaneID: sourcePaneID,
	})
	if err != nil {
		return FocusNextAgentResponse{}, err
	}
	return FocusNextAgentResponse{Focused: true, Agent: response.Agent}, nil
```

Do not add state checks to `focusAgentLocked`; direct focus must remain unchanged.

- [ ] **Step 4: Run service and race tests**

Run:

```bash
gofmt -w internal/codingagent/service.go internal/codingagent/service_test.go
go test ./internal/codingagent -run 'FocusNextAgent|FocusAgentUpdatesCursor' -count=1
go test -race ./internal/codingagent -run 'FocusNextAgent|FocusAgentUpdatesCursor' -count=1
```

Expected: all focused tests pass, including mixed-state, zero-idle, cursor reset, failure preservation, and serialized concurrent selection.

- [ ] **Step 5: Commit the daemon behavior**

```bash
git add internal/codingagent/service.go internal/codingagent/service_test.go
git commit -m "feat: idle 에이전트만 순회하도록 제한"
```

---

### Task 2: Carry no-op status through the transport

**Files:**
- Modify: `internal/transport/types.go:124-132,372-374`
- Modify: `internal/transport/errors.go:60-72`
- Test: `internal/transport/server_test.go:35-70,130-170,726-776`
- Test: `internal/transport/client_test.go:45-72`

**Interfaces:**
- Consumes: `codingagent.FocusNextAgentResponse{Focused bool, Agent codingagent.AgentWithPane}` from Task 1.
- Produces: `transport.FocusNextAgentResponse{Focused bool, Agent transport.AgentWithPane}` serialized as JSON with `focused` and `agent` fields.

- [ ] **Step 1: Write failing HTTP conversion tests**

Extend the successful route test to require `focused: true`, then add a no-op server case whose fake service returns `codingagent.FocusNextAgentResponse{Focused: false}`:

```go
if !nextResponse.Focused || nextResponse.Agent.Agent.ID != "agent-2" {
	t.Fatalf("FocusNextAgent response = %#v", nextResponse)
}

func TestServerFocusNextAgentReturnsSuccessfulNoOp(t *testing.T) {
	service := newFakeRuntimeService()
	service.agentNextResponseSet = true
	service.agentNextResponse = codingagent.FocusNextAgentResponse{Focused: false}
	server := NewServer(ServerOptions{Service: service})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/agents/next",
		strings.NewReader(`{"source_session":"physical-b","source_zellij_pane_id":"terminal_8"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response FocusNextAgentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Focused || response.Agent.Agent.ID != "" {
		t.Fatalf("response=%#v, want no-op", response)
	}
}
```

Add `agentNextResponseSet bool` and `agentNextResponse codingagent.FocusNextAgentResponse` to the fake and make its default response explicitly focused. Remove the `ErrNoAgents` HTTP error-mapping case because zero agents is now HTTP 200. Extend the client test fixture to return both `focused: true` and `focused: false` JSON and assert decoding.

Use this fake behavior so individual tests can override the result without
changing unrelated route tests:

```go
func (f *fakeRuntimeService) FocusNextAgent(_ context.Context, req codingagent.FocusNextAgentRequest) (codingagent.FocusNextAgentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentNextCalls++
	f.agentNextReq = req
	if f.agentNextErr != nil {
		return codingagent.FocusNextAgentResponse{}, f.agentNextErr
	}
	if f.agentNextResponseSet {
		return f.agentNextResponse, nil
	}
	return codingagent.FocusNextAgentResponse{
		Focused: true,
		Agent:   fakeAgentResponse(codingagent.KindCodex, "agent-2").Agent,
	}, nil
}
```

For the client decoding case, serve the exact body
`{"focused":false,"agent":{}}`, call `Client.FocusNextAgent`, and require
`response.Focused == false` with no error.

- [ ] **Step 2: Run transport tests and verify they fail**

Run:

```bash
go test ./internal/transport -run 'Agent|FocusNext' -count=1
```

Expected: compilation or assertions fail because the transport response does not expose `Focused` and the fake cannot return a no-op response.

- [ ] **Step 3: Implement additive transport conversion**

Change the response and conversion:

```go
type FocusNextAgentResponse struct {
	Focused bool          `json:"focused"`
	Agent   AgentWithPane `json:"agent"`
}

func FocusNextAgentFromCodingAgent(response codingagent.FocusNextAgentResponse) FocusNextAgentResponse {
	return FocusNextAgentResponse{
		Focused: response.Focused,
		Agent:   AgentWithPaneFromCodingAgent(response.Agent),
	}
}
```

Remove `codingagent.ErrNoAgents` from `errorCodeFor` in `internal/transport/errors.go`. Keep the handler and client endpoint unchanged; they already encode and decode the typed response.

- [ ] **Step 4: Run transport tests**

Run:

```bash
gofmt -w internal/transport/types.go internal/transport/errors.go internal/transport/server_test.go internal/transport/client_test.go
go test ./internal/transport -run 'Agent|FocusNext' -count=1
go test ./internal/transport -count=1
```

Expected: focused and no-op responses both decode with HTTP 200, while existing validation and runtime errors retain their status mappings.

- [ ] **Step 5: Commit the transport contract**

```bash
git add internal/transport/types.go internal/transport/errors.go internal/transport/server_test.go internal/transport/client_test.go
git commit -m "feat: 에이전트 순회 무동작 응답 추가"
```

---

### Task 3: Make the CLI silently accept a no-op

**Files:**
- Modify: `internal/cli/agent/agent.go:69-125`
- Test: `internal/cli/agent/agent_test.go:55-150,467-524`

**Interfaces:**
- Consumes: `transport.FocusNextAgentResponse.Focused` from Task 2.
- Produces: exit 0 with no stdout/stderr for `Focused == false`; unchanged focused output for `Focused == true`.

- [ ] **Step 1: Write failing CLI no-op and call-count tests**

Make `focusedNext` set `Focused: true`, assert `nextCalls == 1` in success and client-error tests, and `nextCalls == 0` for every validation/preflight case that has a concrete client. Add:

```go
func TestRunNextSilentlySucceedsWhenNoIdleAgentExists(t *testing.T) {
	client := &testClient{nextResponse: transport.FocusNextAgentResponse{Focused: false}}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"next"}, strings.NewReader(""), &stdout, &stderr, testFactory(client), Config{
		Getenv: mapGetenv(map[string]string{
			"ZELLIJ_SESSION_NAME": "session-b",
			"ZELLIJ_PANE_ID":      "terminal_8",
		}),
	})
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 || client.nextCalls != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q calls=%d", code, stdout.String(), stderr.String(), client.nextCalls)
	}
}
```

- [ ] **Step 2: Run CLI tests and verify they fail**

Run:

```bash
go test ./internal/cli/agent -run 'RunNext' -count=1
```

Expected: the no-op test fails because the CLI prints an empty focused-agent line, and existing helpers do not mark successful responses as focused.

- [ ] **Step 3: Return before formatting a no-op response**

Add this immediately after the existing client error branch:

```go
	if !response.Focused {
		return 0
	}
```

Update `focusedNext`:

```go
func focusedNext(id, kind, agentPaneID, paneID string) transport.FocusNextAgentResponse {
	return transport.FocusNextAgentResponse{Focused: true, Agent: transport.AgentWithPane{
		Agent: transport.Agent{ID: id, Kind: kind, PaneID: agentPaneID},
		Pane:  transport.Pane{ID: paneID},
	}}
}
```

- [ ] **Step 4: Run CLI and unified command tests**

Run:

```bash
gofmt -w internal/cli/agent/agent.go internal/cli/agent/agent_test.go
go test ./internal/cli/agent ./cmd/zellij-agent ./cmd/agent-role/agentnext -count=1
```

Expected: focused output remains unchanged, no-idle output is empty with exit 0, and validation paths make zero RPCs.

- [ ] **Step 5: Commit the CLI behavior**

```bash
git add internal/cli/agent/agent.go internal/cli/agent/agent_test.go
git commit -m "feat: idle 에이전트 부재 시 조용히 종료"
```

---

### Task 4: Document, verify, rebuild, and install

**Files:**
- Modify: `README.md:107-115`
- Modify: `docs/manual-smoke-test.md:176-191`
- Build: `bin/agent-role`
- Build: `bin/zellij-agent`
- Install: `/Users/in05908_mac/.config/custom-cli/zellij-agent`
- Audit: `/Users/in05908_mac/.config/zellij/config.kdl`

**Interfaces:**
- Consumes: the idle-only, silent no-op behavior completed in Tasks 1-3.
- Produces: user documentation, verified binaries, and an atomically installed unified CLI.

- [ ] **Step 1: Update usage and smoke documentation**

Change README wording to say navigation visits only agents whose detected state is `idle`, retains creation-order wraparound among that filtered set, and silently does nothing when no idle agents exist. Document the current global Zellij shortcut as repeated `Alt+o` presses instead of the obsolete `Alt+e` then `Tab` sequence.

Update the manual smoke test to create at least four agents and verify:

```text
idle → working → blocked → idle
```

Repeated `Alt+o` must visit only the two idle agents and wrap. Change one of those idle agents to working and confirm the remaining idle agent is selected. Change every agent to a non-idle state and confirm both `Alt+o` and `zellij-agent agent next` leave focus unchanged and produce no visible CLI error/output.

- [ ] **Step 2: Check and commit documentation**

Run:

```bash
git diff --check
rg -n 'idle|Alt\+o|wrap|no idle|silently' README.md docs/manual-smoke-test.md
```

Expected: both files describe the idle-only filter, global shortcut, wraparound, and no-op case.

Commit:

```bash
git add README.md docs/manual-smoke-test.md
git commit -m "docs: idle 에이전트 순회 사용법 추가"
```

- [ ] **Step 3: Run formatting, focused race tests, and the full suite**

Run:

```bash
gofmt -w internal/codingagent/*.go internal/transport/*.go internal/cli/agent/*.go
go test -race ./internal/codingagent -run 'FocusNextAgent|FocusAgentUpdatesCursor' -count=1
go test ./internal/transport ./internal/cli/agent ./cmd/zellij-agent ./cmd/agent-role/agentnext -count=1
go test ./... -count=1
git diff --check
if rg -n 'internal/zellij|exec\.Command.*zellij' internal/cli/agent cmd/agent-role/agentnext; then exit 1; fi
```

Expected: all tests pass, the worktree has no formatting diff, and the boundary scan produces no matches.

- [ ] **Step 4: Build and inspect both binaries**

Run:

```bash
go build -o bin/agent-role ./cmd/agent-role
go build -o bin/zellij-agent ./cmd/zellij-agent
./bin/agent-role roles | rg '^agent-next'
./bin/zellij-agent agent next --help
```

Expected: `agent-next` remains registered and unified CLI help succeeds.

- [ ] **Step 5: Install the unified CLI atomically**

Run:

```bash
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
cmp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/zellij-agent
/Users/in05908_mac/.config/custom-cli/zellij-agent agent next --help
```

Expected: installed and built binaries are byte-identical and help exits 0.

- [ ] **Step 6: Audit the global Zellij binding**

Run:

```bash
test "$(rg -c 'Run "zellij-agent" "agent" "next"' /Users/in05908_mac/.config/zellij/config.kdl)" -eq 1
rg -n -A8 -B3 'bind "Alt o"|Run "zellij-agent" "agent" "next"' /Users/in05908_mac/.config/zellij/config.kdl
zellij setup --check
```

Expected: one `Alt+o` agent-next binding exists under a non-locked shared binding and the configuration is well defined. Do not rewrite the external configuration during this task.

- [ ] **Step 7: Final audit**

Run:

```bash
git status --short
git log --oneline 94a8f6d..HEAD
```

Expected: only intentional Korean commits are present and the tracked worktree is clean. Record the real-Zellij idle-state smoke as verified only if it was actually exercised without disrupting unrelated active sessions.
