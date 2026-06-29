package codereview

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	debatebg "zellij-with-codeagent/internal/cli/debatebackground"
	"zellij-with-codeagent/internal/debate"
)

func TestRunUsesFixedReviewTopicAndStartsCodexByDefault(t *testing.T) {
	runner := &fakeBackgroundRunner{}
	restoreRunner := debatebg.SetBackgroundRunnerForTesting(runner)
	defer restoreRunner()
	starter := &fakeCodexStarter{}
	restoreStarter := debatebg.SetCodexStarterForTesting(starter)
	defer restoreStarter()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--rounds", "2"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got := len(runner.requestsFor("agy")); got != 2 {
		t.Fatalf("agy requests = %d, want 2 rounds", got)
	}
	firstPrompt := runner.requestsFor("codex")[0].Stdin
	if !strings.Contains(firstPrompt, ReviewTopic) {
		t.Fatalf("first codex prompt = %q, want fixed review topic", firstPrompt)
	}
	if strings.Contains(firstPrompt, "--topic") {
		t.Fatalf("first codex prompt = %q, want topic text not CLI flag text", firstPrompt)
	}
	if len(starter.requests) != 1 {
		t.Fatalf("codex start requests = %#v, want one", starter.requests)
	}
	if starter.requests[0].PromptFile == "" || !strings.Contains(stdout.String(), "[debate-background codex]") {
		t.Fatalf("stdout = %q promptFile=%q, want automatic codex start", stdout.String(), starter.requests[0].PromptFile)
	}
}

func TestRunAddsOptionalPromptToReviewTopic(t *testing.T) {
	runner := &fakeBackgroundRunner{}
	restoreRunner := debatebg.SetBackgroundRunnerForTesting(runner)
	defer restoreRunner()
	starter := &fakeCodexStarter{}
	restoreStarter := debatebg.SetCodexStarterForTesting(starter)
	defer restoreStarter()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--prompt", "Pay extra attention to CLI compatibility."}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	firstPrompt := runner.requestsFor("codex")[0].Stdin
	if !strings.Contains(firstPrompt, ReviewTopic) {
		t.Fatalf("first codex prompt = %q, want fixed review topic", firstPrompt)
	}
	if !strings.Contains(firstPrompt, "Pay extra attention to CLI compatibility.") {
		t.Fatalf("first codex prompt = %q, want extra prompt", firstPrompt)
	}
}

func TestRunHelpShowsReviewOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "Usage: zellij-agent code-review [options]") || !strings.Contains(output, "-rounds int") || !strings.Contains(output, "-prompt string") {
		t.Fatalf("stdout = %q, want code-review usage with prompt and rounds", output)
	}
	if strings.Contains(output, "-topic") || strings.Contains(output, "-start-codex") {
		t.Fatalf("stdout = %q, want only code-review options", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

type fakeBackgroundRunner struct {
	mu       sync.Mutex
	requests []debate.BackgroundCommandRequest
}

type fakeCodexStarter struct {
	requests []debatebg.CodexStartRequest
}

func (r *fakeBackgroundRunner) Run(_ context.Context, req debate.BackgroundCommandRequest) (debate.BackgroundCommandResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	if req.AgentID == "debate-coordinator" {
		return debate.BackgroundCommandResult{Stdout: "background synthesis"}, nil
	}
	return debate.BackgroundCommandResult{Stdout: "background answer from " + req.AgentID}, nil
}

func (r *fakeBackgroundRunner) requestsFor(agentID string) []debate.BackgroundCommandRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var requests []debate.BackgroundCommandRequest
	for _, req := range r.requests {
		if req.AgentID == agentID {
			requests = append(requests, req)
		}
	}
	return requests
}

func (s *fakeCodexStarter) Start(_ context.Context, req debatebg.CodexStartRequest) error {
	s.requests = append(s.requests, req)
	return nil
}
