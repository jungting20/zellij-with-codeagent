package ticketworkercli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

type harness struct {
	root   string
	stdout bytes.Buffer
	stderr bytes.Buffer
	deps   Dependencies
}

var errForcedWrite = errors.New("forced write failure")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errForcedWrite }

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		root: root,
		deps: Dependencies{
			StartDirectory: root,
			Now:            func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
		},
	}
	if got := h.run(t, "init"); got != ExitOK {
		t.Fatalf("init exit = %d, stderr = %s", got, h.stderr.String())
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) int {
	t.Helper()
	h.stdout.Reset()
	h.stderr.Reset()
	return Run(context.Background(), args, &h.stdout, &h.stderr, h.deps)
}

func (h *harness) artifacts(t *testing.T, name string) (string, string) {
	t.Helper()
	spec := filepath.Join(h.root, "docs", "superpowers", "specs", name+"-design.md")
	plan := filepath.Join(h.root, "docs", "superpowers", "plans", name+".md")
	for _, path := range []string{spec, plan} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# Approved\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return spec, plan
}

func (h *harness) addJSON(t *testing.T, title, summary, spec, plan string) ticketworker.Ticket {
	t.Helper()
	if got := h.run(t, "add", "--title", title, "--summary", summary, "--spec", spec, "--plan", plan, "--json"); got != ExitOK {
		t.Fatalf("add exit = %d, stderr = %s", got, h.stderr.String())
	}
	return decodeTicket(t, h.stdout.Bytes())
}

func decodeTicket(t *testing.T, data []byte) ticketworker.Ticket {
	t.Helper()
	var got ticketworker.Ticket
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode ticket JSON %q: %v", data, err)
	}
	return got
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration
	requestID  string
	payload    transport.ExecutionPlanPayload
	err        error
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.requestID = requestID
	c.payload = payload
	if c.err != nil {
		return transport.ExecutionPlanResponse{}, c.err
	}
	pane := payload.Tabs[0].Panes[0]
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name: payload.Tabs[0].Name,
				Panes: []transport.Pane{
					{ID: pane.ID, Role: pane.Role, Status: "starting", ZellijPaneID: "terminal_1"},
				},
			},
		},
	}, nil
}

func configureStartClient(h *harness, client *fakeAgentClient) {
	h.deps.Executable = []string{"/opt/zellij-agent"}
	h.deps.NewClient = func(socketPath string, timeout time.Duration) AgentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

func TestStartSubmitsTicketManagerPlan(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	client := &fakeAgentClient{}
	configureStartClient(h, client)

	if got := h.run(t, "start", "--socket", "/tmp/tickets.sock", "--timeout", "2s"); got != ExitOK {
		t.Fatalf("start exit = %d, stderr = %s", got, h.stderr.String())
	}
	if client.socketPath != "/tmp/tickets.sock" || client.timeout != 2*time.Second {
		t.Fatalf("client options = %q/%s", client.socketPath, client.timeout)
	}
	if client.requestID != ticketworker.StartRequestID(client.payload.Session) || client.payload.ZellijSession != "physical-a" {
		t.Fatalf("submission request=%q payload=%#v", client.requestID, client.payload)
	}
	pane := client.payload.Tabs[0].Panes[0]
	if pane.Role != "ticket-manager" || pane.CWD != h.root || !strings.HasPrefix(pane.ID, "ticket-manager-") {
		t.Fatalf("manager pane = %#v", pane)
	}
	if !strings.Contains(h.stdout.String(), "role=ticket-manager status=starting") {
		t.Fatalf("stdout = %q", h.stdout.String())
	}
}

func TestStartExplicitZellijSessionOverridesEnvironment(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "environment-session")
	client := &fakeAgentClient{}
	configureStartClient(h, client)

	if got := h.run(t, "start", "--zellij-session", "explicit-session"); got != ExitOK {
		t.Fatalf("start exit = %d, stderr = %s", got, h.stderr.String())
	}
	if client.payload.ZellijSession != "explicit-session" {
		t.Fatalf("ZellijSession = %q", client.payload.ZellijSession)
	}
}

func TestStartRejectsFormerTicketID(t *testing.T) {
	h := newHarness(t)
	if got := h.run(t, "start", "1"); got != ExitUsage {
		t.Fatalf("start exit = %d, stderr = %s", got, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "start does not accept positional arguments") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}

func TestStartRequiresPositiveTimeout(t *testing.T) {
	h := newHarness(t)
	if got := h.run(t, "start", "--timeout", "0s"); got != ExitUsage {
		t.Fatalf("start exit = %d, stderr = %s", got, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "start --timeout must be positive") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}

func TestStartRequiresZellijSession(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	client := &fakeAgentClient{}
	configureStartClient(h, client)

	if got := h.run(t, "start"); got == ExitOK {
		t.Fatal("start succeeded without a Zellij session")
	}
	if client.requestID != "" || !strings.Contains(h.stderr.String(), "zellij session is required") {
		t.Fatalf("request=%q stderr=%q", client.requestID, h.stderr.String())
	}
}

func TestStartRejectsInvalidConfigWithoutSubmission(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	if err := os.WriteFile(ticketworker.ConfigPath(h.root), []byte("version: 1\nmax_workers: 0\npoll_interval: 1s\nprompt_template: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &fakeAgentClient{}
	configureStartClient(h, client)

	if got := h.run(t, "start"); got == ExitOK {
		t.Fatal("start succeeded with invalid config")
	}
	if client.requestID != "" || !strings.Contains(h.stderr.String(), "load ticket-worker config") {
		t.Fatalf("request=%q stderr=%q", client.requestID, h.stderr.String())
	}
}

func TestStartRejectsMissingDatabaseWithoutSubmission(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	if err := os.Remove(ticketworker.DatabasePath(h.root)); err != nil {
		t.Fatal(err)
	}
	client := &fakeAgentClient{}
	configureStartClient(h, client)

	if got := h.run(t, "start"); got != ExitValidation {
		t.Fatalf("start exit = %d, stderr = %s", got, h.stderr.String())
	}
	if client.requestID != "" {
		t.Fatalf("unexpected request = %q", client.requestID)
	}
}

func TestStartReportsClientFailure(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	client := &fakeAgentClient{err: errors.New("daemon unavailable")}
	configureStartClient(h, client)

	if got := h.run(t, "start"); got == ExitOK {
		t.Fatal("start succeeded after client failure")
	}
	if !strings.Contains(h.stderr.String(), "ticket-worker submit failed") || !strings.Contains(h.stderr.String(), "daemon unavailable") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}

func TestStartReportsWriterFailure(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ZELLIJ_SESSION_NAME", "physical-a")
	client := &fakeAgentClient{}
	configureStartClient(h, client)
	var stderr bytes.Buffer

	if got := Run(context.Background(), []string{"start"}, failingWriter{}, &stderr, h.deps); got == ExitOK {
		t.Fatal("start succeeded after output failure")
	}
	if !strings.Contains(stderr.String(), "write output: forced write failure") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAddJSONRegistersReadyTicketFromNestedDirectory(t *testing.T) {
	h := newHarness(t)
	spec, plan := h.artifacts(t, "search")
	nested := filepath.Join(h.root, "tools", "ra-ticket", "cmd")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	h.deps.StartDirectory = nested

	got := h.addJSON(t, "Search", "Search story bible", spec, plan)
	if got.ID != 1 || got.Status != ticketworker.StatusReady {
		t.Fatalf("add ticket = %#v", got)
	}
	if got.SpecPath != "docs/superpowers/specs/search-design.md" || got.PlanPath != "docs/superpowers/plans/search.md" {
		t.Fatalf("add paths = %q, %q", got.SpecPath, got.PlanPath)
	}
}

func TestTicketJSONUsesExactSnakeCaseKeys(t *testing.T) {
	h := newHarness(t)
	spec, plan := h.artifacts(t, "json-keys")
	if got := h.run(t, "add", "--title", "JSON keys", "--summary", "Assert the JSON contract", "--spec", spec, "--plan", plan, "--json"); got != ExitOK {
		t.Fatalf("add exit = %d, stderr = %s", got, h.stderr.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(h.stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw ticket JSON %q: %v", h.stdout.Bytes(), err)
	}
	wantKeys := []string{
		"id", "title", "summary", "spec_path", "plan_path", "status",
		"created_at", "updated_at", "started_at", "completed_at", "cancelled_at",
	}
	if len(raw) != len(wantKeys) {
		t.Fatalf("JSON keys = %#v, want exactly %#v", raw, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("JSON missing key %q: %#v", key, raw)
		}
	}
}

func TestNextJSONClaimsReadyTicket(t *testing.T) {
	h := newHarness(t)
	spec, plan := h.artifacts(t, "first")
	created := h.addJSON(t, "First", "First ticket", spec, plan)
	spec, plan = h.artifacts(t, "second")
	h.addJSON(t, "Second", "Second ticket", spec, plan)

	if got := h.run(t, "next", "--json"); got != ExitOK {
		t.Fatalf("next exit = %d, stderr = %s", got, h.stderr.String())
	}
	next := decodeTicket(t, h.stdout.Bytes())
	if next.ID != created.ID || next.Status != ticketworker.StatusInProgress || next.StartedAt == nil {
		t.Fatalf("next ticket = %#v", next)
	}

	if got := h.run(t, "show", "1", "--json"); got != ExitOK {
		t.Fatalf("show exit = %d, stderr = %s", got, h.stderr.String())
	}
	shown := decodeTicket(t, h.stdout.Bytes())
	if shown.Status != ticketworker.StatusInProgress || shown.StartedAt == nil || !shown.UpdatedAt.Equal(next.UpdatedAt) {
		t.Fatalf("next claim not persisted: next=%#v shown=%#v", next, shown)
	}
}

func TestEndToEndFIFOAndLifecycle(t *testing.T) {
	h := newHarness(t)
	firstSpec, firstPlan := h.artifacts(t, "first")
	first := h.addJSON(t, "First", "First ticket", firstSpec, firstPlan)
	secondSpec, secondPlan := h.artifacts(t, "second")
	second := h.addJSON(t, "Second", "Second ticket", secondSpec, secondPlan)

	assertNext := func(wantID int64, wantStatus ticketworker.Status) {
		t.Helper()
		if got := h.run(t, "next", "--json"); got != ExitOK {
			t.Fatalf("next exit = %d, stderr = %s", got, h.stderr.String())
		}
		next := decodeTicket(t, h.stdout.Bytes())
		if next.ID != wantID || next.Status != wantStatus {
			t.Fatalf("next ticket = %#v, want ID %d/status %q", next, wantID, wantStatus)
		}
	}

	assertNext(first.ID, ticketworker.StatusInProgress)

	firstID := strconv.FormatInt(first.ID, 10)
	assertNext(second.ID, ticketworker.StatusInProgress)

	if got := h.run(t, "done", firstID, "--json"); got != ExitOK {
		t.Fatalf("done exit = %d, stderr = %s", got, h.stderr.String())
	}
	if done := decodeTicket(t, h.stdout.Bytes()); done.ID != first.ID || done.Status != ticketworker.StatusDone {
		t.Fatalf("done ticket = %#v, want ID %d/status %q", done, first.ID, ticketworker.StatusDone)
	}
	if got := h.run(t, "reopen", firstID, "--json"); got != ExitOK {
		t.Fatalf("reopen exit = %d, stderr = %s", got, h.stderr.String())
	}
	if reopened := decodeTicket(t, h.stdout.Bytes()); reopened.ID != first.ID || reopened.Status != ticketworker.StatusReady {
		t.Fatalf("reopened ticket = %#v, want ID %d/status %q", reopened, first.ID, ticketworker.StatusReady)
	}
	assertNext(first.ID, ticketworker.StatusInProgress)

	if got := h.run(t, "cancel", firstID, "--json"); got != ExitOK {
		t.Fatalf("cancel exit = %d, stderr = %s", got, h.stderr.String())
	}
	if cancelled := decodeTicket(t, h.stdout.Bytes()); cancelled.ID != first.ID || cancelled.Status != ticketworker.StatusCancelled {
		t.Fatalf("cancelled ticket = %#v, want ID %d/status %q", cancelled, first.ID, ticketworker.StatusCancelled)
	}
	if got := h.run(t, "next", "--json"); got != ExitEmptyQueue {
		t.Fatalf("next exit = %d, want %d", got, ExitEmptyQueue)
	}
}

func TestNextJSONReportsEmptyQueue(t *testing.T) {
	h := newHarness(t)

	if got := h.run(t, "next", "--json"); got != ExitEmptyQueue {
		t.Fatalf("next exit = %d, want %d", got, ExitEmptyQueue)
	}
	if got, want := h.stderr.String(), "{\"error\":\"no ready tickets\",\"code\":\"empty_queue\"}\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestListJSONFiltersReadyTickets(t *testing.T) {
	h := newHarness(t)
	firstSpec, firstPlan := h.artifacts(t, "first")
	first := h.addJSON(t, "First", "First ticket", firstSpec, firstPlan)
	secondSpec, secondPlan := h.artifacts(t, "second")
	second := h.addJSON(t, "Second", "Second ticket", secondSpec, secondPlan)
	if got := h.run(t, "next", "--json"); got != ExitOK {
		t.Fatalf("next exit = %d, stderr = %s", got, h.stderr.String())
	}

	if got := h.run(t, "list", "--status", "ready", "--json"); got != ExitOK {
		t.Fatalf("list exit = %d, stderr = %s", got, h.stderr.String())
	}
	var tickets []ticketworker.Ticket
	if err := json.Unmarshal(h.stdout.Bytes(), &tickets); err != nil {
		t.Fatalf("decode list JSON %q: %v", h.stdout.Bytes(), err)
	}
	if len(tickets) != 1 || tickets[0].ID != second.ID || tickets[0].ID == first.ID {
		t.Fatalf("ready tickets = %#v", tickets)
	}
}

func TestListJSONRejectsInvalidStatusAsUsageError(t *testing.T) {
	h := newHarness(t)

	if got := h.run(t, "list", "--status", "bogus", "--json"); got != ExitUsage {
		t.Fatalf("list exit = %d, want %d; stderr = %s", got, ExitUsage, h.stderr.String())
	}
	if got, want := h.stderr.String(), "{\"error\":\"invalid ticket status\",\"code\":\"usage\"}\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if h.stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", h.stdout.String())
	}
}

func TestLifecycleCommandsApplyTransitions(t *testing.T) {
	tests := []struct {
		name    string
		claim   bool
		actions []string
		status  ticketworker.Status
	}{
		{name: "next then done", claim: true, actions: []string{"done"}, status: ticketworker.StatusDone},
		{name: "cancel", actions: []string{"cancel"}, status: ticketworker.StatusCancelled},
		{name: "cancel then reopen", actions: []string{"cancel", "reopen"}, status: ticketworker.StatusReady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			spec, plan := h.artifacts(t, "flow")
			created := h.addJSON(t, "Flow", "Lifecycle ticket", spec, plan)
			if tt.claim {
				if got := h.run(t, "next", "--json"); got != ExitOK {
					t.Fatalf("next exit = %d, stderr = %s", got, h.stderr.String())
				}
				created = decodeTicket(t, h.stdout.Bytes())
			}
			for _, action := range tt.actions {
				if got := h.run(t, action, "1", "--json"); got != ExitOK {
					t.Fatalf("%s exit = %d, stderr = %s", action, got, h.stderr.String())
				}
				created = decodeTicket(t, h.stdout.Bytes())
			}
			if created.Status != tt.status {
				t.Fatalf("status = %q, want %q", created.Status, tt.status)
			}
		})
	}
}

func TestHumanOutputContract(t *testing.T) {
	h := newHarness(t)
	if got := h.run(t, "list"); got != ExitOK {
		t.Fatalf("empty list exit = %d, stderr = %s", got, h.stderr.String())
	}
	if got, want := h.stdout.String(), "No tickets.\n"; got != want {
		t.Fatalf("empty list = %q, want %q", got, want)
	}

	spec, plan := h.artifacts(t, "human")
	created := h.addJSON(t, "Human title", "Human summary", spec, plan)
	if got := h.run(t, "show", "1"); got != ExitOK {
		t.Fatalf("show exit = %d, stderr = %s", got, h.stderr.String())
	}
	for _, value := range []string{"ID: 1", "Status: ready", "Title: Human title", "Summary: Human summary", "Spec: " + created.SpecPath, "Plan: " + created.PlanPath} {
		if !strings.Contains(h.stdout.String(), value) {
			t.Fatalf("show output %q does not contain %q", h.stdout.String(), value)
		}
	}

	if got := h.run(t, "list"); got != ExitOK {
		t.Fatalf("list exit = %d, stderr = %s", got, h.stderr.String())
	}
	if got, want := h.stdout.String(), "1\tready\tHuman title\tdocs/superpowers/plans/human.md\n"; got != want {
		t.Fatalf("list output = %q, want %q", got, want)
	}
}

func TestOutputWriteFailuresReturnNonzeroDiagnostic(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		diagnostic string
		addTicket  bool
	}{
		{name: "single human", args: []string{"show", "1"}, diagnostic: "write output: forced write failure\n", addTicket: true},
		{name: "single JSON", args: []string{"show", "1", "--json"}, diagnostic: "{\"error\":\"write output: forced write failure\",\"code\":\"database\"}\n", addTicket: true},
		{name: "list human", args: []string{"list"}, diagnostic: "write output: forced write failure\n", addTicket: true},
		{name: "empty list human", args: []string{"list"}, diagnostic: "write output: forced write failure\n"},
		{name: "list JSON", args: []string{"list", "--json"}, diagnostic: "{\"error\":\"write output: forced write failure\",\"code\":\"database\"}\n", addTicket: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			if tt.addTicket {
				spec, plan := h.artifacts(t, "output-failure")
				h.addJSON(t, "Output", "Exercise output failures", spec, plan)
			}
			var stderr bytes.Buffer

			got := Run(context.Background(), tt.args, failingWriter{}, &stderr, h.deps)

			if got == ExitOK {
				t.Fatalf("Run(%q) exit = %d, want nonzero", tt.args, got)
			}
			if stderr.String() != tt.diagnostic {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.diagnostic)
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	tests := [][]string{
		{"show", "not-an-id"},
		{"start", "0"},
		{"done", "1", "extra"},
		{"add", "--title", "only title"},
		{"list", "--status", "ready", "extra"},
		{"unknown"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newHarness(t)
			if got := h.run(t, args...); got != ExitUsage {
				t.Fatalf("Run(%q) = %d, want %d; stderr = %s", args, got, ExitUsage, h.stderr.String())
			}
		})
	}
}

func TestNotFoundAndDomainErrorsMapToExitCodes(t *testing.T) {
	t.Run("missing ticket", func(t *testing.T) {
		h := newHarness(t)
		if got := h.run(t, "show", "1", "--json"); got != ExitNotFound {
			t.Fatalf("show exit = %d, want %d; stderr = %s", got, ExitNotFound, h.stderr.String())
		}
	})

	t.Run("duplicate plan", func(t *testing.T) {
		h := newHarness(t)
		spec, plan := h.artifacts(t, "duplicate")
		h.addJSON(t, "First", "First ticket", spec, plan)
		if got := h.run(t, "add", "--title", "Second", "--summary", "Second ticket", "--spec", spec, "--plan", plan); got != ExitDuplicate {
			t.Fatalf("duplicate exit = %d, want %d; stderr = %s", got, ExitDuplicate, h.stderr.String())
		}
	})

	t.Run("invalid transition", func(t *testing.T) {
		h := newHarness(t)
		spec, plan := h.artifacts(t, "invalid-transition")
		h.addJSON(t, "Ready", "Ready ticket", spec, plan)
		if got := h.run(t, "done", "1"); got != ExitInvalidTransition {
			t.Fatalf("done exit = %d, want %d; stderr = %s", got, ExitInvalidTransition, h.stderr.String())
		}
	})

	t.Run("missing artifact", func(t *testing.T) {
		h := newHarness(t)
		_, plan := h.artifacts(t, "existing")
		missingSpec := filepath.Join(h.root, "docs", "superpowers", "specs", "missing-design.md")
		if got := h.run(t, "add", "--title", "Missing", "--summary", "Missing artifact", "--spec", missingSpec, "--plan", plan); got != ExitValidation {
			t.Fatalf("missing artifact exit = %d, want %d; stderr = %s", got, ExitValidation, h.stderr.String())
		}
	})

	t.Run("misplaced artifact", func(t *testing.T) {
		h := newHarness(t)
		spec, _ := h.artifacts(t, "misplaced")
		misplacedPlan := filepath.Join(h.root, "docs", "plans", "misplaced.md")
		if err := os.MkdirAll(filepath.Dir(misplacedPlan), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(misplacedPlan, []byte("# Wrong place\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := h.run(t, "add", "--title", "Misplaced", "--summary", "Misplaced plan", "--spec", spec, "--plan", misplacedPlan); got != ExitValidation {
			t.Fatalf("misplaced artifact exit = %d, want %d; stderr = %s", got, ExitValidation, h.stderr.String())
		}
	})
}

func TestRunHelpListsQueueCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--help"}, &stdout, &stderr, Dependencies{})
	if code != ExitOK {
		t.Fatalf("help exit = %d, stderr = %q", code, stderr.String())
	}
	for _, command := range []string{"init", "add", "list", "next", "show", "start", "done", "cancel", "reopen"} {
		if !strings.Contains(stdout.String(), command) {
			t.Fatalf("help = %q, missing %q", stdout.String(), command)
		}
	}
	if !strings.Contains(stdout.String(), "start   Start the ticket manager pane") {
		t.Fatalf("help = %q, missing manager start description", stdout.String())
	}
}

func TestCommandBeforeInitFailsWithoutCreatingDatabase(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"list"}, &stdout, &stderr, Dependencies{StartDirectory: root})
	if code != ExitValidation {
		t.Fatalf("list exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ticket-worker is not initialized") {
		t.Fatalf("stderr = %q, want initialization guidance", stderr.String())
	}
	if _, err := os.Stat(ticketworker.DatabasePath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database stat error = %v, want not exist", err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{StartDirectory: root}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"init"}, &stdout, &stderr, deps); code != ExitOK {
		t.Fatalf("init exit = %d, stderr = %q", code, stderr.String())
	}
	for _, path := range []string{ticketworker.DatabasePath(root), ticketworker.ConfigPath(root)} {
		if !strings.Contains(stdout.String(), path) {
			t.Fatalf("init stdout = %q, missing %q", stdout.String(), path)
		}
	}
	custom := "version: 1\nmax_workers: 1\npoll_interval: 5s\nprompt_template: custom {{ .ID }}\n"
	if err := os.WriteFile(ticketworker.ConfigPath(root), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"init"}, &stdout, &stderr, deps); code != ExitOK {
		t.Fatalf("second init exit = %d, stderr = %q", code, stderr.String())
	}
	data, err := os.ReadFile(ticketworker.ConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Fatalf("config after second init = %q, want %q", data, custom)
	}
}
