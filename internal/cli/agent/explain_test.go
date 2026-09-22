package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/codingagent"
	"zellij-with-codeagent/internal/transport"
)

type explanationClient struct {
	*testClient
	response transport.ExplainAgentResponse
	err      error
	id       string
	deadline time.Time
}

func (c *explanationClient) ExplainAgent(ctx context.Context, id string) (transport.ExplainAgentResponse, error) {
	c.id = id
	c.deadline, _ = ctx.Deadline()
	return c.response, c.err
}

func TestRunExplainPrintsEvidenceAndJSON(t *testing.T) {
	response := transport.ExplainAgentResponse{
		AgentID: "agent-1", Kind: codingagent.KindCodex, PaneID: "pane-1", State: codingagent.StateWorking,
		StateReason: "matched_manifest_rule", MatchedRule: "working", ObservationAvailable: true,
		StateChangedAt: time.Unix(20, 0).UTC(), ObservedAt: time.Unix(30, 0).UTC(),
		PendingIdle: true, IdleConfirmations: 2,
		TitleAvailable: true, Title: "Title\x1b[31m", TitleSource: "zellij_pane_title", TitleObservedAt: time.Unix(29, 0).UTC(), TitleUsedForDetection: true,
		Detection: &codingagent.DetectionExplanation{
			Detection: codingagent.Detection{State: codingagent.StateIdle, Reason: "no_match", Fallback: true},
			Rules:     []codingagent.RuleEvaluation{{ID: "working", Priority: 10, Region: codingagent.Region{Type: codingagent.RegionOSCTitle}, Evidence: "Title\x1b[31m\n", Truncated: true}},
		},
	}
	for _, jsonOutput := range []bool{false, true} {
		client := &explanationClient{testClient: &testClient{}, response: response}
		factory := func(socket string, timeout time.Duration) AgentClient {
			if socket != "/tmp/explain.sock" || timeout != 3*time.Second {
				t.Fatalf("socket=%q timeout=%s", socket, timeout)
			}
			return client
		}
		args := []string{"explain", "--socket", "/tmp/explain.sock", "--timeout", "3s"}
		if jsonOutput {
			args = append(args, "--json")
		}
		args = append(args, "agent-1")
		var stdout, stderr bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &stderr, factory, Config{})
		if code != 0 || stderr.Len() != 0 || client.id != "agent-1" {
			t.Fatalf("code=%d id=%q stderr=%q", code, client.id, stderr.String())
		}
		if client.deadline.IsZero() || time.Until(client.deadline) > 3*time.Second {
			t.Fatalf("request deadline=%s", client.deadline)
		}
		if jsonOutput {
			var got transport.ExplainAgentResponse
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || !reflect.DeepEqual(got, response) {
				t.Fatalf("JSON=%s decoded=%#v err=%v", stdout.String(), got, err)
			}
			if !strings.Contains(stdout.String(), `"agent_id":"agent-1"`) || !strings.Contains(stdout.String(), `"idle_confirmations":2`) {
				t.Fatalf("unexpected JSON keys: %s", stdout.String())
			}
			continue
		}
		for _, want := range []string{`state="working"`, `matched_rule="working"`, "observed_at=1970-01-01T00:00:30Z", "observation_available=true", "title_available=true", `title_source="zellij_pane_title"`, "title_used_for_detection=true", "title_observed_at=1970-01-01T00:00:29Z", `title="Title\x1b[31m"`, "progress_available=false", "pending_idle=true idle_confirmations=2", "fallback=true", `truncated=true evidence="Title\x1b[31m\n"`} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("output missing %q: %s", want, stdout.String())
			}
		}
		if strings.Contains(stdout.String(), "\x1b") {
			t.Fatalf("unescaped terminal control in %q", stdout.String())
		}
	}
}

func TestRunExplainValidatesArgumentsBeforeCreatingClient(t *testing.T) {
	for _, args := range [][]string{{"explain"}, {"explain", " "}, {"explain", "a", "b"}, {"explain", "--timeout", "0", "a"}, {"explain", "--unknown", "a"}} {
		var stderr bytes.Buffer
		code := Run(args, nil, io.Discard, &stderr, func(string, time.Duration) AgentClient {
			t.Fatal("created a client for invalid arguments")
			return nil
		}, Config{})
		if code != 2 || stderr.Len() == 0 {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

func TestRunExplainUnavailableAndFailures(t *testing.T) {
	for _, client := range []AgentClient{
		nil,
		(*explanationClient)(nil),
		&testClient{},
		&explanationClient{testClient: &testClient{}, err: errors.New("observation failed")},
	} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"explain", "agent-1"}, nil, &stdout, &stderr, func(string, time.Duration) AgentClient { return client }, Config{})
		if code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("client=%T code=%d stdout=%q stderr=%q", client, code, stdout.String(), stderr.String())
		}
	}
}

func TestExplainWithoutObservationDoesNotInventEvidence(t *testing.T) {
	var output bytes.Buffer
	if err := printAgentExplanation(&output, transport.ExplainAgentResponse{AgentID: "agent-1", State: codingagent.StateWorking}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "observed_at=unavailable observation_available=false") || strings.Contains(output.String(), "detection:") {
		t.Fatalf("output=%q", output.String())
	}
}
