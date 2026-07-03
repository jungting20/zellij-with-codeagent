package debatebackground

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zellij-with-codeagent/internal/debate"
)

func TestRunUsesStdoutRunner(t *testing.T) {
	runner := &fakeBackgroundRunner{}
	restore := SetBackgroundRunnerForTesting(runner)
	defer restore()
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--topic", "background test",
		"--agents", "agy,codex",
		"--cwd", "/repo",
		"--agent-timeout", "1s",
		"--timeout", "5s",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(runner.requestsFor("agy")) != 1 || len(runner.requestsFor("codex")) != 1 || len(runner.requestsFor("debate-coordinator")) != 1 {
		t.Fatalf("runner requests = %#v, want agy, codex, coordinator", runner.requests)
	}
	output := stdout.String()
	for _, want := range []string{
		"debate request=",
		"agents=agy,codex",
		"[round 1 debate-agy]",
		"background answer from agy",
		"[debate-coordinator synthesis]",
		"background synthesis",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
}

func TestRunHelpPrintsUsageToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: zellij-agent debate-background") ||
		!strings.Contains(stdout.String(), "-topic string") ||
		!strings.Contains(stdout.String(), `-output string`) ||
		!strings.Contains(stdout.String(), `(default "/tmp")`) {
		t.Fatalf("stdout = %q, want debate-background usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSavesPrintedResultBeforeFinalOutput(t *testing.T) {
	runner := &fakeBackgroundRunner{}
	restore := SetBackgroundRunnerForTesting(runner)
	defer restore()
	outputDir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--topic", "save result",
		"--agents", "agy,codex",
		"--cwd", "/repo",
		"--agent-timeout", "1s",
		"--timeout", "5s",
		"--output", outputDir,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	notice := "saved debate output to "
	resultHeader := "debate request="
	noticeIndex := strings.Index(output, notice)
	resultIndex := strings.Index(output, resultHeader)
	if noticeIndex == -1 || resultIndex == -1 || noticeIndex > resultIndex {
		t.Fatalf("stdout = %q, want save notice before final result", output)
	}
	savedPath := strings.TrimSpace(output[noticeIndex+len(notice) : strings.Index(output[noticeIndex:], "\n")+noticeIndex])
	if filepath.Dir(savedPath) != outputDir {
		t.Fatalf("saved path = %q, want file under %q", savedPath, outputDir)
	}
	saved, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read saved output: %v", err)
	}
	for _, want := range []string{
		"debate request=",
		"[round 1 debate-agy]",
		"background answer from agy",
		"[debate-coordinator synthesis]",
		"background synthesis",
	} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("saved output = %q, missing %q", string(saved), want)
		}
	}
}

func TestRunStartsCodexWithPrintedResult(t *testing.T) {
	runner := &fakeBackgroundRunner{}
	restoreRunner := SetBackgroundRunnerForTesting(runner)
	defer restoreRunner()
	starter := &fakeCodexStarter{}
	restoreStarter := SetCodexStarterForTesting(starter)
	defer restoreStarter()
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--topic", "codex follow up",
		"--agents", "agy,codex",
		"--cwd", "/repo",
		"--agent-timeout", "1s",
		"--timeout", "5s",
		"--start-codex",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(starter.requests) != 1 {
		t.Fatalf("codex start requests = %#v, want one", starter.requests)
	}
	req := starter.requests[0]
	if len(req.Command) < 4 || req.Command[0] != "codex" || req.Command[1] != "--cd" || req.Command[2] != "/repo" {
		t.Fatalf("codex command = %#v, want codex --cd /repo <prompt>", req.Command)
	}
	if len(req.Command) < 6 || req.Command[3] != "--add-dir" || req.PromptFile == "" {
		t.Fatalf("codex command = %#v promptFile=%q, want --add-dir and prompt file", req.Command, req.PromptFile)
	}
	if strings.Contains(req.Command[len(req.Command)-1], req.InitialPrompt) {
		t.Fatalf("codex command prompt includes full debate output")
	}
	if !strings.HasPrefix(req.Command[len(req.Command)-1], "토론결과를 각 주장별로 요약해줘") {
		t.Fatalf("codex command prompt = %q, want summary instruction prefix", req.Command[len(req.Command)-1])
	}
	if !strings.Contains(req.Command[len(req.Command)-1], req.PromptFile) {
		t.Fatalf("codex command prompt = %q, want prompt file path %q", req.Command[len(req.Command)-1], req.PromptFile)
	}
	for _, want := range []string{
		"debate request=",
		"agents=agy,codex",
		"[round 1 debate-agy]",
		"background answer from agy",
		"[debate-coordinator synthesis]",
		"background synthesis",
	} {
		if !strings.Contains(req.InitialPrompt, want) {
			t.Fatalf("initial prompt = %q, missing %q", req.InitialPrompt, want)
		}
	}
	if !strings.Contains(stdout.String(), "[debate-background codex]") {
		t.Fatalf("stdout = %q, want codex start notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), "saved debate output to ") {
		t.Fatalf("stdout = %q, want prompt file notice", stdout.String())
	}
}

func TestRunPrintsRoundProgress(t *testing.T) {
	runner := &fakeBackgroundRunner{}
	restore := SetBackgroundRunnerForTesting(runner)
	defer restore()
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--topic", "progress test",
		"--agents", "agy,codex",
		"--rounds", "2",
		"--cwd", "/repo",
		"--agent-timeout", "1s",
		"--timeout", "5s",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"[debate progress] round=1/2 status=started agents=agy,codex",
		"[debate progress] round=1/2 status=done",
		"[debate progress] round=2/2 status=started agents=agy,codex",
		"[debate progress] round=2/2 status=done",
		"[debate progress] coordinator status=started",
		"[debate progress] coordinator status=done",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
}

type fakeBackgroundRunner struct {
	requests []debate.BackgroundCommandRequest
}

type fakeCodexStarter struct {
	requests []CodexStartRequest
}

func (r *fakeBackgroundRunner) Run(_ context.Context, req debate.BackgroundCommandRequest) (debate.BackgroundCommandResult, error) {
	r.requests = append(r.requests, req)
	if req.AgentID == "debate-coordinator" {
		return debate.BackgroundCommandResult{Stdout: "background synthesis"}, nil
	}
	return debate.BackgroundCommandResult{Stdout: "background answer from " + req.AgentID}, nil
}

func (r *fakeBackgroundRunner) requestsFor(agentID string) []debate.BackgroundCommandRequest {
	var requests []debate.BackgroundCommandRequest
	for _, req := range r.requests {
		if req.AgentID == agentID {
			requests = append(requests, req)
		}
	}
	return requests
}

func (s *fakeCodexStarter) Start(_ context.Context, req CodexStartRequest) error {
	s.requests = append(s.requests, req)
	return nil
}
