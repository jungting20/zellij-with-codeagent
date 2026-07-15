package backgrounddebate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"zellij-with-codeagent/internal/debaterole"
)

func Run(ctx context.Context, runner RoleRunner, opts Options) Result {
	result := Result{
		SchemaVersion:   SchemaVersion,
		Status:          StatusFailed,
		Topic:           opts.Topic,
		RoundsRequested: opts.Rounds,
		Rounds:          make([]RoundResult, 0),
	}
	if message := validateOptions(runner, opts); message != "" {
		result.Failure = &Failure{Kind: FailureValidation, Message: message}
		return result
	}

	previousJudgment := ""
	for round := 1; round <= opts.Rounds; round++ {
		result.Rounds = append(result.Rounds, RoundResult{Round: round})
		current := &result.Rounds[len(result.Rounds)-1]

		proposal, failure := runRole(ctx, runner, opts, round, Proposer, proposerPrompt(opts.Topic, previousJudgment))
		if failure != nil {
			result.Failure = failure
			return result
		}
		current.Proposer = &proposal
		if failure := finishRole(opts, round, Proposer, proposal); failure != nil {
			result.Failure = failure
			return result
		}

		critique, failure := runRole(ctx, runner, opts, round, Critic, criticPrompt(opts.Topic, proposal.Content))
		if failure != nil {
			result.Failure = failure
			return result
		}
		current.Critic = &critique
		if failure := finishRole(opts, round, Critic, critique); failure != nil {
			result.Failure = failure
			return result
		}

		judgment, failure := runRole(ctx, runner, opts, round, Judge, judgePrompt(opts.Topic, proposal.Content, critique.Content))
		if failure != nil {
			result.Failure = failure
			return result
		}
		current.Judge = &judgment
		if failure := finishRole(opts, round, Judge, judgment); failure != nil {
			result.Failure = failure
			return result
		}
		result.RoundsCompleted++
		result.FinalContent = judgment.Content
		previousJudgment = judgment.Content
	}

	result.Status = StatusSuccess
	return result
}

func validateOptions(runner RoleRunner, opts Options) string {
	if strings.TrimSpace(opts.Topic) == "" {
		return "topic is required"
	}
	if opts.Rounds < 1 || opts.Rounds > 3 {
		return "rounds must be between 1 and 3"
	}
	if opts.AgentTimeout <= 0 {
		return "agent timeout must be greater than zero"
	}
	if runner == nil {
		return "role runner is required"
	}
	return ""
}

func runRole(ctx context.Context, runner RoleRunner, opts Options, round int, role RoleSpec, prompt string) (debaterole.Result, *Failure) {
	progress(opts.Progress, ProgressEvent{Round: round, Rounds: opts.Rounds, Role: role.Name, Status: "started"})
	roleCtx, cancel := context.WithTimeout(ctx, opts.AgentTimeout)
	defer cancel()
	roleResult, err := runner.Run(roleCtx, RoleRequest{
		Role:       role,
		Repository: opts.Repository,
		Prompt:     prompt,
		Timeout:    opts.AgentTimeout,
	})
	if err != nil {
		failure := failureFromError(roleCtx, round, role.Name, err)
		progress(opts.Progress, ProgressEvent{Round: round, Rounds: opts.Rounds, Role: role.Name, Status: "failed"})
		return debaterole.Result{}, failure
	}
	return roleResult, nil
}

func finishRole(opts Options, round int, role RoleSpec, roleResult debaterole.Result) *Failure {
	if failure := validateRoleResult(round, role, roleResult); failure != nil {
		progress(opts.Progress, ProgressEvent{Round: round, Rounds: opts.Rounds, Role: role.Name, Status: "failed"})
		return failure
	}
	progress(opts.Progress, ProgressEvent{Round: round, Rounds: opts.Rounds, Role: role.Name, Status: "completed"})
	return nil
}

func validateRoleResult(round int, role RoleSpec, result debaterole.Result) *Failure {
	if result.SchemaVersion != debaterole.SchemaVersion {
		return roleFailure(round, role.Name, FailureContract, fmt.Sprintf("expected schema %q, got %q", debaterole.SchemaVersion, result.SchemaVersion), nil)
	}
	if result.Role != role.Name {
		return roleFailure(round, role.Name, FailureContract, fmt.Sprintf("expected role %q, got %q", role.Name, result.Role), nil)
	}
	if result.Engine != role.Engine {
		return roleFailure(round, role.Name, FailureContract, fmt.Sprintf("expected engine %q, got %q", role.Engine, result.Engine), nil)
	}
	if result.Status != StatusSuccess {
		return roleFailure(round, role.Name, FailureContract, fmt.Sprintf("expected status %q, got %q", StatusSuccess, result.Status), nil)
	}
	if strings.TrimSpace(result.Content) == "" {
		return roleFailure(round, role.Name, FailureEmpty, "role returned empty content", nil)
	}
	return nil
}

func failureFromError(roleCtx context.Context, round int, role string, err error) *Failure {
	var runErr *RunError
	if errors.As(err, &runErr) {
		return roleFailure(round, role, runErr.Kind, runErr.Message, runErr.ExitCode)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || roleCtx.Err() != nil {
		return roleFailure(round, role, FailureTimeout, err.Error(), nil)
	}
	return roleFailure(round, role, FailureExecution, err.Error(), nil)
}

func roleFailure(round int, role, kind, message string, exitCode *int) *Failure {
	return &Failure{Round: round, Role: role, Kind: kind, Message: message, ExitCode: exitCode}
}

func progress(callback func(ProgressEvent), event ProgressEvent) {
	if callback != nil {
		callback(event)
	}
}
