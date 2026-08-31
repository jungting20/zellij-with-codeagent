package ticketworker

import (
	"errors"
	"time"
)

type Status string

const (
	StatusReady      Status = "ready"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

type Action string

const (
	ActionStart  Action = "start"
	ActionDone   Action = "done"
	ActionCancel Action = "cancel"
	ActionReopen Action = "reopen"
)

var (
	ErrNotFound              = errors.New("ticket not found")
	ErrDuplicatePlan         = errors.New("implementation plan already registered")
	ErrEmptyQueue            = errors.New("no ready tickets")
	ErrInvalidStatus         = errors.New("invalid ticket status")
	ErrInvalidTransition     = errors.New("invalid ticket status transition")
	ErrInvalidArtifact       = errors.New("invalid ticket artifact")
	ErrInvalidPrompt         = errors.New("invalid ticket prompt")
	ErrInvalidWorktreeBranch = errors.New("invalid worktree branch")
	ErrInvalidAgent          = errors.New("invalid ticket agent")
	ErrNotInitialized        = errors.New("ticket-worker is not initialized; run zellij-agent ticket-worker init")
)

type Ticket struct {
	ID             int64      `json:"id"`
	Title          string     `json:"title"`
	Summary        string     `json:"summary"`
	SpecPath       string     `json:"spec_path"`
	PlanPath       string     `json:"plan_path"`
	WorktreeBranch string     `json:"worktree_branch"`
	Agent          string     `json:"agent"`
	Prompt         string     `json:"prompt"`
	Status         Status     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CancelledAt    *time.Time `json:"cancelled_at"`
}

type CreateInput struct {
	Title          string
	Summary        string
	SpecPath       string
	PlanPath       string
	WorktreeBranch string
	Agent          string
	Prompt         string
}
