package backgrounddebate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"zellij-with-codeagent/internal/debaterole"
)

type recordingRunner struct {
	requests []RoleRequest
	failRole string
}

type roleRunnerFunc func(context.Context, RoleRequest) (debaterole.Result, error)

func (fn roleRunnerFunc) Run(ctx context.Context, req RoleRequest) (debaterole.Result, error) {
	return fn(ctx, req)
}

func (r *recordingRunner) Run(_ context.Context, req RoleRequest) (debaterole.Result, error) {
	r.requests = append(r.requests, req)
	if req.Role.Name == r.failRole {
		exitCode := 7
		return debaterole.Result{}, &RunError{Kind: FailureExecution, Message: "role exited", ExitCode: &exitCode}
	}
	round := 1
	if len(r.requests) > 3 {
		round = 2
	}
	contentPrefix := map[string]string{
		Proposer.Name: "proposal-",
		Critic.Name:   "critique-",
		Judge.Name:    "judgment-",
	}[req.Role.Name]
	return debaterole.Result{
		SchemaVersion: debaterole.SchemaVersion,
		Role:          req.Role.Name,
		Engine:        req.Role.Engine,
		Status:        StatusSuccess,
		Content:       contentPrefix + string(rune('0'+round)),
	}, nil
}

func TestRunExecutesRolesInOrderAndScopesPrompts(t *testing.T) {
	runner := &recordingRunner{}
	result := Run(context.Background(), runner, Options{
		Topic:        "Should we ship?",
		Repository:   "/repo",
		Rounds:       2,
		AgentTimeout: time.Second,
	})

	var got []string
	for _, req := range runner.requests {
		got = append(got, req.Role.Name)
	}
	want := []string{"debate-proposer", "debate-critic", "debate-judge", "debate-proposer", "debate-critic", "debate-judge"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("role order = %v, want %v", got, want)
	}
	if !strings.Contains(runner.requests[3].Prompt, "judgment-1") {
		t.Fatalf("second proposer prompt does not contain previous judgment: %q", runner.requests[3].Prompt)
	}
	if strings.Contains(runner.requests[4].Prompt, "proposal-1") {
		t.Fatalf("second critic prompt contains prior proposal: %q", runner.requests[4].Prompt)
	}
	if result.Status != StatusSuccess || result.RoundsCompleted != 2 || result.FinalContent != "judgment-2" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRunReportsCompletedContentCharacterCounts(t *testing.T) {
	runner := &recordingRunner{}
	var events []ProgressEvent
	result := Run(context.Background(), runner, Options{
		Topic: "topic", Repository: "/repo", Rounds: 1, AgentTimeout: time.Second,
		Progress: func(event ProgressEvent) { events = append(events, event) },
	})
	if result.Status != StatusSuccess {
		t.Fatalf("Run() status = %q, want success", result.Status)
	}
	var completed []ProgressEvent
	for _, event := range events {
		if event.Status == "completed" {
			completed = append(completed, event)
		}
	}
	want := []ProgressEvent{
		{Round: 1, Rounds: 1, Role: Proposer.Name, Status: "completed", ContentChars: len([]rune("proposal-1"))},
		{Round: 1, Rounds: 1, Role: Critic.Name, Status: "completed", ContentChars: len([]rune("critique-1"))},
		{Round: 1, Rounds: 1, Role: Judge.Name, Status: "completed", ContentChars: len([]rune("judgment-1"))},
	}
	if !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed events = %#v, want %#v", completed, want)
	}
}

func TestRunRejectsInvalidOptionsBeforeCallingRunner(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{name: "blank topic", opts: Options{Topic: "  ", Rounds: 1, AgentTimeout: time.Second}},
		{name: "zero rounds", opts: Options{Topic: "topic", Rounds: 0, AgentTimeout: time.Second}},
		{name: "four rounds", opts: Options{Topic: "topic", Rounds: 4, AgentTimeout: time.Second}},
		{name: "zero timeout", opts: Options{Topic: "topic", Rounds: 1, AgentTimeout: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingRunner{}
			result := Run(context.Background(), runner, tt.opts)
			if len(runner.requests) != 0 {
				t.Fatalf("runner calls = %d, want 0", len(runner.requests))
			}
			if result.Status != StatusFailed || result.Failure == nil || result.Failure.Kind != FailureValidation {
				t.Fatalf("unexpected result: %#v", result)
			}
		})
	}
}

func TestRunStopsAfterRoleExecutionFailureAndKeepsPartialRound(t *testing.T) {
	runner := &recordingRunner{failRole: Critic.Name}
	result := Run(context.Background(), runner, Options{
		Topic:        "Should we ship?",
		Repository:   "/repo",
		Rounds:       2,
		AgentTimeout: time.Second,
	})

	if len(runner.requests) != 2 {
		t.Fatalf("runner calls = %d, want 2", len(runner.requests))
	}
	if result.RoundsCompleted != 0 || len(result.Rounds) != 1 {
		t.Fatalf("unexpected round counts: %#v", result)
	}
	if result.Rounds[0].Proposer == nil || result.Rounds[0].Critic != nil || result.Rounds[0].Judge != nil {
		t.Fatalf("unexpected partial round: %#v", result.Rounds[0])
	}
	if result.Failure == nil || result.Failure.Round != 1 || result.Failure.Role != Critic.Name || result.Failure.Kind != FailureExecution {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
	if result.Failure.ExitCode == nil || *result.Failure.ExitCode != 7 {
		t.Fatalf("unexpected exit code: %#v", result.Failure.ExitCode)
	}
}

func TestRunMapsUnknownRunnerErrorToExecutionFailure(t *testing.T) {
	runner := roleRunnerFunc(func(context.Context, RoleRequest) (debaterole.Result, error) {
		return debaterole.Result{}, errors.New("runner broke")
	})
	result := Run(context.Background(), runner, Options{Topic: "topic", Rounds: 1, AgentTimeout: time.Second})

	if result.Failure == nil || result.Failure.Kind != FailureExecution {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
}

func TestRunStoresReturnedResultBeforeContractFailure(t *testing.T) {
	runner := roleRunnerFunc(func(_ context.Context, req RoleRequest) (debaterole.Result, error) {
		return debaterole.Result{
			SchemaVersion: "wrong-schema",
			Role:          req.Role.Name,
			Engine:        req.Role.Engine,
			Status:        StatusSuccess,
			Content:       "proposal",
		}, nil
	})
	result := Run(context.Background(), runner, Options{Topic: "topic", Rounds: 1, AgentTimeout: time.Second})

	if result.Failure == nil || result.Failure.Kind != FailureContract {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
	if len(result.Rounds) != 1 || result.Rounds[0].Proposer == nil {
		t.Fatalf("returned proposer result was not stored: %#v", result.Rounds)
	}
}
