# Runtime Supervisor Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add a local-only zellij-agent dashboard Bubble Tea TUI for browsing the managed runtime, inspecting output and events, sending input, reconciling state, and cleaning up a selected task.

**Architecture:** A transport-only internal/dashboard package owns hierarchy projection and the Bubble Tea model, while internal/cli/dashboard owns flags, signal-aware context, client construction, and program startup. Runtime events trigger coalesced reads through existing transport APIs, with a two-second polling fallback and no direct Zellij access.

**Tech Stack:** Go 1.26 module, Bubble Tea v1.3.10, Lip Gloss v1.1.0, charmbracelet/x/ansi, standard testing, existing Unix-socket transport client.

## Global Constraints

- Every runtime read and mutation goes through transport.Client; dashboard code must not import internal/zellij.
- The hierarchy is exactly session -> task -> tab -> pane, with empty identifiers displayed as ungrouped.
- Default request timeout is 10s, refresh interval is 2s, and event limit is 100.
- raw_output triggers refresh but is not rendered in the semantic event list.
- At most one status/events refresh is in flight; additional triggers coalesce into one follow-up refresh.
- Stream failure leaves polling and actions available in a visible degraded state; this release does not reconnect the stream.
- Cleanup is task-scoped, requires y confirmation, and is disabled for an empty task ID.
- Input is limited to starting or running panes and sends the edited line plus a newline.
- Quitting never cleans up managed panes.
- Use TDD for every behavior: add the focused test, observe the expected failure, then add only enough production code to pass.

---

## File Structure

- internal/dashboard/tree.go: stable hierarchy construction, expansion keys, and visible-row flattening.
- internal/dashboard/tree_test.go: ordering, ungrouped, and expansion tests.
- internal/dashboard/model.go: client boundary, async messages, refresh/event/snapshot flow, and navigation.
- internal/dashboard/actions.go: input, reconcile, cleanup confirmation, and action commands.
- internal/dashboard/view.go: responsive Lip Gloss rendering and ANSI-aware clipping.
- internal/dashboard/model_test.go: refresh, event, selection, action, and degraded-state tests.
- internal/dashboard/view_test.go: empty, lifecycle, selected, and small-window rendering tests.
- internal/cli/dashboard/dashboard.go: flags, context, client factory, and Bubble Tea runner.
- internal/cli/dashboard/dashboard_test.go: defaults, validation, help, runner, and option-forwarding tests.
- cmd/zellij-agent/main.go: top-level command dispatch and client construction.
- cmd/zellij-agent/main_test.go: unified help and dashboard dispatch tests.
- docs/manual-smoke-test.md: real-Zellij dashboard smoke flow.
- docs/next-steps-todolist.md: P1 completion state after verification.

### Task 1: Build the Stable Runtime Hierarchy

**Files:**
- Create: internal/dashboard/tree.go
- Create: internal/dashboard/tree_test.go

**Interfaces:**
- Consumes: transport.Pane SessionID, TaskID, TabID, TabName, ID, Role, and Status.
- Produces: buildTree([]transport.Pane) []*treeNode, defaultExpanded([]*treeNode) map[string]bool, and flattenTree([]*treeNode, map[string]bool) []treeRow.

- [ ] **Step 1: Write failing hierarchy tests**

Create tree_test.go with these tests:

~~~go
func TestBuildTreeSortsHierarchyAndGroupsMissingIDs(t *testing.T) {
	panes := []transport.Pane{
		{ID: "pane-b", SessionID: "session-b", TaskID: "task-b", TabID: "tab-2", TabName: "work"},
		{ID: "pane-a", SessionID: "session-a", TaskID: "task-a", TabID: "tab-1", TabName: "code"},
		{ID: "pane-c"},
	}
	rows := flattenTree(buildTree(panes), defaultExpanded(buildTree(panes)))
	var got []string
	for _, row := range rows {
		got = append(got, row.node.kind+":"+row.node.label)
	}
	want := []string{
		"session:session-a", "task:task-a", "tab:code (tab-1)", "pane:pane-a",
		"session:session-b", "task:task-b", "tab:work (tab-2)", "pane:pane-b",
		"session:ungrouped", "task:ungrouped", "tab:ungrouped", "pane:pane-c",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestFlattenTreeHonorsExpansion(t *testing.T) {
	tree := buildTree([]transport.Pane{{ID: "pane-a", SessionID: "s", TaskID: "t", TabID: "tab"}})
	rows := flattenTree(tree, map[string]bool{})
	if len(rows) != 1 || rows[0].node.kind != "session" {
		t.Fatalf("rows = %#v, want one collapsed session", rows)
	}
}
~~~

- [ ] **Step 2: Run the focused tests and verify RED**

~~~bash
go test ./internal/dashboard -run '^(TestBuildTree|TestFlattenTree)'
~~~

Expected: compilation fails because the package and hierarchy functions do not exist.

- [ ] **Step 3: Implement hierarchy projection**

Use these exact types:

~~~go
type treeNode struct {
	kind     string
	key      string
	label    string
	pane     *transport.Pane
	children []*treeNode
}

type treeRow struct {
	node  *treeNode
	depth int
}
~~~

buildTree groups with normalized values, uses fully qualified keys such as session\x00<id>\x00task\x00<id>, labels named tabs as name (id), sorts groups by label then key, sorts panes by logical ID, and copies each pane before storing its pointer. defaultExpanded walks every non-pane node. flattenTree emits preorder rows and descends only through expanded groups.

- [ ] **Step 4: Format and verify GREEN**

~~~bash
gofmt -w internal/dashboard/tree.go internal/dashboard/tree_test.go
go test ./internal/dashboard -run '^(TestBuildTree|TestFlattenTree)'
~~~

Expected: focused tests report ok.

- [ ] **Step 5: Commit**

~~~bash
git add internal/dashboard/tree.go internal/dashboard/tree_test.go
git commit -m "feat: add dashboard runtime hierarchy"
~~~

### Task 2: Add Refresh, Event, Selection, Snapshot, and Rendering

**Files:**
- Create: internal/dashboard/model.go
- Create: internal/dashboard/view.go
- Create: internal/dashboard/model_test.go
- Create: internal/dashboard/view_test.go

**Interfaces:**
- Consumes: Task 1 hierarchy functions and transport read/stream methods.
- Produces: Client, Options, Model, and NewModel(context.Context, Client, Options) Model; Model implements tea.Model.

- [ ] **Step 1: Write failing model and view tests**

Define a fake client and add these core assertions:

~~~go
func TestModelCoalescesRefreshTriggers(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{RefreshInterval: time.Second, EventLimit: 5})
	m.refreshing = true
	next, _ := m.Update(refreshTickMsg{})
	next, _ = next.(Model).Update(streamEventMsg{event: transport.Event{Type: "raw_output"}})
	got := next.(Model)
	if !got.refreshDirty || !got.refreshing {
		t.Fatalf("model = %#v, want one in-flight refresh marked dirty", got)
	}
}

func TestModelStreamCloseKeepsPollingDegraded(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{RefreshInterval: time.Second, EventLimit: 5})
	next, cmd := m.Update(streamClosedMsg{err: errors.New("eof")})
	got := next.(Model)
	if got.connection != "degraded" || !strings.Contains(got.statusText, "eof") {
		t.Fatalf("model = %#v, want degraded eof", got)
	}
	if cmd != nil {
		t.Fatal("stream close must not quit")
	}
}

func TestViewHandlesEmptyAndTinyWindows(t *testing.T) {
	m := NewModel(context.Background(), &fakeClient{}, Options{})
	for _, size := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 20, Height: 4}, {Width: 100, Height: 30}} {
		next, _ := m.Update(size)
		m = next.(Model)
		view := m.View()
		if !strings.Contains(view, "RUNTIME DASHBOARD") || !strings.Contains(view, "no managed panes") {
			t.Fatalf("size=%#v view=%q", size, view)
		}
	}
}
~~~

Also add TestModelPreservesSelectionAcrossRefresh, TestModelFallsBackWhenSelectedPaneDisappears, TestModelSelectionRequestsSnapshot, and TestViewShowsLifecycleBadgesAndFiltersRawOutput. The fake records calls and returns configurable InspectRuntimeResponse, RecentEventsResponse, SnapshotOutputResponse, and EventStream values.

- [ ] **Step 2: Run tests and verify RED**

~~~bash
go test ./internal/dashboard -run '^(TestModel|TestView)'
~~~

Expected: compilation fails because Client, Options, Model, NewModel, and message types do not exist.

- [ ] **Step 3: Implement the model boundary and state**

Use these exact declarations:

~~~go
type Client interface {
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
	Reconcile(context.Context) (transport.ReconcileResponse, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type Options struct {
	RefreshInterval time.Duration
	EventLimit      int
}

type refreshResultMsg struct {
	status transport.InspectRuntimeResponse
	events transport.RecentEventsResponse
	err    error
}
type snapshotResultMsg struct { paneID, output string; err error }
type streamReadyMsg struct { stream *transport.EventStream; err error }
type streamEventMsg struct { event transport.Event }
type streamClosedMsg struct { err error }
type refreshTickMsg struct{}

type Model struct {
	ctx context.Context
	client Client
	opts Options
	width, height int
	tree []*treeNode
	rows []treeRow
	expanded map[string]bool
	selected int
	selectedKey string
	events []transport.Event
	snapshots map[string]string
	refreshing, refreshDirty, snapshotting bool
	stream *transport.EventStream
	connection, statusText string
	mode string
	input []rune
	confirmTask string
	actionInFlight bool
}
~~~

NewModel normalizes missing options to 2s and 100, initializes maps, and starts with connection=connecting. Init returns tea.Batch of refreshCmd, connectStreamCmd, and tickCmd. All commands use the model context.

Use this coalescing helper:

~~~go
func (m *Model) requestRefresh() tea.Cmd {
	if m.refreshing {
		m.refreshDirty = true
		return nil
	}
	m.refreshing = true
	return m.refreshCmd()
}
~~~

On successful refresh, rebuild the tree, retain expansion for surviving keys, restore selectedKey, filter raw_output, and request a snapshot when the selected pane changes or lacks cached output. On error, preserve prior data. After completion, clear refreshing and launch exactly one follow-up when refreshDirty is set. Every stream event reinstalls the one-event wait command and requests refresh. Stream completion sets degraded without tea.Quit. Every timer tick reschedules the timer and requests refresh.

- [ ] **Step 4: Implement responsive rendering**

view.go renders title, connection, tree, selected output, semantic events, status, and key hints. Below width 80 it stacks sections; at width 80 or above it uses a 35% tree column and a vertically split right column. Use ansi.Truncate for clipping, never slice display strings by byte width, and safely handle zero dimensions. Pane lines use paneID role=<role> [<status>], unknown statuses remain textual, and selected lines use reverse video.

- [ ] **Step 5: Format and verify GREEN**

~~~bash
gofmt -w internal/dashboard/model.go internal/dashboard/view.go internal/dashboard/model_test.go internal/dashboard/view_test.go
go test ./internal/dashboard
~~~

Expected: all dashboard tests report ok.

- [ ] **Step 6: Commit**

~~~bash
git add internal/dashboard
git commit -m "feat: add live dashboard model and view"
~~~

### Task 3: Add Input, Reconcile, and Confirmed Task Cleanup

**Files:**
- Create: internal/dashboard/actions.go
- Modify: internal/dashboard/model.go
- Modify: internal/dashboard/model_test.go

**Interfaces:**
- Consumes: Task 2 Model, selected pane lookup, and Client mutation methods.
- Produces: input mode, reconcile, cleanup confirmation, and action results.

- [ ] **Step 1: Write failing action tests**

Add the following behavioral tests plus focused tests for backspace/Esc, empty input, inactive panes, n cancellation, missing task IDs, duplicate action suppression, and errors:

~~~go
func TestModelInputSendsLineWithNewline(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("echo ok")})
	next, cmd := next.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	next, _ = next.(Model).Update(msg)
	if client.inputPane != "coder" || client.input.Text != "echo ok\n" {
		t.Fatalf("input pane=%q req=%#v", client.inputPane, client.input)
	}
}

func TestModelCleanupRequiresConfirmationAndScopesTask(t *testing.T) {
	client, m := modelWithSelectedPane(t, transport.Pane{ID: "coder", TaskID: "task-1", Status: "running"})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || next.(Model).mode != "confirm-cleanup" {
		t.Fatal("x must only enter confirmation")
	}
	next, cmd = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	msg := cmd()
	next, _ = next.(Model).Update(msg)
	if client.cleanup.TaskID != "task-1" || len(client.cleanup.PaneIDs) != 0 || client.cleanup.Role != "" {
		t.Fatalf("cleanup = %#v", client.cleanup)
	}
}
~~~

- [ ] **Step 2: Run action tests and verify RED**

~~~bash
go test ./internal/dashboard -run 'Input|Cleanup|Reconcile|Action'
~~~

Expected: failures because action modes and commands are absent.

- [ ] **Step 3: Implement action commands**

actions.go defines:

~~~go
type actionResultMsg struct {
	kind, summary string
	err error
}

func (m Model) sendInputCmd(paneID, text string) tea.Cmd {
	return func() tea.Msg {
		err := m.client.SendInput(m.ctx, paneID, transport.SendInputRequest{Text: text + "\n"})
		return actionResultMsg{kind: "input", summary: "input sent to " + paneID, err: err}
	}
}

func (m Model) reconcileCmd() tea.Cmd {
	return func() tea.Msg {
		r, err := m.client.Reconcile(m.ctx)
		return actionResultMsg{kind: "reconcile", summary: fmt.Sprintf("reconciled active=%d lost=%d", len(r.Active), len(r.Lost)), err: err}
	}
}

func (m Model) cleanupCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		r, err := m.client.Cleanup(m.ctx, transport.CleanupRequest{TaskID: taskID})
		return actionResultMsg{kind: "cleanup", summary: fmt.Sprintf("cleanup closed=%d failed=%d skipped=%d", len(r.Closed), len(r.Failed), len(r.Skipped)), err: err}
	}
}
~~~

Update handles modes before normal keys. Input mode appends runes, handles backspace, cancels on Esc, and sends only non-empty input on Enter. Confirmation submits only y and cancels n/Esc. Normal i requires starting/running; x requires a non-empty task; r requires no in-flight action. actionResultMsg clears the flag, renders either <kind> failed: <error> or the summary, resets mode, and refreshes after success.

- [ ] **Step 4: Format and verify GREEN**

~~~bash
gofmt -w internal/dashboard/actions.go internal/dashboard/model.go internal/dashboard/model_test.go
go test ./internal/dashboard
~~~

Expected: all dashboard tests report ok.

- [ ] **Step 5: Commit**

~~~bash
git add internal/dashboard
git commit -m "feat: add dashboard runtime actions"
~~~

### Task 4: Add the Dashboard CLI and Unified Dispatch

**Files:**
- Create: internal/cli/dashboard/dashboard.go
- Create: internal/cli/dashboard/dashboard_test.go
- Modify: cmd/zellij-agent/main.go
- Modify: cmd/zellij-agent/main_test.go

**Interfaces:**
- Consumes: dashboard.NewModel, transport.Client, newAutoStartClient, stdin/stdout/stderr.
- Produces: dashboardcli.Run and top-level zellij-agent dashboard.

- [ ] **Step 1: Write failing CLI tests**

Use these injection boundaries:

~~~go
type ClientFactory func(socketPath string, timeout time.Duration) dashboard.Client
type ModelFactory func(context.Context, dashboard.Client, dashboard.Options) tea.Model
type ProgramRunner func(context.Context, tea.Model, io.Reader, io.Writer) error
type Config struct {
	NewModel   ModelFactory
	RunProgram ProgramRunner
}
~~~

Use a recording ModelFactory to assert that custom socket, timeout, refresh interval, and event limit reach the client factory and dashboard.Options; help returns 0; positional, unknown, and non-positive values return 2; a runner error returns 1 containing dashboard failed. Extend TestRunHelp to require dashboard and add TestRunDispatchesDashboardHelp expecting Usage: zellij-agent dashboard.

- [ ] **Step 2: Run tests and verify RED**

~~~bash
go test ./internal/cli/dashboard ./cmd/zellij-agent -run 'Dashboard|RunHelp'
~~~

Expected: the package is absent and unified assertions fail.

- [ ] **Step 3: Implement the CLI package**

Run parses --socket, --timeout, --refresh-interval, and --event-limit, rejects positional/non-positive values, creates signal.NotifyContext for interrupt/SIGTERM, builds the model with Config.NewModel, and invokes the configured runner. When Config.NewModel is nil, adapt dashboard.NewModel as the default. The default program runner is:

~~~go
func runProgram(ctx context.Context, model tea.Model, stdin io.Reader, stdout io.Writer) error {
	_, err := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithAltScreen(),
	).Run()
	return err
}
~~~

Help lists all defaults and keys. Return 0 for help/success, 2 for usage errors, and 1 for runner failure.

- [ ] **Step 4: Wire unified dispatch**

Import internal/cli/dashboard as dashboardcli, add the dashboard switch case, add newDashboardClient returning newAutoStartClient, and add dashboard to printUsage. Pass stdin/stdout/stderr and preserve auto-start behavior.

- [ ] **Step 5: Format and verify GREEN**

~~~bash
gofmt -w internal/cli/dashboard/dashboard.go internal/cli/dashboard/dashboard_test.go cmd/zellij-agent/main.go cmd/zellij-agent/main_test.go
go test ./internal/cli/dashboard ./cmd/zellij-agent
~~~

Expected: both packages report ok.

- [ ] **Step 6: Commit**

~~~bash
git add internal/cli/dashboard cmd/zellij-agent
git commit -m "feat: add dashboard command"
~~~

### Task 5: Document, Close P1, and Verify the Binary

**Files:**
- Modify: docs/manual-smoke-test.md
- Modify: docs/next-steps-todolist.md

**Interfaces:**
- Consumes: completed dashboard command and a real Zellij session.
- Produces: reproducible smoke instructions and accurate P1 roadmap state.

- [ ] **Step 1: Add the dashboard smoke flow**

Document building the unified binary and immediately copying it to ~/.config/custom-cli. In a real managed workspace run zellij-agent dashboard --socket /tmp/agentd.sock, navigate to coder, refresh with s, send echo dashboard-smoke-ok through i, reconcile with r, and confirm task cleanup with x then y. State that unmanaged panes remain open and stream failure displays degraded while polling continues.

- [ ] **Step 2: Run focused and full verification**

~~~bash
go test ./internal/dashboard ./internal/cli/dashboard ./cmd/zellij-agent
go test ./...
git diff --check
~~~

Expected: all packages report ok or no test files, and diff check is silent.

- [ ] **Step 3: Build and register the binary**

Run exactly in this order:

~~~bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
~~~

Expected: both commands exit successfully.

- [ ] **Step 4: Mark P1 complete after verification**

Check all P1 initial-scope items and add the focused/full test and build/copy commands as verification. Leave P2, P3, and later items unchanged.

- [ ] **Step 5: Commit docs**

~~~bash
git add docs/manual-smoke-test.md docs/next-steps-todolist.md
git commit -m "docs: add dashboard smoke flow"
~~~

- [ ] **Step 6: Inspect final state**

~~~bash
git status --short
git log -6 --oneline
~~~

Expected: clean worktree with hierarchy, model, actions, command, and docs commits visible.
