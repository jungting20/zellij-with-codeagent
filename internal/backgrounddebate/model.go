package backgrounddebate

import (
	"context"
	"time"

	"zellij-with-codeagent/internal/debaterole"
)

const SchemaVersion = "debate-background/v1"
const StatusSuccess = "success"
const StatusFailed = "failed"
const FailureValidation = "validation_failed"
const FailureTimeout = "timeout"
const FailureExecution = "role_execution_failed"
const FailureMalformed = "malformed_role_output"
const FailureContract = "role_contract_mismatch"
const FailureEmpty = "empty_role_content"
const FailurePersistence = "persistence_failed"

type RoleSpec struct {
	Name   string
	Engine string
}

var Proposer = RoleSpec{Name: "debate-proposer", Engine: "agy"}
var Critic = RoleSpec{Name: "debate-critic", Engine: "agent"}
var Judge = RoleSpec{Name: "debate-judge", Engine: "codex"}

type RoleRequest struct {
	Role       RoleSpec
	Repository string
	Prompt     string
	Timeout    time.Duration
}

type RoleRunner interface {
	Run(context.Context, RoleRequest) (debaterole.Result, error)
}

type Options struct {
	Topic        string
	Repository   string
	Rounds       int
	AgentTimeout time.Duration
	Progress     func(ProgressEvent)
}

type ProgressEvent struct {
	Round  int
	Rounds int
	Role   string
	Status string
}

type RoundResult struct {
	Round    int                `json:"round"`
	Proposer *debaterole.Result `json:"proposer,omitempty"`
	Critic   *debaterole.Result `json:"critic,omitempty"`
	Judge    *debaterole.Result `json:"judge,omitempty"`
}

type Failure struct {
	Round    int    `json:"round,omitempty"`
	Role     string `json:"role,omitempty"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

type Result struct {
	SchemaVersion   string        `json:"schema_version"`
	Status          string        `json:"status"`
	Topic           string        `json:"topic"`
	RoundsRequested int           `json:"rounds_requested"`
	RoundsCompleted int           `json:"rounds_completed"`
	Rounds          []RoundResult `json:"rounds"`
	FinalContent    string        `json:"final_content,omitempty"`
	OutputPath      string        `json:"output_path,omitempty"`
	Failure         *Failure      `json:"failure,omitempty"`
}

type RunError struct {
	Kind     string
	Message  string
	ExitCode *int
}

func (e *RunError) Error() string { return e.Message }
