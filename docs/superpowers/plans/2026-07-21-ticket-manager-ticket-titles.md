# Ticket Manager Ticket Titles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each ticket's human-readable title in every ticket-specific manager log and in the corresponding worker's Zellij pane name.

**Architecture:** Keep logical pane IDs and runtime behavior unchanged. Add one pure helper for normalized, length-limited display names and one manager logging helper that applies the shared `ticket=<id> title=%q` prefix to every existing ticket-specific log event.

**Tech Stack:** Go standard library (`fmt`, `strings`, `testing`), existing ticket-worker manager fakes, Go toolchain.

## Global Constraints

- Preserve logical worker pane IDs in the form `ticket-coding-<manager-id>-<ticket-id>`.
- Set only `transport.CreatePaneRequest.Name` to `[<ticket-id>] <normalized-title>`.
- Collapse all title whitespace runs to one ASCII space before constructing a pane name.
- Limit the normalized title portion to 32 Unicode code points; for longer titles retain 31 code points and append `…`.
- Add `title=%q` immediately after `ticket=<id>` in every existing ticket-specific manager log.
- Do not add new log events, database queries, store interface methods, runtime calls, or external dependencies.
- Leave claim and event-stream logs unchanged because they have no associated ticket.
- Follow TDD and observe each focused test fail for the expected missing behavior before production edits.

---

### Task 1: Title-Based Worker Pane Display Names

**Files:**
- Modify: `internal/ticketworker/manager_test.go:13-50`
- Modify: `internal/ticketworker/manager.go:300-345`

**Interfaces:**
- Consumes: `Ticket{ID int64, Title string}`.
- Produces: `workerPaneName(ticket Ticket) string`, used as `transport.CreatePaneRequest.Name`.
- Preserves: `managerSlot.paneID` and `transport.CreatePaneRequest.ID`.

- [ ] **Step 1: Write failing display-name tests**

Add this test after `TestNewManagerGeneratesDistinctInstanceIDs`:

```go
func TestWorkerPaneNameNormalizesAndLimitsTitle(t *testing.T) {
	tests := []struct {
		name   string
		ticket Ticket
		want   string
	}{
		{name: "plain", ticket: Ticket{ID: 7, Title: "검색 기능 구현"}, want: "[7] 검색 기능 구현"},
		{name: "whitespace", ticket: Ticket{ID: 8, Title: "  검색\n\t기능 \r 구현  "}, want: "[8] 검색 기능 구현"},
		{name: "long unicode", ticket: Ticket{ID: 9, Title: strings.Repeat("한", 32) + "끝"}, want: "[9] " + strings.Repeat("한", 31) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerPaneName(tt.ticket); got != tt.want {
				t.Fatalf("workerPaneName(%#v) = %q, want %q", tt.ticket, got, tt.want)
			}
		})
	}
}
```

In `TestManagerWaitsForAnchorThenFillsConfiguredCapacity`, define `wantNames`
before the existing create-request loop and extend its request assertion:

```go
wantNames := []string{"[1] Ticket", "[2] Ticket"}
for i, req := range client.created() {
	wantID := int64(i + 1)
	if req.ID != "ticket-coding-run-a-"+string(rune('0'+wantID)) || req.Name != wantNames[i] || req.Role != "coding-agent" || req.TaskID != "tickets" || req.SameTabAsPaneID != "ticket-manager" || req.ZellijSession != "physical-a" {
		t.Fatalf("create[%d] = %#v", i, req)
	}
	wantCommand := []string{"zellij-agent", "role", "coding-agent", "--yolo", "/repo"}
	if len(req.Command) != len(wantCommand) {
		t.Fatalf("command = %#v", req.Command)
	}
	for j := range wantCommand {
		if req.Command[j] != wantCommand[j] {
			t.Fatalf("command = %#v", req.Command)
		}
	}
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
go test ./internal/ticketworker -run '^(TestWorkerPaneNameNormalizesAndLimitsTitle|TestManagerWaitsForAnchorThenFillsConfiguredCapacity)$' -count=1
```

Expected: `FAIL`; `workerPaneName` is undefined and the current create request still uses the logical pane ID as `Name`.

- [ ] **Step 3: Implement the pane-name helper and use it**

Add the constant and helper near `startSlot`:

```go
const workerPaneTitleLimit = 32

func workerPaneName(ticket Ticket) string {
	title := strings.Join(strings.Fields(ticket.Title), " ")
	runes := []rune(title)
	if len(runes) > workerPaneTitleLimit {
		title = string(runes[:workerPaneTitleLimit-1]) + "…"
	}
	return fmt.Sprintf("[%d] %s", ticket.ID, title)
}
```

Change only the display name in the create request:

```go
req := transport.CreatePaneRequest{
	ID: slot.paneID, TaskID: m.taskID, ZellijSession: m.zellijSession,
	Role: "coding-agent", Name: workerPaneName(ticket), SameTabAsPaneID: m.anchorPaneID,
	Command: []string{m.roleBin, "role", "coding-agent", "--yolo", m.root}, CWD: m.root,
}
```

- [ ] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go
go test ./internal/ticketworker -run '^(TestWorkerPaneNameNormalizesAndLimitsTitle|TestManagerWaitsForAnchorThenFillsConfiguredCapacity)$' -count=1
go test ./internal/ticketworker -count=1
```

Expected: both test commands report `ok`.

- [ ] **Step 5: Commit the pane-name change**

Run:

```bash
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git commit -m "feat: name ticket worker panes from titles"
```

Expected: commit succeeds with only the pane-name helper, create request change, and focused tests.

---

### Task 2: Ticket Titles in Manager Logs

**Files:**
- Modify: `internal/ticketworker/manager_test.go`
- Modify: `internal/ticketworker/manager.go:313-615`

**Interfaces:**
- Consumes: an event label, a complete `Ticket`, an optional format suffix, and suffix arguments.
- Produces: `func (m *Manager) logTicketf(event string, ticket Ticket, format string, args ...any)`.
- Preserves: the existing `func (m *Manager) logf(format string, args ...any)` for non-ticket logs.

- [ ] **Step 1: Write failing quoted-title and lifecycle log tests**

Add this focused test near the manager constructor tests:

```go
func TestManagerLogTicketfIncludesQuotedTitle(t *testing.T) {
	var output strings.Builder
	manager := &Manager{log: &output}
	ticket := Ticket{ID: 7, Title: "첫째 \"제목\"\n둘째"}

	manager.logTicketf("started", ticket, "pane=%s", "pane-7")

	want := "started ticket=7 title=\"첫째 \\\"제목\\\"\\n둘째\" pane=pane-7\n"
	if got := output.String(); got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
```

In `TestManagerIgnoresPromptEchoAndCompletesExactMarker`, capture the manager
log immediately after construction:

```go
manager := newTestManager(t, store, client, 1)
var logs strings.Builder
manager.log = &logs
```

After the exact completion marker closes the pane, assert the successful
lifecycle events:

```go
for _, want := range []string{
	`started ticket=42 title="Ticket" pane=ticket-coding-run-a-42`,
	`closed ticket=42 title="Ticket" pane=ticket-coding-run-a-42`,
} {
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("manager log %q does not contain %q", logs.String(), want)
	}
}
```

In `TestManagerSafeCreateFailureRequeuesClaimedTicket`, capture the log after
manager construction:

```go
var logs strings.Builder
manager.log = &logs
```

After the existing `waitFor` observes the requeue, add:

```go
if want := `create ticket=9 title="Ticket" pane=ticket-coding-run-a-9 failed:`; !strings.Contains(logs.String(), want) {
	t.Fatalf("manager log %q does not contain %q", logs.String(), want)
}
```

In `TestManagerRetriesDoneBeforeClosing`, capture the log after manager
construction:

```go
var logs strings.Builder
manager.log = &logs
```

After the existing `waitFor` observes the first transition attempt, add:

```go
if want := `complete ticket=7 title="Ticket" failed: database busy`; !strings.Contains(logs.String(), want) {
	t.Fatalf("manager log %q does not contain %q", logs.String(), want)
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test ./internal/ticketworker -run '^(TestManagerLogTicketfIncludesQuotedTitle|TestManagerIgnoresPromptEchoAndCompletesExactMarker|TestManagerSafeCreateFailureRequeuesClaimedTicket|TestManagerRetriesDoneBeforeClosing)$' -count=1
```

Expected: `FAIL`; `Manager.logTicketf` is undefined and the existing lifecycle
logs lack `title="Ticket"`.

- [ ] **Step 3: Add the ticket logging helper**

Add this helper immediately before `logf`:

```go
func (m *Manager) logTicketf(event string, ticket Ticket, format string, args ...any) {
	values := []any{event, ticket.ID, ticket.Title}
	values = append(values, args...)
	m.logf("%s ticket=%d title=%q "+format, values...)
}
```

Run the helper-only test:

```bash
gofmt -w internal/ticketworker/manager_test.go internal/ticketworker/manager.go
go test ./internal/ticketworker -run '^TestManagerLogTicketfIncludesQuotedTitle$' -count=1
```

Expected: `PASS`. The lifecycle tests remain red until their call sites are
migrated in the next step.

- [ ] **Step 4: Route all existing ticket-specific logs through the helper**

Replace the eight existing ticket-specific log calls with:

```go
m.logTicketf("render", ticket, "failed: %v", err)
m.logTicketf("create", ticket, "pane=%s failed: %v", slot.paneID, err)
m.logTicketf("started", ticket, "pane=%s", slot.paneID)
m.logTicketf("inspect worker", slot.ticket, "pane=%s failed: %v", slot.paneID, err)
m.logTicketf("complete", slot.ticket, "failed: %v", err)
m.logTicketf("close", slot.ticket, "pane=%s failed: %v", slot.paneID, err)
m.logTicketf("closed", slot.ticket, "pane=%s", slot.paneID)
m.logTicketf("snapshot recovery", slot.ticket, "pane=%s failed: %v", slot.paneID, err)
```

Keep these non-ticket calls on `logf`:

```go
m.logf("event stream reconnect failed: %v", err)
m.logf("event stream lost: %v", err)
m.logf("claim ticket failed: %v", err)
```

- [ ] **Step 5: Format, verify GREEN, and check complete call-site coverage**

Run:

```bash
gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go
go test ./internal/ticketworker -run '^(TestManagerLogTicketfIncludesQuotedTitle|TestManagerIgnoresPromptEchoAndCompletesExactMarker|TestManagerSafeCreateFailureRequeuesClaimedTicket|TestManagerRetriesDoneBeforeClosing)$' -count=1
go test ./internal/ticketworker -count=1
if rg -n 'm\.logf\("(render|create|started|inspect worker|complete|close|closed|snapshot recovery)' internal/ticketworker/manager.go; then exit 1; fi
```

Expected: both test commands report `ok`, and the source scan produces no matches because all eight ticket-specific events use `logTicketf`.

- [ ] **Step 6: Run repository verification**

Run:

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 7: Build and atomically register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
cmp -s bin/zellij-agent ~/.config/custom-cli/zellij-agent
```

Expected: every command exits with status 0 and the registered binary matches the build artifact.

- [ ] **Step 8: Review and commit the logging change**

Run:

```bash
git diff --check
git diff -- internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go
git commit -m "feat: include ticket titles in manager logs"
```

Expected: the diff contains the logging helper, eight migrated call sites, and the quoted-title test; the commit succeeds.
