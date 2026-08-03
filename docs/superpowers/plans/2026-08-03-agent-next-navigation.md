
# Agent Next Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add zellij-agent agent next and a Zellij tab-mode Tab binding that cycles through managed coding-agent panes across sessions.

**Architecture:** The daemon owns one mutex-protected in-memory focus cursor and exposes POST /v1/agents/next. The CLI and required default role use that transport boundary; only RuntimeService performs Zellij switching. The local Zellij configuration launches the one-shot CLI from tab mode.

**Tech Stack:** Go, standard testing, Unix-socket HTTP transport, Zellij 0.44.1 KDL.

## Global Constraints

- Keep focus behavior behind codingagent.Service and RuntimeService.FocusPane; client and role code must not invoke Zellij directly.
- Add the repository-required default role before other feature surfaces.
- Order by CreatedAt, then agent ID, matching Store.List.
- Use one daemon-wide in-memory cursor shared by attached clients.
- Advance only after successful focus; a missing cursor target restarts at the first agent.
- Do not add previous navigation, persistence, per-client cursors, or agent-dashboard keys.
- Replace only tab-mode bind "tab" { ToggleTab; } in /Users/in05908_mac/.config/zellij/config.kdl.
- Run gofmt and go test ./...; commit messages must be Korean; install through .zellij-agent.new.

---

### Task 1: Add the required agent-next default role

**Files:**
- Modify: internal/roles/roles.go
- Modify: internal/roles/roles_test.go
- Create: cmd/agent-role/agentnext/agentnext.go
- Create: cmd/agent-role/agentnext/agentnext_test.go
- Modify: internal/cli/role/role.go
- Modify: internal/cli/role/role_test.go
- Modify: /Users/in05908_mac/.config/pi/docs/agent-roles.md

**Interfaces:**
- Consumes: zellij-agent on PATH.
- Produces: roles.RoleAgentNext and agentnext.Run(args []string) int.

- [ ] **Step 1: Write failing tests**

Add a catalog test requiring usage agent-next [--socket PATH --timeout DURATION] and optional --socket/--timeout ArgumentSpec entries. Create wrapper tests with a fake zellij-agent and assert:

~~~go
cmd, err := prepare([]string{"--socket", "/tmp/test.sock"})
// cmd.Args == []string{fakeBinary, "agent", "next", "--socket", "/tmp/test.sock"}
~~~

Make the fake exit 7 and assert Run(nil) == 7. Add a role dispatch test asserting agent-next --timeout 3s invokes agent next --timeout 3s.

- [ ] **Step 2: Verify RED**

~~~bash
go test ./internal/roles ./cmd/agent-role/agentnext ./internal/cli/role -run 'AgentNext' -count=1
~~~

Expected: FAIL because the constant, package, and dispatch are absent.

- [ ] **Step 3: Implement role metadata and wrapper**

~~~go
const RoleAgentNext = "agent-next"

{
	Name:        RoleAgentNext,
	Usage:       "agent-next [--socket PATH --timeout DURATION]",
	Description: "Focuses the next managed coding agent through the daemon.",
	Arguments: []ArgumentSpec{
		{Name: "--socket", Required: false, Description: "agentd Unix socket path."},
		{Name: "--timeout", Required: false, Description: "Daemon request timeout."},
	},
}
~~~

Implement prepare with exec.LookPath("zellij-agent") and exec.Command(binary, append([]string{"agent", "next"}, args...)...). Run wires stdio, returns child ExitError codes, and prints concise lookup/run failures. Import and dispatch the package from role.go.

- [ ] **Step 4: Update external role docs**

Add agent-next to the table and detail section. Document agent-role agent-next [--socket PATH --timeout DURATION], active-Zellij and daemon requirements, and delegation to zellij-agent agent next.

- [ ] **Step 5: Verify and commit**

~~~bash
gofmt -w internal/roles/roles.go internal/roles/roles_test.go cmd/agent-role/agentnext/agentnext.go cmd/agent-role/agentnext/agentnext_test.go internal/cli/role/role.go internal/cli/role/role_test.go
go test ./internal/roles ./cmd/agent-role/agentnext ./internal/cli/role -run 'AgentNext' -count=1
git add internal/roles cmd/agent-role/agentnext internal/cli/role
git commit -m "feat: 에이전트 순회 기본 역할 추가"
~~~

Expected: PASS.

---

### Task 2: Add daemon-owned next-agent selection

**Files:**
- Modify: internal/codingagent/service.go
- Modify: internal/codingagent/service_test.go

**Interfaces:**
- Consumes: sorted Store.List and RuntimeService.FocusPane.
- Produces: FocusNextAgentRequest, FocusNextAgentResponse, AgentService.FocusNextAgent, ErrNoAgents.

- [ ] **Step 1: Write failing tests**

Seed three records out of insertion order and verify first, consecutive, and wrapped selection. Also verify direct FocusAgent(agent-2) makes next choose agent-3; deleted cursor target restarts at agent-1; empty store returns ErrNoAgents; blank source returns ErrAgentSourceRequired; failed focus does not advance; two concurrent calls serialize to consecutive IDs. Add focusFn to serviceFakeRuntime to block the first concurrency call.

- [ ] **Step 2: Verify RED**

~~~bash
go test ./internal/codingagent -run 'FocusNextAgent|FocusAgentUpdatesCursor' -count=1
~~~

Expected: FAIL because the new contract is absent.

- [ ] **Step 3: Implement contract and cursor**

~~~go
var ErrNoAgents = errors.New("no managed coding agents")

type FocusNextAgentRequest struct {
	SourceZellijSession string
	SourceZellijPaneID  runtime.ZellijPaneID
}

type FocusNextAgentResponse struct {
	Agent AgentWithPane
}
~~~

Extend AgentService with FocusNextAgent. Add focusMu sync.Mutex and lastFocusedID ID to Service. Refactor the current focus body into focusAgentLocked. Both public focus methods hold focusMu; only a successful non-terminal result assigns lastFocusedID.

Use:

~~~go
func nextAgentRecord(records []Record, current ID) (Record, error) {
	if len(records) == 0 {
		return Record{}, ErrNoAgents
	}
	for index := range records {
		if records[index].ID == current {
			return records[(index+1)%len(records)], nil
		}
	}
	return records[0], nil
}
~~~

FocusNextAgent validates trimmed source context before Store.List, selects once, calls focusAgentLocked once, and converts the response without a second runtime call.

- [ ] **Step 4: Verify and commit**

~~~bash
gofmt -w internal/codingagent/service.go internal/codingagent/service_test.go
go test ./internal/codingagent -count=1
git add internal/codingagent/service.go internal/codingagent/service_test.go
git commit -m "feat: 다음 에이전트 포커스 서비스 추가"
~~~

Expected: all coding-agent tests pass.

---

### Task 3: Expose next focus through transport

**Files:**
- Modify: internal/transport/types.go
- Modify: internal/transport/handlers_agents.go
- Modify: internal/transport/server.go
- Modify: internal/transport/errors.go
- Modify: internal/transport/client.go
- Modify: internal/transport/server_test.go
- Modify: internal/transport/client_test.go

**Interfaces:**
- Consumes: codingagent.Service.FocusNextAgent.
- Produces: POST /v1/agents/next and Client.FocusNextAgent.

- [ ] **Step 1: Write failing tests**

Extend fakeRuntimeService with next request/call/error fields and a FocusNextAgent method returning agent-2. Send POST /v1/agents/next with source_session physical-b and source_zellij_pane_id terminal_8; assert status 200, exact conversion, one dispatch, and agent-2. Add malformed/trailing JSON, wrong method, ErrNoAgents -> 404/not_found, and source-required tests. Extend TestClientAgentMethodsUseExactPathsMethodsAndEscaping to require POST /v1/agents/next and exact request JSON.

- [ ] **Step 2: Verify RED**

~~~bash
go test ./internal/transport -run 'AgentRoutes|ClientAgentMethods' -count=1
~~~

Expected: FAIL because route, types, and client method are absent.

- [ ] **Step 3: Implement wire contract**

Create FocusNextAgentRequest with source_session and source_zellij_pane_id JSON fields and FocusNextAgentResponse with agent. Add conversion helpers. Add handleNextAgent accepting only POST and route it before the generic prefix:

~~~go
case r.URL.Path == "/v1/agents/next":
	s.handleNextAgent(w, r)
case strings.HasPrefix(r.URL.Path, "/v1/agents/"):
	s.handleAgentAction(w, r)
~~~

Add:

~~~go
func (c *Client) FocusNextAgent(ctx context.Context, req FocusNextAgentRequest) (FocusNextAgentResponse, error) {
	var response FocusNextAgentResponse
	err := c.do(ctx, http.MethodPost, "/v1/agents/next", req, &response)
	return response, err
}
~~~

Map ErrNoAgents with not-found errors in ErrorFor.

- [ ] **Step 4: Verify and commit**

~~~bash
gofmt -w internal/transport/types.go internal/transport/handlers_agents.go internal/transport/server.go internal/transport/errors.go internal/transport/client.go internal/transport/server_test.go internal/transport/client_test.go
go test ./internal/transport -count=1
git add internal/transport
git commit -m "feat: 다음 에이전트 포커스 API 추가"
~~~

Expected: all transport tests pass.

---

### Task 4: Add zellij-agent agent next

**Files:**
- Modify: internal/cli/agent/agent.go
- Modify: internal/cli/agent/agent_test.go
- Modify: cmd/zellij-agent/main_test.go

**Interfaces:**
- Consumes: AgentClient.FocusNextAgent and Config.Getenv.
- Produces: zellij-agent agent next [--socket PATH --timeout DURATION].

- [ ] **Step 1: Write failing CLI tests**

Extend testClient with next response/error/request/calls. Call:

~~~go
code := Run(
	[]string{"next", "--socket", "/tmp/next.sock", "--timeout", "3s"},
	strings.NewReader(""), &stdout, &stderr, testFactory(client),
	Config{Getenv: mapGetenv(map[string]string{
		"ZELLIJ_SESSION_NAME": " session-b ",
		"ZELLIJ_PANE_ID":      " 8 ",
	})},
)
~~~

Assert exit 0, empty stderr, socket/timeout forwarding, source session-b and terminal_8, and stdout focused agent=agent-2 kind=claude pane=agent-2. Add cases for positional args, non-positive timeout, missing Getenv/context, nil client, daemon error, and group/subcommand help.

- [ ] **Step 2: Verify RED**

~~~bash
go test ./internal/cli/agent ./cmd/zellij-agent -run 'Next|Agent.*Help' -count=1
~~~

Expected: FAIL because next is unknown.

- [ ] **Step 3: Implement CLI**

Dispatch next to runNext. Parse --socket and --timeout with flag.FlagSet, reject positional args and non-positive timeout, require normalized Zellij context, and reject nil factory/client.

~~~go
response, err := client.FocusNextAgent(ctx, transport.FocusNextAgentRequest{
	SourceSession:      session,
	SourceZellijPaneID: paneID,
})
~~~

Use Agent.PaneID with Pane.ID fallback and print:

~~~go
fmt.Fprintf(stdout, "focused agent=%s kind=%s pane=%s\n",
	response.Agent.Agent.ID, response.Agent.Agent.Kind, focusedPaneID)
~~~

Update all help text.

- [ ] **Step 4: Verify and commit**

~~~bash
gofmt -w internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main_test.go
go test ./internal/cli/agent ./cmd/zellij-agent -count=1
git add internal/cli/agent cmd/zellij-agent/main_test.go
git commit -m "feat: 다음 에이전트 이동 명령 추가"
~~~

Expected: focused CLI tests pass.

---

### Task 5: Document and configure tab-mode cycling

**Files:**
- Modify: README.md
- Modify: docs/manual-smoke-test.md
- Modify: /Users/in05908_mac/.config/zellij/config.kdl

- [ ] **Step 1: Update docs**

Add zellij-agent agent next. Document creation-order wraparound, daemon-wide in-memory cursor, and Alt+e followed by repeated Tab. Add a smoke section for agents in two sessions, first/next/wrap, deleted cursor recovery, and normal Tab outside tab mode.

- [ ] **Step 2: Check and commit docs**

~~~bash
git diff --check
rg -n 'agent next|Alt\+e|creation order|wrap' README.md docs/manual-smoke-test.md
git add README.md docs/manual-smoke-test.md
git commit -m "docs: 에이전트 순회 사용법 추가"
~~~

- [ ] **Step 3: Replace exact local binding**

Replace only:

~~~kdl
bind "tab" { ToggleTab; }
~~~

with:

~~~kdl
bind "tab" {
    Run "zellij-agent" "agent" "next" {
        floating true
        close_on_exit true
        borderless true
    }
}
~~~

Do not modify move mode's Tab binding or normal/shared bindings.

- [ ] **Step 4: Validate config**

~~~bash
rg -n -A7 -B2 'Run "zellij-agent" "agent" "next"' /Users/in05908_mac/.config/zellij/config.kdl
zellij setup --check
~~~

Expected: one next-agent binding and valid configuration.

---

### Task 6: Verify, review, build, and install

**Files:**
- Build: bin/agent-role
- Build: bin/zellij-agent
- Install: /Users/in05908_mac/.config/custom-cli/zellij-agent

- [ ] **Step 1: Run focused and race tests**

~~~bash
gofmt -w internal/codingagent/*.go internal/transport/*.go internal/cli/agent/*.go internal/roles/*.go internal/cli/role/*.go cmd/agent-role/agentnext/*.go cmd/zellij-agent/*.go
go test -race ./internal/codingagent -run 'FocusAgent|FocusNextAgent' -count=1
go test ./internal/transport ./internal/cli/agent ./internal/roles ./internal/cli/role ./cmd/agent-role/agentnext ./cmd/zellij-agent -count=1
~~~

- [ ] **Step 2: Run full regression and boundary checks**

~~~bash
go test ./... -count=1
git diff --check
if rg -n 'internal/zellij|exec\.Command.*zellij' internal/cli/agent cmd/agent-role/agentnext; then exit 1; fi
~~~

Expected: all pass and no direct Zellij calls exist.

- [ ] **Step 3: Request code review**

Use superpowers:requesting-code-review for the implementation after design commit 3a44513. Review spec compliance, cursor concurrency, route precedence, validation, role behavior, and unintended edits. Apply valid findings and rerun affected tests.

- [ ] **Step 4: Build binaries**

~~~bash
go build -o bin/agent-role ./cmd/agent-role
go build -o bin/zellij-agent ./cmd/zellij-agent
./bin/agent-role roles | rg '^agent-next'
./bin/zellij-agent agent next --help
~~~

- [ ] **Step 5: Install atomically**

~~~bash
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
cmp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/zellij-agent
/Users/in05908_mac/.config/custom-cli/zellij-agent agent next --help
~~~

- [ ] **Step 6: Perform real-Zellij smoke**

Reload config or start a fresh client. Create agents in two sessions, enter tab mode with Alt+e, and press Tab repeatedly. Verify ordering, cross-session switching, wrap, continued tab mode, and normal application Tab.

- [ ] **Step 7: Final audit**

~~~bash
git status --short
git log --oneline 3a44513..HEAD
rg -n 'agent-next' /Users/in05908_mac/.config/pi/docs/agent-roles.md
rg -n -A7 -B2 'Run "zellij-agent" "agent" "next"' /Users/in05908_mac/.config/zellij/config.kdl
~~~

Expected: only intentional changes remain, commits are Korean, external role docs are current, and the binding exists once.
