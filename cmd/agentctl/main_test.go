package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/transport"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: agentctl") {
		t.Fatalf("stdout = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHealth(t *testing.T) {
	client := &fakeAgentClient{
		healthResponse: transport.HealthResponse{Status: "ok", Version: "test"},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"health", "--socket", "/tmp/custom.sock"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.socketPath != "/tmp/custom.sock" {
		t.Fatalf("socket path = %q, want custom socket", client.socketPath)
	}
	if !strings.Contains(stdout.String(), "agentd ok (test)") {
		t.Fatalf("stdout = %q, want health summary", stdout.String())
	}
}

func TestRunStatusPrintsRuntimeSummary(t *testing.T) {
	client := &fakeAgentClient{
		statusResponse: transport.InspectRuntimeResponse{
			Message: "runtime healthy",
			Counts:  transport.RuntimeCounts{Managed: 1, Active: 1, Running: 1},
			Panes: []transport.Pane{
				{ID: "tester", Role: "test", TaskID: "task-1", Status: "running", ZellijPaneID: "terminal_1"},
			},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"status"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "runtime healthy") || !strings.Contains(output, "- tester role=test task=task-1 status=running") {
		t.Fatalf("stdout = %q, want runtime summary", output)
	}
}

func TestRunPlanSubmitsExecutionPlanFile(t *testing.T) {
	planPath := writeTempFile(t, `{
		"session": "feature-auth",
		"layout": "triple-horizontal",
		"tabs": [
			{"name": "main", "panes": [{"id": "planner", "role": "planner"}]}
		]
	}`)
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--file", planPath, "--request-id", "req_123"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID != "req_123" || client.planPayload.Session != "feature-auth" {
		t.Fatalf("submitted plan = request %q payload %#v, want feature-auth", client.planRequestID, client.planPayload)
	}
	if len(client.planPayload.Tabs) != 1 || client.planPayload.Tabs[0].Panes[0].ID != "planner" {
		t.Fatalf("submitted tabs = %#v, want planner pane", client.planPayload.Tabs)
	}
	if !strings.Contains(stdout.String(), "request=req_123 session=feature-auth") {
		t.Fatalf("stdout = %q, want plan summary", stdout.String())
	}
}

func TestRunDebateSubmitsPlan(t *testing.T) {
	client := &fakeAgentClient{streamEventsFromInputs: true}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "Should we use markers?",
		"--agents", "a,b,c",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
		"--timeout", "5s",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID == "" {
		t.Fatal("plan request id = empty, want debate request id")
	}
	if client.planPayload.Session == "" || client.planPayload.Layout != "debate" {
		t.Fatalf("plan payload = %#v, want debate session/layout", client.planPayload)
	}
	if len(client.planPayload.Tabs) != 1 {
		t.Fatalf("plan tabs = %d, want 1", len(client.planPayload.Tabs))
	}
	panes := client.planPayload.Tabs[0].Panes
	wantIDs := []string{"debate-coordinator", "debate-a", "debate-b", "debate-c"}
	if len(panes) != len(wantIDs) {
		t.Fatalf("plan panes = %#v, want %d panes", panes, len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if panes[i].ID != wantID {
			t.Fatalf("pane[%d].ID = %q, want %q", i, panes[i].ID, wantID)
		}
	}
	if panes[0].Role != "debate-coordinator" || !containsString(panes[0].Command, "debate-coordinator") {
		t.Fatalf("coordinator pane = %#v, want debate-coordinator role command", panes[0])
	}
	if panes[1].Role != "coding-agent" || len(panes[1].Command) == 0 {
		t.Fatalf("agent pane = %#v, want coding-agent command", panes[1])
	}
	if !strings.Contains(stdout.String(), "debate request=") {
		t.Fatalf("stdout = %q, want debate summary", stdout.String())
	}
}

func TestRunDebateLoadsAgentCommandsFromConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "debate.yaml")
	config := `agents:
  - id: a
    command: ["agy", "--dangerously-skip-permissions"]
  - id: b
    command: ["agent", "--yolo", "--model", "claude-opus-4-8-thinking-high"]
    submit_newlines: 2
    extra_submit_enters: 1
    extra_submit_delay_ms: 1
  - id: c
    command: ["codex", "--dangerously-bypass-approvals-and-sandbox"]
coordinator:
  command: ["./bin/zellij-agent", "role", "debate-coordinator", "/repo"]
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	client := &fakeAgentClient{streamEventsFromInputs: true}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "configured agents",
		"--rounds", "1",
		"--config", configPath,
		"--cwd", "/repo",
		"--timeout", "5s",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	panes := client.planPayload.Tabs[0].Panes
	if len(panes) != 4 {
		t.Fatalf("panes = %#v, want coordinator plus three agents", panes)
	}
	if panes[0].ID != "debate-coordinator" || strings.Join(panes[0].Command, " ") != "./bin/zellij-agent role debate-coordinator /repo" {
		t.Fatalf("coordinator pane = %#v, want configured coordinator command", panes[0])
	}
	wantCommands := map[string]string{
		"debate-a": "agy --dangerously-skip-permissions",
		"debate-b": "agent --yolo --model claude-opus-4-8-thinking-high",
		"debate-c": "codex --dangerously-bypass-approvals-and-sandbox",
	}
	for _, pane := range panes[1:] {
		if got, want := strings.Join(pane.Command, " "), wantCommands[pane.ID]; got != want {
			t.Fatalf("%s command = %q, want %q", pane.ID, got, want)
		}
		if pane.CWD != "/repo" {
			t.Fatalf("%s CWD = %q, want /repo", pane.ID, pane.CWD)
		}
	}
	inputsB := filterInputRequests(client.inputRequests, "debate-b")
	if len(inputsB) != 2 {
		t.Fatalf("debate-b inputs = %#v, want prompt plus extra submit enter", inputsB)
	}
	if !strings.HasSuffix(inputsB[0].req.Text, "\n\n") {
		t.Fatalf("debate-b input = %q, want double newline submit suffix", inputsB[0].req.Text)
	}
	if inputsB[1].req.Text != "\n" {
		t.Fatalf("debate-b extra input = %q, want Enter", inputsB[1].req.Text)
	}
}

func TestRunDebateSendsRoundPromptWithMarkers(t *testing.T) {
	client := &fakeAgentClient{streamEventsFromInputs: true}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "marker test",
		"--agents", "a,b",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(client.inputRequests) < 2 {
		t.Fatalf("input requests = %#v, want at least 2 round prompts", client.inputRequests)
	}
	roundInputs := client.inputRequests[:2]
	for _, req := range roundInputs {
		if !strings.Contains(req.req.Text, "Round: 1") ||
			!strings.Contains(req.req.Text, "Topic: marker test") ||
			!strings.Contains(req.req.Text, "Completion marker parts:") ||
			!strings.Contains(req.req.Text, "<<<AGENT_DEBATE_DONE") {
			t.Fatalf("input text = %q, want round prompt with marker construction instructions", req.req.Text)
		}
		if containsExactDebateMarker(req.req.Text) {
			t.Fatalf("input text = %q, must not contain exact marker before agent completion", req.req.Text)
		}
	}
	if roundInputs[0].paneID != "debate-a" || roundInputs[1].paneID != "debate-b" {
		t.Fatalf("input targets = %#v, want debate-a and debate-b", roundInputs)
	}
}

func TestRunDebateSendsSynthesisBlockToCoordinator(t *testing.T) {
	client := &fakeAgentClient{
		streamEventsFromInputs: true,
		snapshotOutputsByPane: map[string]string{
			"debate-a":           "answer from a",
			"debate-b":           "answer from b",
			"debate-coordinator": "final synthesis",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "synthesis test",
		"--agents", "a,b",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	synthesisInputs := filterInputRequests(client.inputRequests, "debate-coordinator")
	if len(synthesisInputs) != 1 {
		t.Fatalf("coordinator inputs = %#v, want one synthesis block", synthesisInputs)
	}
	text := synthesisInputs[0].req.Text
	if !strings.Contains(text, "<<<DEBATE_SYNTHESIS_BEGIN>>>") ||
		!strings.Contains(text, "Completion-Marker-Base64:") ||
		!strings.Contains(text, "[round 1 debate-a]") ||
		!strings.Contains(text, "answer from a") ||
		!strings.Contains(text, "[round 1 debate-b]") ||
		!strings.Contains(text, "answer from b") ||
		!strings.Contains(text, "<<<DEBATE_SYNTHESIS_END>>>") {
		t.Fatalf("synthesis input = %q, want coordinator block", text)
	}
	if containsExactDebateMarker(text) {
		t.Fatalf("synthesis input = %q, must not contain exact marker before coordinator completion", text)
	}
	if len(client.createPaneRequests) != 0 {
		t.Fatalf("create pane requests = %#v, want coordinator created by initial plan", client.createPaneRequests)
	}
	output := stdout.String()
	if !strings.Contains(output, "[debate-coordinator synthesis]") || !strings.Contains(output, "final synthesis") {
		t.Fatalf("stdout = %q, want coordinator synthesis output", output)
	}
}

func TestRunDebateSupportsThreeRounds(t *testing.T) {
	client := &fakeAgentClient{
		streamEventsFromInputs: true,
		snapshotOutputsByPaneSequence: map[string][]string{
			"debate-a": {
				"a round 1",
				"a round 2",
				"a round 3",
			},
			"debate-b": {
				"b round 1",
				"b round 2",
				"b round 3",
			},
			"debate-coordinator": {"final synthesis"},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "multi round test",
		"--agents", "a,b",
		"--rounds", "3",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	inputsA := filterInputRequests(client.inputRequests, "debate-a")
	inputsB := filterInputRequests(client.inputRequests, "debate-b")
	if len(inputsA) != 3 || len(inputsB) != 3 {
		t.Fatalf("inputs a=%d b=%d, want 3 each; all=%#v", len(inputsA), len(inputsB), client.inputRequests)
	}
	if !strings.Contains(inputsA[1].req.Text, "Round: 2") ||
		!strings.Contains(inputsA[1].req.Text, "[round 1 debate-a]") ||
		!strings.Contains(inputsA[1].req.Text, "a round 1") ||
		!strings.Contains(inputsA[1].req.Text, "[round 1 debate-b]") ||
		!strings.Contains(inputsA[1].req.Text, "b round 1") {
		t.Fatalf("round 2 prompt = %q, want round 1 context", inputsA[1].req.Text)
	}
	if !strings.Contains(inputsA[2].req.Text, "Round: 3") ||
		!strings.Contains(inputsA[2].req.Text, "[round 2 debate-a]") ||
		!strings.Contains(inputsA[2].req.Text, "a round 2") ||
		containsExactDebateMarker(inputsA[2].req.Text) {
		t.Fatalf("round 3 prompt = %q, want round 2 context without exact marker", inputsA[2].req.Text)
	}
	synthesisInputs := filterInputRequests(client.inputRequests, "debate-coordinator")
	if len(synthesisInputs) != 1 {
		t.Fatalf("coordinator inputs = %#v, want one final synthesis block", synthesisInputs)
	}
	synthesis := synthesisInputs[0].req.Text
	for _, want := range []string{
		"[round 1 debate-a]", "a round 1",
		"[round 2 debate-b]", "b round 2",
		"[round 3 debate-a]", "a round 3",
	} {
		if !strings.Contains(synthesis, want) {
			t.Fatalf("synthesis = %q, missing %q", synthesis, want)
		}
	}
	if len(client.snapshotRequests) != 7 {
		t.Fatalf("snapshot requests = %#v, want 6 agent snapshots plus coordinator", client.snapshotRequests)
	}
	if !strings.Contains(stdout.String(), "[round 3 debate-b]") {
		t.Fatalf("stdout = %q, want round-labeled outputs", stdout.String())
	}
}

func TestRunDebateRejectsRoundsOutsideSupportedRange(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "bad rounds",
		"--rounds", "4",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "debate requires --rounds between 1 and 3") {
		t.Fatalf("stderr = %q, want rounds validation error", stderr.String())
	}
	if client.planRequestID != "" {
		t.Fatalf("plan request id = %q, want no submitted plan", client.planRequestID)
	}
}

func TestRunDebateWaitsForMarkersAndSnapshots(t *testing.T) {
	client := &fakeAgentClient{
		streamEventsFromInputs: true,
		snapshotOutputsByPane: map[string]string{
			"debate-a": "answer from a",
			"debate-b": "answer from b",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "snapshot test",
		"--agents", "a,b",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !client.streamEventsCalled {
		t.Fatal("StreamEvents called = false, want true")
	}
	if len(client.snapshotRequests) != 3 {
		t.Fatalf("snapshot requests = %#v, want 3", client.snapshotRequests)
	}
	if client.snapshotRequests[0].paneID != "debate-a" || client.snapshotRequests[1].paneID != "debate-b" {
		t.Fatalf("snapshot requests = %#v, want debate-a and debate-b", client.snapshotRequests)
	}
	if client.snapshotRequests[2].paneID != "debate-coordinator" {
		t.Fatalf("snapshot requests = %#v, want coordinator synthesis snapshot", client.snapshotRequests)
	}
	if !client.snapshotRequests[0].req.Full || !client.snapshotRequests[1].req.Full || !client.snapshotRequests[2].req.Full {
		t.Fatalf("snapshot requests = %#v, want full snapshots", client.snapshotRequests)
	}
	output := stdout.String()
	if !strings.Contains(output, "answer from a") || !strings.Contains(output, "answer from b") {
		t.Fatalf("stdout = %q, want snapshot outputs", output)
	}
}

func TestRunDebateTimesOutWhenMarkerMissing(t *testing.T) {
	client := &fakeAgentClient{
		streamEventsFromInputs: true,
		streamOmitPanes: map[string]bool{
			"debate-b": true,
		},
		streamKeepOpen:         true,
		snapshotOutputsByPane: map[string]string{
			"debate-a":           "answer from a",
			"debate-b":           "auth failed: not logged in",
			"debate-coordinator": "partial synthesis",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "timeout test",
		"--agents", "a,b",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
		"--agent-timeout", "20ms",
		"--timeout", "5s",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if len(client.snapshotRequests) != 3 {
		t.Fatalf("snapshot requests = %#v, want two agents plus coordinator after partial timeout", client.snapshotRequests)
	}
	output := stdout.String()
	for _, want := range []string{
		"round=1 pane=debate-a status=done",
		"round=1 pane=debate-b status=timed_out",
		"[round 1 debate-b]",
		"auth failed: not logged in",
		"[debate-coordinator synthesis]",
		"partial synthesis",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
	synthesisInputs := filterInputRequests(client.inputRequests, "debate-coordinator")
	if len(synthesisInputs) != 1 {
		t.Fatalf("coordinator inputs = %#v, want one synthesis block", synthesisInputs)
	}
	synthesis := synthesisInputs[0].req.Text
	for _, want := range []string{
		"[round 1 debate-b] status=timed_out",
		"auth failed: not logged in",
	} {
		if !strings.Contains(synthesis, want) {
			t.Fatalf("synthesis = %q, missing %q", synthesis, want)
		}
	}
}

func TestRunDebateFailsWhenOverallTimeoutExpiresBeforeAgentTimeout(t *testing.T) {
	client := &fakeAgentClient{
		streamEventsFromInputs: true,
		streamOmitPanes: map[string]bool{
			"debate-b": true,
		},
		streamKeepOpen: true,
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"debate",
		"--topic", "overall timeout test",
		"--agents", "a,b",
		"--cwd", "/repo",
		"--agent-role-bin", "/bin/zellij-agent",
		"--agent-timeout", "5s",
		"--timeout", "20ms",
	}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 1 {
		t.Fatalf("run() exit code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "context deadline exceeded") || !strings.Contains(stderr.String(), "debate-b") {
		t.Fatalf("stderr = %q, want overall timeout with missing debate-b", stderr.String())
	}
	if len(client.snapshotRequests) != 0 {
		t.Fatalf("snapshot requests = %#v, want none after overall timeout", client.snapshotRequests)
	}
}

func TestRunPlanAcceptsRequestEnvelopeFromStdin(t *testing.T) {
	input := `{
		"type": "execution_plan",
		"request_id": "req_from_stdin",
		"payload": {
			"session": "demo",
			"tabs": [
				{"name": "demo", "panes": [{"id": "coder", "role": "coder"}]}
			]
		}
	}`
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--file", "-"}, strings.NewReader(input), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID != "req_from_stdin" || client.planPayload.Session != "demo" {
		t.Fatalf("submitted plan = request %q payload %#v, want stdin envelope", client.planRequestID, client.planPayload)
	}
}

func TestRunPlanAcceptsCanonicalBadgeCategoryEnvelope(t *testing.T) {
	planPath := filepath.Join("..", "..", "examples", "plans", "badge-category-source-lsp.json")
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--file", planPath}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.planRequestID != "req_badge_category_source_lsp" {
		t.Fatalf("request id = %q, want req_badge_category_source_lsp", client.planRequestID)
	}
	if client.planPayload.Session != "badge-category-source-lsp" || client.planPayload.Layout != "triple-horizontal" {
		t.Fatalf("payload = %#v, want canonical badge-category payload", client.planPayload)
	}
	if len(client.planPayload.Tabs) != 1 || len(client.planPayload.Tabs[0].Panes) != 2 {
		t.Fatalf("tabs = %#v, want one tab with editor and lsp panes", client.planPayload.Tabs)
	}
	if client.planPayload.Tabs[0].Panes[0].ID != "badge-category-editor" || client.planPayload.Tabs[0].Panes[1].ID != "badge-category-lsp" {
		t.Fatalf("panes = %#v, want editor and lsp", client.planPayload.Tabs[0].Panes)
	}
}

func TestRunEventsPassesFilters(t *testing.T) {
	client := &fakeAgentClient{
		eventsResponse: transport.RecentEventsResponse{
			Events: []transport.Event{
				{Type: "test_passed", PaneID: "test", TaskID: "task-1", Message: "ok", Time: time.Unix(1, 0)},
			},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"events", "--limit", "5", "--type", "test_passed"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.eventsLimit != 5 || len(client.eventTypes) != 1 || client.eventTypes[0] != "test_passed" {
		t.Fatalf("event filters = limit %d types %#v, want test_passed", client.eventsLimit, client.eventTypes)
	}
	if !strings.Contains(stdout.String(), "type=test_passed pane=test") {
		t.Fatalf("stdout = %q, want event summary", stdout.String())
	}
}

func TestRunEventsFollowStreamsAndFilters(t *testing.T) {
	events := make(chan transport.Event, 2)
	events <- transport.Event{Type: "raw_output", PaneID: "coder", Message: "ignored", Time: time.Unix(1, 0)}
	events <- transport.Event{Type: "message_sent", PaneID: "tester", Message: "delivered", Time: time.Unix(2, 0)}
	close(events)
	errs := make(chan error)
	client := &fakeAgentClient{
		eventStream: &transport.EventStream{
			Events: events,
			Errors: errs,
			Close:  func() error { return nil },
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"events", "--follow", "--type", "message_sent"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !client.streamEventsCalled {
		t.Fatal("StreamEvents was not called")
	}
	if strings.Contains(output, "raw_output") || !strings.Contains(output, "type=message_sent pane=tester") {
		t.Fatalf("stdout = %q, want only filtered message_sent event", output)
	}
}

func TestRunInputSendsText(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"input", "pane-1", "--text", "hello\n"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.inputPaneID != "pane-1" || client.inputRequest.Text != "hello\n" {
		t.Fatalf("input request = pane %q %#v, want pane-1 hello", client.inputPaneID, client.inputRequest)
	}
	if !strings.Contains(stdout.String(), "sent input pane=pane-1 bytes=6") {
		t.Fatalf("stdout = %q, want input summary", stdout.String())
	}
}

func TestRunInputReadsStdin(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"input", "pane-1", "--file", "-"}, strings.NewReader("from stdin"), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.inputRequest.Text != "from stdin" {
		t.Fatalf("input text = %q, want stdin payload", client.inputRequest.Text)
	}
}

func TestRunSnapshotPrintsOutput(t *testing.T) {
	client := &fakeAgentClient{
		snapshotResponse: transport.SnapshotOutputResponse{
			Output: "pane output\n",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"snapshot", "pane-1", "--full", "--ansi"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.snapshotPaneID != "pane-1" || !client.snapshotRequest.Full || !client.snapshotRequest.ANSI {
		t.Fatalf("snapshot request = pane %q %#v, want full ansi", client.snapshotPaneID, client.snapshotRequest)
	}
	if stdout.String() != "pane output\n" {
		t.Fatalf("stdout = %q, want raw output", stdout.String())
	}
}

func TestRunMessageSendsBody(t *testing.T) {
	client := &fakeAgentClient{}
	var stdout, stderr bytes.Buffer

	code := run([]string{"message", "--from", "planner", "--to", "tester", "--type", "task", "--body", "run tests"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.messageRequest.From != "planner" || client.messageRequest.To != "tester" || client.messageRequest.Type != "task" || client.messageRequest.Body != "run tests" {
		t.Fatalf("message request = %#v, want planner to tester task", client.messageRequest)
	}
	if !strings.Contains(stdout.String(), "delivered from=planner to=tester type=task bytes=9") {
		t.Fatalf("stdout = %q, want delivery summary", stdout.String())
	}
}

func TestRunForwardSnapshotSendsSnapshotOutput(t *testing.T) {
	client := &fakeAgentClient{
		snapshotResponse: transport.SnapshotOutputResponse{
			Output: "screen dump",
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"forward-snapshot", "coder", "reviewer", "--full"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if client.snapshotPaneID != "coder" || !client.snapshotRequest.Full {
		t.Fatalf("snapshot request = pane %q %#v, want coder full", client.snapshotPaneID, client.snapshotRequest)
	}
	if client.messageRequest.From != "coder" || client.messageRequest.To != "reviewer" || client.messageRequest.Type != "screen_dump" || client.messageRequest.Body != "screen dump" {
		t.Fatalf("message request = %#v, want forwarded snapshot", client.messageRequest)
	}
	if !strings.Contains(stdout.String(), "delivered from=coder to=reviewer type=screen_dump bytes=11") {
		t.Fatalf("stdout = %q, want delivery summary", stdout.String())
	}
}

func TestRunCleanupPassesFilters(t *testing.T) {
	client := &fakeAgentClient{
		cleanupResponse: transport.CleanupResponse{
			Closed: []transport.Pane{{ID: "pane-1", Status: "closed"}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"cleanup", "--pane", "pane-1", "--task", "task-1", "--role", "test"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(client))

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(client.cleanupRequest.PaneIDs) != 1 || client.cleanupRequest.PaneIDs[0] != "pane-1" || client.cleanupRequest.TaskID != "task-1" || client.cleanupRequest.Role != "test" {
		t.Fatalf("cleanup request = %#v, want pane/task/role filters", client.cleanupRequest)
	}
	if !strings.Contains(stdout.String(), "closed=1 failed=0 skipped=0") {
		t.Fatalf("stdout = %q, want cleanup summary", stdout.String())
	}
}

func TestRunPlanRequiresFile(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan"}, strings.NewReader(""), &stdout, &stderr, fakeFactory(&fakeAgentClient{}))

	if code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "plan requires --file") {
		t.Fatalf("stderr = %q, want missing file error", stderr.String())
	}
}

func writeTempFile(t *testing.T, contents string) string {
	t.Helper()
	path := t.TempDir() + "/plan.json"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func filterMessageRequests(requests []transport.SendMessageRequest, messageType string) []transport.SendMessageRequest {
	filtered := make([]transport.SendMessageRequest, 0, len(requests))
	for _, req := range requests {
		if req.Type == messageType {
			filtered = append(filtered, req)
		}
	}
	return filtered
}

func filterInputRequests(requests []fakeInputRequest, paneID string) []fakeInputRequest {
	filtered := make([]fakeInputRequest, 0, len(requests))
	for _, req := range requests {
		if req.paneID == paneID {
			filtered = append(filtered, req)
		}
	}
	return filtered
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsExactDebateMarker(text string) bool {
	return regexp.MustCompile(`<<<AGENT_DEBATE_DONE debate=[^>]+>>>`).FindString(text) != ""
}

func fakeFactory(client *fakeAgentClient) clientFactory {
	return func(socketPath string, timeout time.Duration) agentClient {
		client.socketPath = socketPath
		client.timeout = timeout
		return client
	}
}

type fakeAgentClient struct {
	socketPath string
	timeout    time.Duration

	healthResponse                transport.HealthResponse
	statusResponse                transport.InspectRuntimeResponse
	snapshotResponse              transport.SnapshotOutputResponse
	snapshotOutputsByPane         map[string]string
	snapshotOutputsByPaneSequence map[string][]string
	eventsResponse                transport.RecentEventsResponse
	cleanupResponse               transport.CleanupResponse
	messageResponse               transport.SendMessageResponse
	eventStream                   *transport.EventStream

	planRequestID      string
	planPayload        transport.ExecutionPlanPayload
	createPaneRequests []transport.CreatePaneRequest

	inputPaneID          string
	inputRequest         transport.SendInputRequest
	inputRequests        []fakeInputRequest
	snapshotPaneID       string
	snapshotRequest      transport.SnapshotOutputRequest
	snapshotRequests     []fakeSnapshotRequest
	snapshotCountsByPane map[string]int
	messageRequest       transport.SendMessageRequest
	messageRequests      []transport.SendMessageRequest

	eventsLimit              int
	eventTypes               []string
	streamEventsCalled       bool
	streamEventsFromMessages bool
	streamEventsFromInputs   bool
	streamEventLimit         int
	streamKeepOpen           bool
	streamOmitPanes          map[string]bool

	cleanupRequest transport.CleanupRequest
}

type fakeInputRequest struct {
	paneID string
	req    transport.SendInputRequest
}

type fakeSnapshotRequest struct {
	paneID string
	req    transport.SnapshotOutputRequest
}

func (c *fakeAgentClient) Health(context.Context) (transport.HealthResponse, error) {
	return c.healthResponse, nil
}

func (c *fakeAgentClient) InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error) {
	return c.statusResponse, nil
}

func (c *fakeAgentClient) SendInput(_ context.Context, paneID string, req transport.SendInputRequest) error {
	c.inputPaneID = paneID
	c.inputRequest = req
	c.inputRequests = append(c.inputRequests, fakeInputRequest{paneID: paneID, req: req})
	return nil
}

func (c *fakeAgentClient) SnapshotOutput(_ context.Context, paneID string, req transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error) {
	c.snapshotPaneID = paneID
	c.snapshotRequest = req
	c.snapshotRequests = append(c.snapshotRequests, fakeSnapshotRequest{paneID: paneID, req: req})
	if outputs, ok := c.snapshotOutputsByPaneSequence[paneID]; ok {
		if c.snapshotCountsByPane == nil {
			c.snapshotCountsByPane = make(map[string]int)
		}
		index := c.snapshotCountsByPane[paneID]
		c.snapshotCountsByPane[paneID]++
		if index >= len(outputs) {
			index = len(outputs) - 1
		}
		return transport.SnapshotOutputResponse{Pane: transport.Pane{ID: paneID}, Output: outputs[index]}, nil
	}
	if output, ok := c.snapshotOutputsByPane[paneID]; ok {
		return transport.SnapshotOutputResponse{Pane: transport.Pane{ID: paneID}, Output: output}, nil
	}
	return c.snapshotResponse, nil
}

func (c *fakeAgentClient) SendMessage(_ context.Context, req transport.SendMessageRequest) (transport.SendMessageResponse, error) {
	c.messageRequest = req
	c.messageRequests = append(c.messageRequests, req)
	if c.messageResponse.From.ID != "" || c.messageResponse.To.ID != "" {
		return c.messageResponse, nil
	}
	return transport.SendMessageResponse{
		From: transport.Pane{ID: req.From},
		To:   transport.Pane{ID: req.To},
		Type: req.Type,
		Body: req.Body,
	}, nil
}

func (c *fakeAgentClient) CreatePane(_ context.Context, req transport.CreatePaneRequest) (transport.CreatePaneResponse, error) {
	c.createPaneRequests = append(c.createPaneRequests, req)
	return transport.CreatePaneResponse{
		Pane: transport.Pane{
			ID:           req.ID,
			Role:         req.Role,
			AgentID:      req.AgentID,
			ZellijTabID:  req.ZellijTabID,
			Status:       "running",
			ZellijPaneID: "terminal_coordinator",
		},
	}, nil
}

func (c *fakeAgentClient) SubmitExecutionPlan(_ context.Context, requestID string, payload transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error) {
	c.planRequestID = requestID
	c.planPayload = payload
	return transport.ExecutionPlanResponse{
		RequestID: requestID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs: []transport.ExecutionPlanTabResponse{
			{
				Name:  "main",
				Panes: fakePlanResponsePanes(payload),
			},
		},
	}, nil
}

func fakePlanResponsePanes(payload transport.ExecutionPlanPayload) []transport.Pane {
	if len(payload.Tabs) == 0 {
		return nil
	}
	panes := make([]transport.Pane, 0, len(payload.Tabs[0].Panes))
	tabID := 7
	for i, pane := range payload.Tabs[0].Panes {
		panes = append(panes, transport.Pane{
			ID:           pane.ID,
			Role:         pane.Role,
			AgentID:      pane.AgentID,
			Status:       "running",
			ZellijPaneID: fmt.Sprintf("terminal_%d", i+1),
			ZellijTabID:  &tabID,
		})
	}
	return panes
}

func (c *fakeAgentClient) RecentEvents(_ context.Context, limit int, eventTypes ...string) (transport.RecentEventsResponse, error) {
	c.eventsLimit = limit
	c.eventTypes = append([]string(nil), eventTypes...)
	return c.eventsResponse, nil
}

func (c *fakeAgentClient) StreamEvents(context.Context) (*transport.EventStream, error) {
	c.streamEventsCalled = true
	if c.eventStream != nil {
		return c.eventStream, nil
	}
	if c.streamEventsFromMessages {
		events := make(chan transport.Event, len(c.messageRequests))
		for i, req := range c.messageRequests {
			if c.streamEventLimit > 0 && i >= c.streamEventLimit {
				break
			}
			events <- transport.Event{Type: "raw_output", PaneID: req.To, Message: extractCompletionMarker(req.Body)}
		}
		errs := make(chan error)
		if !c.streamKeepOpen {
			close(events)
			close(errs)
		}
		return &transport.EventStream{
			Events: events,
			Errors: errs,
			Close:  func() error { return nil },
		}, nil
	}
	if c.streamEventsFromInputs {
		events := make(chan transport.Event, len(c.inputRequests))
		for i, req := range c.inputRequests {
			if c.streamEventLimit > 0 && i >= c.streamEventLimit {
				break
			}
			if c.streamOmitPanes[req.paneID] {
				continue
			}
			events <- transport.Event{Type: "raw_output", PaneID: req.paneID, Message: extractCompletionMarker(req.req.Text)}
		}
		errs := make(chan error)
		if !c.streamKeepOpen {
			close(events)
			close(errs)
		}
		return &transport.EventStream{
			Events: events,
			Errors: errs,
			Close:  func() error { return nil },
		}, nil
	}
	events := make(chan transport.Event)
	close(events)
	errs := make(chan error)
	return &transport.EventStream{
		Events: events,
		Errors: errs,
		Close:  func() error { return nil },
	}, nil
}

func extractCompletionMarker(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "<<<AGENT_DEBATE_DONE") {
			return line
		}
		if strings.HasPrefix(line, "Completion-Marker:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Completion-Marker:"))
		}
		if strings.HasPrefix(line, "Completion-Marker-Base64:") {
			encoded := strings.TrimSpace(strings.TrimPrefix(line, "Completion-Marker-Base64:"))
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil {
				return string(decoded)
			}
		}
	}
	if marker := extractCompletionMarkerFromParts(body); marker != "" {
		return marker
	}
	return ""
}

func extractCompletionMarkerFromParts(body string) string {
	lines := strings.Split(body, "\n")
	collecting := false
	parts := make([]string, 0, 6)
	partLine := regexp.MustCompile(`^\s*\d+\.\s?(.*)$`)
	for _, line := range lines {
		if strings.TrimSpace(line) == "Completion marker parts:" {
			collecting = true
			continue
		}
		if !collecting {
			continue
		}
		match := partLine.FindStringSubmatch(line)
		if len(match) == 2 {
			parts = append(parts, match[1])
			continue
		}
		if len(parts) > 0 && strings.TrimSpace(line) == "" {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "")
}

func (c *fakeAgentClient) Cleanup(_ context.Context, req transport.CleanupRequest) (transport.CleanupResponse, error) {
	c.cleanupRequest = req
	return c.cleanupResponse, nil
}
