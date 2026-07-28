# Coding Agent Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run four supported coding agents in a new pane on the current Zellij tab, detect their work state in the daemon, show them in a dedicated dashboard, and focus the selected pane.

**Architecture:** A new `internal/codingagent` domain owns profiles, an in-memory record store, manifest evaluation, monitoring, and agent use cases while embedding the existing `RuntimeService`. Runtime subscriptions feed rendered screens to the monitor, agent-specific transport endpoints expose records and focus actions, and a separate Bubble Tea dashboard renders only agent data.

**Tech Stack:** Go 1.26, Go standard library (`embed`, `regexp`, `sync`, `time`), YAML v3, Bubble Tea, Lip Gloss, existing Unix-socket JSON transport, Zellij 0.44.1 CLI

## Global Constraints

- Manage only coding agents started through `zellij-agent agent start`.
- Support exactly `codex`, `claude`, `gemini`, and `cursor` in the first version.
- Default commands include the approved permission-bypass arguments.
- Start each agent in a new pane on the caller's current Zellij tab.
- Keep pane lifecycle status separate from `idle`, `working`, `blocked`, and `unknown` agent state.
- The daemon owns state detection; the dashboard never evaluates manifests.
- The first version uses an in-memory agent store and does not recover agents after daemon restart.
- The first version displays state and focuses panes; it does not send prompts, stop agents, or emit OS notifications.
- Route pane creation, screen observations, focus, and cleanup through `RuntimeService` and transport boundaries.
- Preserve the existing runtime dashboard behavior.
- Commit messages are written in Korean.
- After unified binary changes, build and install atomically at `~/.config/custom-cli/zellij-agent`.

---

### Task 1: Generalize the default coding-agent role and add command profiles

**Files:**
- Create: `internal/codingagent/profile.go`
- Create: `internal/codingagent/profile_test.go`
- Modify: `cmd/agent-role/codingagent/codingagent.go`
- Modify: `cmd/agent-role/codingagent/codingagent_test.go`
- Modify: `internal/roles/roles.go`
- Modify: `internal/cli/role/role_test.go`

**Interfaces:**
- Produces: `type Kind string` and constants `KindCodex`, `KindClaude`, `KindGemini`, `KindCursor`
- Produces: `func ParseKind(string) (Kind, error)`
- Produces: `func LookupProfile(Kind) (Profile, bool)`
- Produces: `func (Profile) BuildCommand(bypass bool, extra []string) []string`
- Preserves: legacy `agent-role coding-agent [--yolo] <path>` as Codex
- Adds: `agent-role coding-agent --agent <kind> [--yolo] <path> [-- <agent-args...>]`

- [ ] **Step 1: Write failing profile tests**

Create table tests with these exact expectations:

```go
tests := []struct {
	kind codingagent.Kind
	want []string
}{
	{codingagent.KindCodex, []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}},
	{codingagent.KindClaude, []string{"claude", "--dangerously-skip-permissions"}},
	{codingagent.KindGemini, []string{"agy", "--dangerously-skip-permissions"}},
	{codingagent.KindCursor, []string{"agent", "--yolo", "--trust"}},
}
for _, tt := range tests {
	profile, ok := codingagent.LookupProfile(tt.kind)
	if !ok {
		t.Fatalf("LookupProfile(%q) missing", tt.kind)
	}
	if got := profile.BuildCommand(true, nil); !slices.Equal(got, tt.want) {
		t.Fatalf("BuildCommand() = %#v, want %#v", got, tt.want)
	}
}
```

Also assert that `BuildCommand(false, []string{"--model", "custom"})` omits bypass arguments and appends the two extra arguments, and that `ParseKind("agy")` and `ParseKind("agent")` fail because those are executable names rather than public kinds.

- [ ] **Step 2: Run the profile tests to verify RED**

```bash
go test ./internal/codingagent -run 'Test(Profile|ParseKind)' -count=1
```

Expected: FAIL because the package and types do not exist.

- [ ] **Step 3: Implement immutable profiles**

Use this shape and return cloned slices from every public method:

```go
type Kind string

const (
	KindCodex  Kind = "codex"
	KindClaude Kind = "claude"
	KindGemini Kind = "gemini"
	KindCursor Kind = "cursor"
)

type Profile struct {
	Kind        Kind
	DisplayName string
	Executable  string
	BypassArgs  []string
	Manifest    string
}
```

Set manifest names to `codex.yaml`, `claude.yaml`, `gemini.yaml`, and `cursor.yaml`. `BuildCommand` starts with `Executable`, conditionally adds `BypassArgs`, and finally appends a clone of `extra`.

- [ ] **Step 4: Write failing generalized-role tests**

Keep the existing Codex tests and add fake executables for `claude`, `agy`, and `agent`. Assert, for example:

```go
cmd, err := prepare([]string{"--agent", "gemini", "--yolo", repo, "--", "--model", "gemini-3"})
if err != nil {
	t.Fatal(err)
}
want := []string{agyPath, "--dangerously-skip-permissions", "--model", "gemini-3"}
if !slices.Equal(cmd.Args, want) {
	t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, want)
}
```

Add rejection tests for unknown kinds, a missing path, and more than one pre-`--` path. Assert the role catalog describes `coding-agent [--agent kind] [--yolo] <path> [-- agent-args...]`.

- [ ] **Step 5: Implement the role parser and catalog change**

Split arguments once at the literal `--`, parse the left side with `flag.FlagSet`, default `--agent` to `codex`, resolve the repository as today, look up the executable with `exec.LookPath`, and construct the command from the profile. Keep `--yolo` optional for direct role compatibility even though the new `agent start` use case always requests bypass mode.

- [ ] **Step 6: Format, verify GREEN, and commit**

```bash
gofmt -w internal/codingagent/profile.go internal/codingagent/profile_test.go cmd/agent-role/codingagent/codingagent.go cmd/agent-role/codingagent/codingagent_test.go internal/roles/roles.go internal/cli/role/role_test.go
go test ./internal/codingagent ./cmd/agent-role/codingagent ./internal/cli/role -count=1
git add internal/codingagent/profile.go internal/codingagent/profile_test.go cmd/agent-role/codingagent/codingagent.go cmd/agent-role/codingagent/codingagent_test.go internal/roles/roles.go internal/cli/role/role_test.go
git commit -m "feat: 코딩 에이전트 기본 역할 일반화"
```

Expected: profile and role tests PASS without changing legacy Codex behavior.

### Task 2: Add the agent record model and in-memory store

**Files:**
- Create: `internal/codingagent/types.go`
- Create: `internal/codingagent/store.go`
- Create: `internal/codingagent/store_test.go`

**Interfaces:**
- Produces: `type ID string`
- Produces: `type State string` with `StateIdle`, `StateWorking`, `StateBlocked`, `StateUnknown`
- Produces: `type Record`, `type StateUpdate`, and `type StateChange`
- Produces: `type Store interface { Create; Get; GetByPane; List; UpdateState; Delete }`
- Produces: `func NewMemoryStore(func() time.Time) Store`

- [ ] **Step 1: Write failing store contract tests**

Use a deterministic clock and this record shape:

```go
record := codingagent.Record{
	ID:             "agent-1",
	Kind:           codingagent.KindCodex,
	PaneID:         runtime.PaneID("agent-1"),
	State:          codingagent.StateUnknown,
	CreatedAt:      time.Unix(10, 0),
	StateChangedAt: time.Unix(10, 0),
}
```

Test create/get cloning, duplicate ID rejection, duplicate pane rejection, stable `CreatedAt` sorting from `List`, `GetByPane`, delete, and not-found errors. For `UpdateState`, assert an identical state/reason/rule returns `Changed=false` and does not move `StateChangedAt`; changing to working returns previous/current states and moves the timestamp once.

- [ ] **Step 2: Verify the store tests RED**

```bash
go test ./internal/codingagent -run TestMemoryStore -count=1
```

Expected: FAIL because store types do not exist.

- [ ] **Step 3: Implement the store with explicit atomic state changes**

Define:

```go
type Record struct {
	ID             ID
	Kind           Kind
	PaneID         runtime.PaneID
	State          State
	StateReason    string
	MatchedRule    string
	CreatedAt      time.Time
	StateChangedAt time.Time
}

type StateUpdate struct {
	State       State
	Reason      string
	MatchedRule string
}

type StateChange struct {
	Previous Record
	Current  Record
	Changed  bool
}
```

Protect `byID` and `byPane` maps with `sync.RWMutex`. Validate non-empty IDs, kinds, pane IDs, and state values on `Create` and `UpdateState`. Return value copies; no map or slice owned by the store may escape.

- [ ] **Step 4: Format, verify GREEN, and commit**

```bash
gofmt -w internal/codingagent/types.go internal/codingagent/store.go internal/codingagent/store_test.go
go test ./internal/codingagent -run TestMemoryStore -count=1
git add internal/codingagent/types.go internal/codingagent/store.go internal/codingagent/store_test.go
git commit -m "feat: 코딩 에이전트 상태 저장소 추가"
```

### Task 3: Implement the manifest evaluation engine

**Files:**
- Create: `internal/codingagent/manifest.go`
- Create: `internal/codingagent/regions.go`
- Create: `internal/codingagent/matcher.go`
- Create: `internal/codingagent/detector.go`
- Create: `internal/codingagent/detector_test.go`

**Interfaces:**
- Produces: `type DetectionInput struct { Screen, OSCTitle, OSCProgress string }`
- Produces: `type Detection struct { State, RuleID, Reason, VisibleIdle, VisibleWorking, VisibleBlocker, SkipStateUpdate, Fallback }`
- Produces: `func LoadManifest([]byte) (Manifest, error)`
- Produces: `func NewDetector(map[Kind]Manifest) (*Detector, error)`
- Produces: `func (d *Detector) Detect(Kind, DetectionInput) (Detection, error)`

- [ ] **Step 1: Write failing manifest parse and validation tests**

Parse a minimal YAML manifest:

```yaml
version: 1
agent: codex
rules:
  - id: blocked
    priority: 100
    state: blocked
    region:
      type: bottom_non_empty_lines
      lines: 3
    match:
      all:
        - contains: ["allow command?"]
        - not:
            - contains: ["conversation interrupted"]
    visible_blocker: true
```

Reject an unknown state, unknown region, zero `lines` for `bottom_non_empty_lines`, duplicate rule ID, a matcher with no operator, invalid regexp, and a manifest whose `agent` does not match the detector map key.

- [ ] **Step 2: Write failing region and matcher tests**

Cover these exact behaviors:

- `contains` is case-insensitive and requires every listed string.
- `regex` applies every expression to the whole selected region.
- `line_regex` requires each expression to match at least one line.
- `all`, `any`, and `not` recurse and have deterministic empty-list validation.
- `bottom_non_empty_lines(2)` includes the bottom two non-empty lines plus intervening blank lines through the screen end.
- `after_last_prompt_marker` uses the content after the last Codex `›`, falling back to the full screen.
- `prompt_box_body` selects the Claude prompt-box body between its upper border and next horizontal rule.
- `after_last_horizontal_rule` selects content after the last horizontal rule.
- OSC regions select only their corresponding input field.

- [ ] **Step 3: Verify detector tests RED**

```bash
go test ./internal/codingagent -run 'Test(LoadManifest|Region|Matcher|Detector)' -count=1
```

Expected: FAIL because the engine does not exist.

- [ ] **Step 4: Implement typed YAML loading and precompiled matchers**

Use `gopkg.in/yaml.v3`. Keep YAML DTOs private, convert them at load time to validated internal rules, and compile regexps once. Use this rule shape:

```go
type Rule struct {
	ID              string
	Priority        int
	State           State
	Region          Region
	Matcher         Matcher
	VisibleIdle     bool
	VisibleWorking  bool
	VisibleBlocker  bool
	SkipStateUpdate bool
	Order           int
}
```

Sort evaluation candidates by descending priority and then ascending declaration order. If a rule with `SkipStateUpdate` matches, return a detection with that flag and do not fabricate a new state. If no rule matches a known kind, return `StateIdle`, `Fallback=true`, and reason `default_known_agent_idle_fallback`.

- [ ] **Step 5: Format, verify GREEN, and commit**

```bash
gofmt -w internal/codingagent/manifest.go internal/codingagent/regions.go internal/codingagent/matcher.go internal/codingagent/detector.go internal/codingagent/detector_test.go
go test ./internal/codingagent -run 'Test(LoadManifest|Region|Matcher|Detector)' -count=1
git add internal/codingagent/manifest.go internal/codingagent/regions.go internal/codingagent/matcher.go internal/codingagent/detector.go internal/codingagent/detector_test.go
git commit -m "feat: 에이전트 상태 매니페스트 엔진 추가"
```

### Task 4: Encode and verify all four agent manifests

**Files:**
- Create: `internal/codingagent/manifests.go`
- Create: `internal/codingagent/manifests/codex.yaml`
- Create: `internal/codingagent/manifests/claude.yaml`
- Create: `internal/codingagent/manifests/gemini.yaml`
- Create: `internal/codingagent/manifests/cursor.yaml`
- Create: `internal/codingagent/testdata/codex/*.txt`
- Create: `internal/codingagent/testdata/claude/*.txt`
- Create: `internal/codingagent/testdata/gemini/*.txt`
- Create: `internal/codingagent/testdata/cursor/*.txt`
- Create: `internal/codingagent/manifests_test.go`

**Interfaces:**
- Produces: `func LoadEmbeddedDetector() (*Detector, map[Kind]error)`
- Consumes the engine from Task 3.
- Treats `docs/agent-status-detection.md` sections 2 through 5 as the rule source of truth.

- [ ] **Step 1: Write failing embedded-manifest tests**

Create fixture-driven tests whose filenames declare the expected state, such as `working-screen.txt`, `blocked-approval.txt`, and `idle-prompt.txt`. At minimum cover:

| Kind | Required fixture assertions |
|---|---|
| Codex | OSC Action Required blocked; OSC spinner working; transcript viewer skip; live strong blocker blocked; weak yes/no blocker blocked; bottom Working footer working; unmatched idle fallback |
| Gemini | apply/allow confirmation blocked wins over `esc to cancel`; working cancel hint; unmatched idle fallback |
| Cursor | write-file approval blocked; command approval blocked; stop hint working; non-zero background task working; spinner `...ing` working; unmatched idle fallback |
| Claude | OSC spinner working; transcript viewer skip; live selection form blocked; dynamic workflow blocked; `/btw` overlay working; prompt box idle; model picker skip; Bash permission blocked; generic permission blocked; legacy blocker blocked; OSC idle title; OSC progress idle |

For every rule assert `RuleID`, priority winner, visible flags, and `SkipStateUpdate` where applicable, not only the resulting state.

- [ ] **Step 2: Verify manifest tests RED**

```bash
go test ./internal/codingagent -run TestEmbeddedManifests -count=1
```

Expected: FAIL because embedded manifests and fixtures do not exist.

- [ ] **Step 3: Add the embedded manifests with exact priorities**

Use `//go:embed manifests/*.yaml`. Encode these rule IDs and priorities exactly:

```text
codex: 1100 osc_title_blocked; 1050 osc_title_working; 1000 transcript_viewer;
       900 live_strong_blocker; 600 weak_blocker; 500 screen_working_fallback;
       100 osc_title_idle
gemini: 300 apply_or_allow_change; 100 esc_cancel_working
cursor: 320 write_file_approval; 300 approval_prompt; 100 stop_hint_working;
        95 background_task_status_working; 90 spinner_working
claude: 1100 osc_title_working; 1000 transcript_viewer; 980 live_blocked_form;
        980 dynamic_workflow_prompt; 975 btw_overlay_working; 950 live_prompt_box;
        900 model_picker_menu; 850 bash_permission_prompt; 840 generic_permission_prompt;
        300 legacy_no_prompt_blocker; 250 osc_title_idle; 250 osc_progress_idle
```

Copy the literal evidence strings, regex conditions, negative gates, regions, and visible flags from `docs/agent-status-detection.md`; preserve declaration order for equal priorities. Do not add undocumented heuristics. Load each embedded manifest independently. Return one filename-qualified error per invalid kind while keeping valid kinds available in the detector.

- [ ] **Step 4: Format, verify GREEN, and commit**

```bash
gofmt -w internal/codingagent/manifests.go internal/codingagent/manifests_test.go
go test ./internal/codingagent -run TestEmbeddedManifests -count=1
git add internal/codingagent/manifests.go internal/codingagent/manifests internal/codingagent/testdata internal/codingagent/manifests_test.go
git commit -m "feat: 네 가지 코딩 에이전트 감지 규칙 추가"
```

### Task 5: Add the daemon monitor and feed it runtime pane observations

**Files:**
- Create: `internal/codingagent/monitor.go`
- Create: `internal/codingagent/monitor_test.go`
- Modify: `internal/eventbus/types.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/reconcile.go`
- Modify: `internal/runtime/reconcile_test.go`
- Modify: `internal/runtime/subscriptions.go`
- Modify: `internal/runtime/subscriptions_test.go`

**Interfaces:**
- Produces in `runtime`: `type PaneObserver interface { PaneOutput; PaneClosed; PaneError }`
- Adds: `runtime.Options.PaneObserver PaneObserver`
- Produces: `func NewMonitor(MonitorOptions) *Monitor`
- Produces: `func (m *Monitor) Start(Record) error` and `func (m *Monitor) Stop(ID)`
- `Monitor` implements `runtime.PaneObserver`.
- Adds event type `agent_state_changed` and explicit agent-state fields to `eventbus.Event`.

- [ ] **Step 1: Write failing runtime observer tests**

Add a recording observer:

```go
type recordingPaneObserver struct {
	outputs []string
	closed  []registry.PaneID
	errors  []error
}
```

Assert that a parsed pane update calls `PaneOutput(record, renderedText)` after registry output is updated; a pane-closed event calls `PaneClosed(removedRecord)` once; subscribe startup, parse, and stream failures call `PaneError(record, err)` without suppressing existing runtime events; and stale generations never reach the observer.

Also assert that runtime reconciliation calls `PaneClosed(record)` when a managed pane is confirmed missing or exited, after the generation check and subscription teardown. Keep the existing runtime `lost` lifecycle semantics for a missing pane; only the coding-agent observer treats confirmed absence as closure. Stale generations and unrelated live panes must not notify the observer.

- [ ] **Step 2: Write failing monitor state-machine tests**

Inject `Now` and an `AfterFunc` fake scheduler. Test:

- a newly started record remains unknown during the three-second grace;
- the latest cached screen is evaluated when grace expires even without a new output event;
- `working -> idle` without `VisibleIdle` requires three 100ms confirmations and resolves no later than 700ms;
- a new working or blocked screen cancels an idle candidate;
- `SkipStateUpdate` preserves the current state;
- visible idle changes immediately;
- identical detections do not update `StateChangedAt` or publish an event;
- pane error changes state to unknown with a diagnostic reason;
- pane close cancels timers and deletes the record;
- observations for non-`coding-agent` panes or unknown pane IDs are ignored.

- [ ] **Step 3: Verify observer and monitor tests RED**

```bash
go test ./internal/runtime -run 'TestSubscriptionManager.*Observer' -count=1
go test ./internal/codingagent -run TestMonitor -count=1
```

Expected: FAIL because the observer seam and monitor do not exist.

- [ ] **Step 4: Implement observation delivery and monitor scheduling**

Define the runtime-owned interface without importing `codingagent`:

```go
type PaneObserver interface {
	PaneOutput(registry.PaneRecord, string)
	PaneClosed(registry.PaneRecord)
	PaneError(registry.PaneRecord, error)
}
```

Call it only after the existing generation checks. The monitor indexes records through `Store.GetByPane`, caches the latest detection input per agent, and uses generation tokens so canceled grace/idle callbacks cannot mutate a restarted record.

Deliver the same `PaneClosed` observation from the runtime reconciliation path when `ListPanes` confirms the physical pane is missing or exited. This is the safety net for a missed subscription close event; Task 8 supplies the periodic daemon scheduler.

Extend `eventbus.Event` with:

```go
AgentKind      string
PreviousState string
AgentState    string
MatchedRule   string
Reason        string
```

Publish `TypeAgentStateChanged` only after `Store.UpdateState` returns `Changed=true`.

- [ ] **Step 5: Format, verify GREEN, and commit**

```bash
gofmt -w internal/codingagent/monitor.go internal/codingagent/monitor_test.go internal/eventbus/types.go internal/runtime/service.go internal/runtime/reconcile.go internal/runtime/reconcile_test.go internal/runtime/subscriptions.go internal/runtime/subscriptions_test.go
go test ./internal/codingagent ./internal/runtime -count=1
git add internal/codingagent/monitor.go internal/codingagent/monitor_test.go internal/eventbus/types.go internal/runtime/service.go internal/runtime/reconcile.go internal/runtime/reconcile_test.go internal/runtime/subscriptions.go internal/runtime/subscriptions_test.go
git commit -m "feat: 데몬 에이전트 상태 감시 추가"
```

### Task 6: Add current-tab targeting and runtime focus support

**Files:**
- Modify: `internal/zellij/types.go`
- Modify: `internal/zellij/commands.go`
- Modify: `internal/zellij/backend.go`
- Modify: `internal/zellij/backend_test.go`
- Modify: `internal/runtime/types.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_test.go`

**Interfaces:**
- Produces: `zellij.SessionSwitcher` with `SwitchSession(context.Context, SwitchSessionRequest) error`
- Adds: `runtime.Options.SessionSwitcher zellij.SessionSwitcher`
- Adds: `runtime.CreatePaneRequest.SameTabAsZellijPaneID ZellijPaneID`
- Produces: `runtime.FocusService` and `Service.FocusPane(context.Context, FocusPaneRequest)`
- Adds `FocusService` to `runtime.RuntimeService`.

- [ ] **Step 1: Write failing Zellij switch-session command tests**

Assert the backend emits exactly:

```go
want := CommandSpec{
	Name: "zellij",
	Args: []string{
		"--session", "dashboard-session",
		"action", "switch-session", "target-session",
		"--pane-id", "terminal_12",
	},
	Env: []string{
		"ZELLIJ_SESSION_NAME=dashboard-session",
		"ZELLIJ_PANE_ID=terminal_2",
	},
}
```

Reject empty source session, source pane, target session, and target pane before running a command. Add `CommandSpec.Env []string` and verify `ExecRunner` appends these entries to `os.Environ()`.

- [ ] **Step 2: Write failing runtime target and focus tests**

For `CreatePane`, let `ListPanes` return source physical pane `terminal_2` in tab ID 7 and assert the created pane request targets tab 7. Add validation cases where `SameTabAsZellijPaneID` conflicts with `NewTab`, `ZellijTabID`, or `SameTabAsPaneID`, and where the source pane is absent.

For focus, register logical pane `agent-1` pointing at `target-session/terminal_12`, call:

```go
_, err := service.FocusPane(ctx, FocusPaneRequest{
	PaneID:              "agent-1",
	SourceZellijSession: "dashboard-session",
	SourceZellijPaneID:  "terminal_2",
})
```

and assert the session switcher receives the source context and registered target. Lost, closed, or missing panes must return the existing pane-not-found/invalid-target error family.

- [ ] **Step 3: Verify runtime focus tests RED**

```bash
go test ./internal/zellij -run 'Test(SwitchSession|ExecRunnerEnv)' -count=1
go test ./internal/runtime -run 'Test(CreatePaneSameTabAsZellijPane|FocusPane)' -count=1
```

Expected: FAIL because the request fields and focus API do not exist.

- [ ] **Step 4: Implement a separate session-switching capability**

Keep the broad `zellij.Backend` interface unchanged to avoid forcing focus methods into unrelated test backends. Add:

```go
type SessionSwitcher interface {
	SwitchSession(context.Context, SwitchSessionRequest) error
}
```

`CLIBackend` implements both interfaces. `runtime.NewService` uses `Options.SessionSwitcher` when supplied and otherwise type-asserts the configured backend. `FocusPane` fails clearly when no switcher is configured.

Resolve `SameTabAsZellijPaneID` through `Backend.ListPanes` for the requested physical session, then set the discovered `ZellijTabID` before `createBackendPane` runs.

- [ ] **Step 5: Format, verify GREEN, and commit**

```bash
gofmt -w internal/zellij/types.go internal/zellij/commands.go internal/zellij/backend.go internal/zellij/backend_test.go internal/runtime/types.go internal/runtime/service.go internal/runtime/service_test.go
go test ./internal/zellij ./internal/runtime -count=1
git add internal/zellij/types.go internal/zellij/commands.go internal/zellij/backend.go internal/zellij/backend_test.go internal/runtime/types.go internal/runtime/service.go internal/runtime/service_test.go
git commit -m "feat: 현재 탭 실행과 에이전트 포커스 지원"
```

### Task 7: Implement agent start, list, and focus use cases

**Files:**
- Create: `internal/codingagent/service.go`
- Create: `internal/codingagent/service_test.go`

**Interfaces:**
- Produces: `type AgentService interface { StartAgent; ListAgents; FocusAgent }`
- Produces: `type StartAgentRequest`, `StartAgentResponse`, `ListAgentsResponse`, `AgentWithPane`, `FocusAgentRequest`, `FocusAgentResponse`
- Produces: `type LifecycleMonitor interface { Start(Record) error; Stop(ID) }`
- Produces: `func NewService(ServiceOptions) *Service`
- `Service` anonymously embeds `runtime.RuntimeService`, so one assembled value implements runtime and agent APIs.

- [ ] **Step 1: Write failing start-agent tests**

Use a fake runtime and deterministic ID generator. Assert the request:

```go
response, err := service.StartAgent(ctx, StartAgentRequest{
	Kind:                KindGemini,
	CWD:                 "/workspace/project",
	ExtraArgs:           []string{"--model", "gemini-3"},
	SourceZellijSession: "physical-a",
	SourceZellijPaneID:  "terminal_2",
})
```

creates a runtime pane with:

```text
ID=agent-1
AgentID=agent-1
Role=coding-agent
Name=gemini-agent-1
ZellijSession=physical-a
SameTabAsZellijPaneID=terminal_2
Command=[agy --dangerously-skip-permissions --model gemini-3]
CWD=/workspace/project
```

The store must contain an unknown AgentRecord before the fake runtime emits any observation, and `Monitor.Start` must be called once.

Add cases for unknown kind, a kind whose manifest failed to load, blank or inaccessible CWD, missing source context, duplicate generated ID, runtime creation failure, and monitor-start failure. The exact order is store create, monitor start, then runtime pane create. Monitor-start failure deletes the record without creating a pane. Runtime creation failure calls `Monitor.Stop`, deletes the record, and relies on the existing atomic `RuntimeService.CreatePane` cleanup contract for any partially created physical pane.

- [ ] **Step 2: Write failing list and focus tests**

Assert `ListAgents` joins each record with its runtime pane, returns records in creation order, and removes an orphaned record whose pane no longer exists. Assert `FocusAgent` resolves the record's logical pane ID and forwards dashboard source context to `RuntimeService.FocusPane`.

- [ ] **Step 3: Verify agent service tests RED**

```bash
go test ./internal/codingagent -run TestService -count=1
```

Expected: FAIL because service types do not exist.

- [ ] **Step 4: Implement the service and rollback order**

Define the exact service interface:

```go
type AgentWithPane struct {
	Agent Record
	Pane  runtime.Pane
}

type StartAgentResponse struct {
	Agent AgentWithPane
}

type ListAgentsResponse struct {
	Agents []AgentWithPane
}

type FocusAgentResponse struct {
	Agent AgentWithPane
}

type AgentService interface {
	StartAgent(context.Context, StartAgentRequest) (StartAgentResponse, error)
	ListAgents(context.Context) (ListAgentsResponse, error)
	FocusAgent(context.Context, FocusAgentRequest) (FocusAgentResponse, error)
}
```

`ServiceOptions` contains the embedded `runtime.RuntimeService`, `Store`, `LifecycleMonitor`, `Now func() time.Time`, and `NewAgentID func() ID`. Use `filepath.Abs`, `os.Stat`, and directory validation for CWD without requiring a Git repository. Always call `profile.BuildCommand(true, ExtraArgs)`. Generate one ID and use it for both agent and logical pane identity. Register the unknown record and start its monitor before asking runtime to create the pane, so an immediate subscription update cannot race record registration. Do not expose the store directly to CLI or dashboard packages.

- [ ] **Step 5: Format, verify GREEN, and commit**

```bash
gofmt -w internal/codingagent/service.go internal/codingagent/service_test.go
go test ./internal/codingagent -run TestService -count=1
git add internal/codingagent/service.go internal/codingagent/service_test.go
git commit -m "feat: 코딩 에이전트 관리 서비스 추가"
```

### Task 8: Expose agent APIs and assemble them in the daemon

**Files:**
- Create: `internal/transport/handlers_agents.go`
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/types_test.go`
- Modify: `internal/transport/client.go`
- Modify: `internal/transport/client_test.go`
- Modify: `internal/transport/server.go`
- Modify: `internal/transport/server_test.go`
- Modify: `internal/transport/errors.go`
- Modify: `internal/eventbus/types.go`
- Modify: `internal/cli/daemon/daemon.go`
- Modify: `internal/cli/daemon/daemon_test.go`

**Interfaces:**
- Adds transport DTOs: `StartAgentRequest/Response`, `ListAgentsResponse`, `Agent`, `AgentWithPane`, `FocusAgentRequest/Response`
- Adds client methods: `StartAgent`, `ListAgents`, `FocusAgent`
- Adds routes: `POST /v1/agents`, `GET /v1/agents`, `POST /v1/agents/{id}/focus`
- Extends event DTO conversion with the fields introduced in Task 5.
- Starts a daemon-owned runtime reconciliation loop with a two-second default interval.

- [ ] **Step 1: Write failing DTO and client tests**

Round-trip this payload without losing source context or extra arguments:

```json
{
  "kind": "codex",
  "cwd": "/workspace/project",
  "args": ["--model", "gpt-5"],
  "source_session": "physical-a",
  "source_zellij_pane_id": "terminal_2"
}
```

Assert the client uses exact paths and methods:

```text
POST /v1/agents
GET  /v1/agents
POST /v1/agents/agent-1/focus
```

Verify URL escaping for the agent ID and response conversion for all record timestamps and the joined pane.

- [ ] **Step 2: Write failing server route tests**

Extend the fake server service with `codingagent.AgentService`. Test created status `201`, list status `200`, focus status `200`, method rejection, malformed JSON, unknown agent `404`, invalid kind/CWD/source context `400`, and service failure `500`. `GET /v1/agents/agent-1/focus` must not dispatch.

- [ ] **Step 3: Verify transport tests RED**

```bash
go test ./internal/transport -run 'Test(Agent|ServerAgent|ClientAgent|EventAgentState)' -count=1
```

Expected: FAIL because agent DTOs, routes, and client methods do not exist.

- [ ] **Step 4: Implement handlers, error mapping, and event conversion**

Add `codingagent.AgentService` to `ServerRuntime`. Parse the suffix after `/v1/agents/` with the same strict action routing used for panes. Map `codingagent.ErrNotFound` to existing `not_found`, validation errors to `bad_request`, and preserve internal error wrapping in logs/messages without exposing Go type names.

Extend `transport.Event` with JSON fields `agent_kind`, `previous_state`, `agent_state`, `matched_rule`, and `reason`, using `omitempty` for non-agent events.

- [ ] **Step 5: Write failing daemon assembly test**

Replace the narrow `newRuntimeService` assertion with an assembly test that starts the returned service through a fake backend/subscription runner, calls both `InspectRuntime` and `ListAgents`, and proves they share the same runtime registry and event bus. Also inject one invalid manifest and assert that its agent kind fails safely from `StartAgent` while valid kinds and the daemon remain available.

Inject a fake ticker into the daemon reconciliation loop. Assert each tick calls `RuntimeService.Reconcile`, an individual reconciliation error does not stop the daemon or later ticks, context cancellation stops and releases the ticker, and a managed coding-agent record whose physical Zellij pane disappears is removed from `AgentStore` through the Task 5 observer path. Unrelated live records must remain.

- [ ] **Step 6: Assemble store, detector, monitor, runtime, and agent service**

Change daemon construction to return `(transport.ServerRuntime, error)` and build in this order:

```go
bus := eventbus.New()
store := codingagent.NewMemoryStore(time.Now)
detector, manifestErrors := codingagent.LoadEmbeddedDetector()
monitor := codingagent.NewMonitor(codingagent.MonitorOptions{
	Store: store, Detector: detector, DetectorErrors: manifestErrors, EventBus: bus,
})
backend := zellij.NewBackend(zellij.Options{})
runtimeService := agentruntime.NewService(agentruntime.Options{
	Registry: registry.New(), Backend: backend, SessionSwitcher: backend,
	EventBus: bus, SubscriptionRunner: agentruntime.ExecSubscriptionRunner{},
	PaneObserver: monitor,
})
return codingagent.NewService(codingagent.ServiceOptions{
	RuntimeService: runtimeService, Store: store, Monitor: monitor,
}), nil
```

`daemon serve` stays available when one manifest is invalid. `Monitor.Start` returns the stored filename-qualified error only for that kind, and `StartAgent` fails before creating its runtime pane. A failure to create the shared store, event bus, backend, or monitor remains a daemon construction error.

Start a daemon-owned reconciliation goroutine next to `ListenAndServe`. It calls the assembled service's `Reconcile` every two seconds, uses the serve context for cancellation, stops its ticker on exit, and treats individual reconciliation failures as health/diagnostic events rather than daemon-fatal errors. Together with Task 5's reconciliation observer, this removes stale coding-agent records even when the pane-close subscription event was missed. Do not make the dashboard own this cleanup.

- [ ] **Step 7: Format, verify GREEN, and commit**

```bash
gofmt -w internal/transport/handlers_agents.go internal/transport/types.go internal/transport/types_test.go internal/transport/client.go internal/transport/client_test.go internal/transport/server.go internal/transport/server_test.go internal/transport/errors.go internal/eventbus/types.go internal/cli/daemon/daemon.go internal/cli/daemon/daemon_test.go
go test ./internal/transport ./internal/cli/daemon -count=1
git add internal/transport/handlers_agents.go internal/transport/types.go internal/transport/types_test.go internal/transport/client.go internal/transport/client_test.go internal/transport/server.go internal/transport/server_test.go internal/transport/errors.go internal/eventbus/types.go internal/cli/daemon/daemon.go internal/cli/daemon/daemon_test.go
git commit -m "feat: 코딩 에이전트 전송 API 연결"
```

### Task 9: Add the `agent start` CLI

**Files:**
- Create: `internal/cli/agent/agent.go`
- Create: `internal/cli/agent/agent_test.go`
- Modify: `cmd/zellij-agent/main.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Produces: `agentcli.Run(args, stdin, stdout, stderr, ClientFactory, Config) int`
- Adds unified command group: `zellij-agent agent`
- Consumes `transport.Client.StartAgent` from Task 8.

- [ ] **Step 1: Write failing CLI parsing and request tests**

Inject `Getwd` and `Getenv` through `Config`. Test:

```bash
zellij-agent agent start gemini --cwd /workspace -- --model gemini-3
```

and assert the client receives kind `gemini`, absolute CWD, extra arguments, `ZELLIJ_SESSION_NAME`, and `ZELLIJ_PANE_ID`. Assert stdout is:

```text
started agent=agent-1 kind=gemini pane=agent-1
```

Add validation tests for unsupported kind, missing kind, non-directory CWD, missing either Zellij environment variable, non-positive timeout, unexpected start arguments before `--`, and client errors. Help must document the four public kinds, default CWD, permission-bypass defaults, and `--` passthrough.

- [ ] **Step 2: Verify CLI tests RED**

```bash
go test ./internal/cli/agent -run 'TestRun(Start|Help)' -count=1
```

Expected: FAIL because the CLI package does not exist.

- [ ] **Step 3: Implement `agent start`**

Parse the kind as the first token after `start`, split once at literal `--`, and parse `--cwd`, `--socket`, and `--timeout` from the tokens between kind and separator. Default CWD to `Getwd()`. Read source context only through injected `Getenv`; do not call Zellij from the CLI.

Use the existing auto-start transport client supplied by the unified entrypoint. Do not add `list`, `stop`, or prompt-send subcommands.

- [ ] **Step 4: Write failing unified dispatch tests**

Add `agent` to top-level help and verify `zellij-agent agent --help` and `zellij-agent agent start --help` dispatch. Inject a fake agent client through a package-level factory seam in `cmd/zellij-agent/main.go` and assert a start call reaches it.

- [ ] **Step 5: Wire the command group and verify GREEN**

```bash
gofmt -w internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
go test ./internal/cli/agent ./cmd/zellij-agent -count=1
git add internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
git commit -m "feat: 코딩 에이전트 실행 명령 추가"
```

### Task 10: Build the dedicated agent dashboard and focus interaction

**Files:**
- Create: `internal/agentdashboard/model.go`
- Create: `internal/agentdashboard/model_test.go`
- Create: `internal/agentdashboard/view.go`
- Create: `internal/agentdashboard/view_test.go`
- Modify: `internal/cli/agent/agent.go`
- Modify: `internal/cli/agent/agent_test.go`
- Modify: `cmd/zellij-agent/main.go`
- Modify: `cmd/zellij-agent/main_test.go`

**Interfaces:**
- Produces: `agentdashboard.Client` with `ListAgents`, `FocusAgent`, and `StreamEvents`
- Produces: `agentdashboard.Options { RefreshInterval, SourceSession, SourceZellijPaneID }`
- Produces: `agentdashboard.NewModel(context.Context, Client, Options) tea.Model`
- Adds: `zellij-agent agent dashboard`

- [ ] **Step 1: Write failing dashboard model tests**

Use a fake client and Bubble Tea messages to verify:

- `Init` loads agents, opens the event stream, and schedules polling;
- rows remain in creation order and selection survives refresh by agent ID;
- `j/k` and arrows clamp selection;
- `Enter` sends the selected agent ID plus dashboard source session/pane;
- focus success keeps the TUI alive and writes `focused <agent-id>`;
- focus failure keeps the TUI alive and writes `focus failed: ...`;
- `R` refreshes immediately;
- `agent_state_changed` requests a refresh while unrelated events do not;
- a dropped stream or failed list marks `DEGRADED` while retaining the last successful rows;
- `q` and `Ctrl-C` close the stream and quit.

- [ ] **Step 2: Write failing view tests**

At widths 80 and 120, assert the header, flat columns, selected row, and footer. Use records covering all four states. Required labels are:

```text
AGENT DASHBOARD
STATE  AGENT  PROJECT  SINCE
j/k move  Enter focus  R refresh  q quit
```

Render display names from profiles, project from `filepath.Base(Pane.CWD)`, and elapsed state time from `StateChangedAt`. Use distinct symbols/colors for working, blocked, idle, and unknown, but assert plain text after stripping ANSI. Narrow views may truncate cells but must not add a tree or detail pane.

- [ ] **Step 3: Verify dashboard tests RED**

```bash
go test ./internal/agentdashboard -count=1
```

Expected: FAIL because the dashboard package does not exist.

- [ ] **Step 4: Implement the minimal flat-list TUI**

Follow the existing runtime dashboard's command/message pattern but do not import it or share mutable model types. Poll every two seconds by default, refresh on agent-state events, and ensure only one refresh and one focus request can be in flight. Keep the last good list on errors.

- [ ] **Step 5: Add the dashboard subcommand and source-context validation**

Parse:

```bash
zellij-agent agent dashboard [--socket PATH] [--timeout 10s] [--refresh-interval 2s]
```

Require `ZELLIJ_SESSION_NAME` and `ZELLIJ_PANE_ID` before starting Bubble Tea because Enter must address the requesting Zellij client. Reuse an injected program runner in tests, as `internal/cli/dashboard` does. Extend unified dispatch tests to assert the new help path without changing `zellij-agent dashboard`.

- [ ] **Step 6: Format, verify GREEN, and commit**

```bash
gofmt -w internal/agentdashboard/model.go internal/agentdashboard/model_test.go internal/agentdashboard/view.go internal/agentdashboard/view_test.go internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
go test ./internal/agentdashboard ./internal/cli/agent ./cmd/zellij-agent -count=1
git add internal/agentdashboard internal/cli/agent/agent.go internal/cli/agent/agent_test.go cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
git commit -m "feat: 코딩 에이전트 전용 대시보드 추가"
```

### Task 11: Verify the end-to-end flow and install the unified binary

**Files:**
- Create: `internal/codingagent/integration_test.go`
- Modify: `README.md`

**Interfaces:**
- Verifies the public CLI/API/TUI path assembled in Tasks 1 through 10.
- Documents only the approved `start` and `dashboard` commands.

- [ ] **Step 1: Add an opt-in Zellij integration scenario**

Under the existing `AGENTD_ZELLIJ_INTEGRATION=1` gate, create a harmless fake agent executable that prints deterministic working, blocked, and idle screens. Start it through `AgentService` using the current physical pane as source, then assert:

- the new pane's Zellij tab ID equals the source pane's tab ID;
- `ListAgents` returns the joined record and pane;
- state events follow the emitted screens without duplicate identical events;
- `FocusAgent` builds a target for the created session/pane;
- closing the pane removes it from `ListAgents`.

Keep the real `switch-session` call behind `AGENTD_ZELLIJ_E2E=1` because it can disrupt the test runner's active client; command construction remains covered unconditionally in Task 6. Under that E2E flag, add a manual two-client scenario which attaches an observer client to the same session and verifies that focus changes only for the dashboard/source client identified by `source_session` and `source_zellij_pane_id`.

- [ ] **Step 2: Add concise usage documentation**

Add this command family and state meaning to `README.md`:

```bash
zellij-agent agent start codex
zellij-agent agent start claude --cwd /path/to/project
zellij-agent agent start gemini -- --model gemini-3
zellij-agent agent start cursor
zellij-agent agent dashboard
```

Document the four underlying default commands, current-tab requirement, `idle/working/blocked/unknown`, Enter-to-focus behavior, in-memory/no-restart-recovery limitation, and the absence of notifications in v1.

- [ ] **Step 3: Run package and full verification**

```bash
go test ./internal/codingagent ./internal/agentdashboard ./internal/runtime ./internal/zellij ./internal/transport ./internal/cli/agent ./internal/cli/daemon ./cmd/agent-role/codingagent ./cmd/zellij-agent -count=1
go test ./... -count=1
go build -o bin/zellij-agent ./cmd/zellij-agent
```

Expected: all tests PASS and the unified binary builds.

- [ ] **Step 4: Run the opt-in integration test when Zellij context is available**

```bash
AGENTD_ZELLIJ_INTEGRATION=1 go test ./internal/codingagent -run '^TestIntegrationCodingAgent' -v -count=1
```

Expected: PASS inside Zellij. If no Zellij session is available, record the explicit skip and do not claim the integration path ran.

Run the client-switching E2E only when a disposable multi-client session is available:

```bash
AGENTD_ZELLIJ_E2E=1 go test ./internal/codingagent -run '^TestE2ECodingAgentFocusMultipleClients' -v -count=1
```

Expected: the source client focuses the target agent pane and the observer client remains unchanged.

- [ ] **Step 5: Install the binary atomically and smoke-check help**

```bash
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
~/.config/custom-cli/zellij-agent agent --help
~/.config/custom-cli/zellij-agent agent dashboard --help
```

Expected: both help commands exit 0 and list the approved options.

- [ ] **Step 6: Commit integration coverage and documentation**

```bash
git add internal/codingagent/integration_test.go README.md
git commit -m "test: 코딩 에이전트 대시보드 통합 검증 추가"
```

Expected: the worktree is clean after the commit.
