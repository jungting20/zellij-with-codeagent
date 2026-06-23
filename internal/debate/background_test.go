package debate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunBackgroundDefaultsUseStdoutCommands(t *testing.T) {
	runner := &recordingBackgroundRunner{}

	result, err := RunBackground(context.Background(), BackgroundOptions{
		Topic:        "default command test",
		Agents:       ParseAgents("agy,agent,codex"),
		Rounds:       1,
		AgentTimeout: time.Second,
		CWD:          "/repo",
		Runner:       runner,
	})
	if err != nil {
		t.Fatalf("RunBackground() error = %v", err)
	}

	if got, want := runner.commandsFor("agy")[0], []string{"agy", "--dangerously-skip-permissions", "--print"}; !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("agy command prefix = %#v, want %#v", got, want)
	}
	if prompt := runner.commandsFor("agy")[0][len(runner.commandsFor("agy")[0])-1]; !strings.Contains(prompt, "Round: 1") || !strings.Contains(prompt, "Topic: default command test") {
		t.Fatalf("agy prompt arg = %q, want round prompt", prompt)
	}
	if got, want := runner.commandsFor("agent")[0], []string{"agent", "--yolo", "--print", "--model", "claude-opus-4-8-thinking-high", "--trust"}; !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("agent command prefix = %#v, want %#v", got, want)
	}
	codexReq := runner.requestsFor("codex")[0]
	if got, want := codexReq.Command, []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--cd", "/repo", "-"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codex command = %#v, want %#v", got, want)
	}
	if !strings.Contains(codexReq.Stdin, "Round: 1") || !strings.Contains(codexReq.Stdin, "Agent: codex") {
		t.Fatalf("codex stdin = %q, want round prompt", codexReq.Stdin)
	}
	coordinatorReqs := runner.requestsFor("debate-coordinator")
	if len(coordinatorReqs) != 1 {
		t.Fatalf("coordinator requests = %#v, want one", coordinatorReqs)
	}
	if got, want := coordinatorReqs[0].Command, []string{"codex", "exec", "--cd", "/repo", "-"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("coordinator command = %#v, want %#v", got, want)
	}
	if !strings.Contains(coordinatorReqs[0].Stdin, "[round 1 debate-agy]") || !strings.Contains(coordinatorReqs[0].Stdin, "answer from agy") {
		t.Fatalf("coordinator stdin = %q, want synthesis block with agent outputs", coordinatorReqs[0].Stdin)
	}
	if result.coordinatorOutput != "final synthesis" {
		t.Fatalf("coordinator output = %q, want final synthesis", result.coordinatorOutput)
	}
}

func TestRunBackgroundRejectsConfigAgentWithoutPrintCommand(t *testing.T) {
	configPath := writeTempDebateConfig(t, `agents:
  - id: custom
    command: ["custom-agent"]
`)

	_, err := RunBackground(context.Background(), BackgroundOptions{
		Topic:        "missing print command",
		Agents:       ParseAgents("custom"),
		Rounds:       1,
		AgentTimeout: time.Second,
		ConfigPath:   configPath,
		CWD:          "/repo",
		Runner:       &recordingBackgroundRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "print_command") {
		t.Fatalf("RunBackground() error = %v, want print_command validation", err)
	}
}

func TestRunBackgroundLoadsConfigPrintCommand(t *testing.T) {
	configPath := writeTempDebateConfig(t, `agents:
  - id: custom
    command: ["custom-agent"]
    print_command: ["custom-agent", "--print"]
    prompt_delivery: arg
`)
	runner := &recordingBackgroundRunner{}

	_, err := RunBackground(context.Background(), BackgroundOptions{
		Topic:        "custom print command",
		Agents:       ParseAgents("ignored"),
		Rounds:       1,
		AgentTimeout: time.Second,
		ConfigPath:   configPath,
		CWD:          "/repo",
		Runner:       runner,
	})
	if err != nil {
		t.Fatalf("RunBackground() error = %v", err)
	}

	requests := runner.requestsFor("custom")
	if len(requests) != 1 {
		t.Fatalf("custom requests = %#v, want one", requests)
	}
	if got, want := requests[0].Command[:2], []string{"custom-agent", "--print"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("custom command prefix = %#v, want %#v", got, want)
	}
	if requests[0].Stdin != "" {
		t.Fatalf("custom stdin = %q, want prompt delivered as arg", requests[0].Stdin)
	}
	if prompt := requests[0].Command[len(requests[0].Command)-1]; !strings.Contains(prompt, "Topic: custom print command") {
		t.Fatalf("custom prompt arg = %q, want debate prompt", prompt)
	}
}

func TestRunBackgroundRecordsFailedAgentAndContinuesSynthesis(t *testing.T) {
	runner := &recordingBackgroundRunner{
		errByAgent: map[string]error{
			"agent": errors.New("exit status 2"),
		},
		stderrByAgent: map[string]string{
			"agent": "auth failed",
		},
	}

	result, err := RunBackground(context.Background(), BackgroundOptions{
		Topic:        "partial failure",
		Agents:       ParseAgents("agy,agent"),
		Rounds:       1,
		AgentTimeout: time.Second,
		CWD:          "/repo",
		Runner:       runner,
	})
	if err != nil {
		t.Fatalf("RunBackground() error = %v", err)
	}

	failedStatus := result.roundOutputs[0].Statuses["debate-agent"]
	if failedStatus.Status != paneStatusFailed {
		t.Fatalf("status = %q, want %q", failedStatus.Status, paneStatusFailed)
	}
	coordinatorStdin := runner.requestsFor("debate-coordinator")[0].Stdin
	if !strings.Contains(coordinatorStdin, "[round 1 debate-agent] status=failed") || !strings.Contains(coordinatorStdin, "auth failed") {
		t.Fatalf("coordinator stdin = %q, want failed agent output", coordinatorStdin)
	}
}

type recordingBackgroundRunner struct {
	mu            sync.Mutex
	requests      []BackgroundCommandRequest
	errByAgent    map[string]error
	stderrByAgent map[string]string
}

func (r *recordingBackgroundRunner) Run(_ context.Context, req BackgroundCommandRequest) (BackgroundCommandResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	stdout := "answer from " + req.AgentID
	if req.AgentID == "debate-coordinator" {
		stdout = "final synthesis"
	}
	stderr := ""
	if r.stderrByAgent != nil {
		stderr = r.stderrByAgent[req.AgentID]
	}
	err := error(nil)
	if r.errByAgent != nil {
		err = r.errByAgent[req.AgentID]
	}
	return BackgroundCommandResult{Stdout: stdout, Stderr: stderr}, err
}

func (r *recordingBackgroundRunner) requestsFor(agentID string) []BackgroundCommandRequest {
	var requests []BackgroundCommandRequest
	for _, req := range r.requests {
		if req.AgentID == agentID {
			requests = append(requests, req)
		}
	}
	return requests
}

func (r *recordingBackgroundRunner) commandsFor(agentID string) [][]string {
	requests := r.requestsFor(agentID)
	commands := make([][]string, 0, len(requests))
	for _, req := range requests {
		commands = append(commands, req.Command)
	}
	return commands
}

func writeTempDebateConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "debate.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return path
}
