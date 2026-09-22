// Package followup owns durable, per-agent queues of subsequent instructions.
package followup

import (
	"context"
	"errors"
	"time"
)

const (
	Queued         = "queued"
	Sending        = "sending"
	Sent           = "sent"
	NeedsAttention = "needs_attention"
	Canceled       = "canceled"
	Ready          = "ready"
	AwaitingWork   = "awaiting_work"
	Working        = "working"
)

var (
	ErrInvalid  = errors.New("invalid follow-up request")
	ErrNotFound = errors.New("follow-up not found")
	ErrConflict = errors.New("follow-up state conflict")
	// ErrNotReady guarantees that the adapter did not attempt external input.
	ErrNotReady = errors.New("follow-up target is no longer ready")
)

type Item struct {
	ID        string    `json:"id"`
	RequestID string    `json:"request_id,omitempty"`
	Text      string    `json:"text"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SentAt    time.Time `json:"sent_at,omitempty"`
}

type Queue struct {
	AgentID        string    `json:"agent_id"`
	PaneID         string    `json:"pane_id"`
	OwnershipToken string    `json:"ownership_token"`
	Paused         bool      `json:"paused"`
	Reason         string    `json:"reason,omitempty"`
	Phase          string    `json:"phase"`
	ActiveItemID   string    `json:"active_item_id,omitempty"`
	Items          []Item    `json:"items"`
	UpdatedAt      time.Time `json:"updated_at"`
	AttemptedAt    time.Time `json:"attempted_at,omitempty"`
	// These describe the last delivery attempt, never restart-ready observations.
	BaselineRevision uint64 `json:"baseline_revision,omitempty"`
	ObservationEpoch uint64 `json:"observation_epoch,omitempty"`
}

func (q Queue) PendingCount() int {
	n := 0
	for _, item := range q.Items {
		if item.State == Queued || item.State == Sending || item.State == NeedsAttention {
			n++
		}
	}
	return n
}

type Update struct {
	Action    string `json:"action"`
	ItemID    string `json:"item_id,omitempty"`
	Text      string `json:"text,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// Observation is obtained through the runtime and its coding-agent monitor.
// Epoch and revision are transient and only comparable within one monitor epoch.
type Observation struct {
	AgentID, PaneID, OwnershipToken string
	Reason                          string
	State                           string
	Running, Ready                  bool
	Epoch, WorkingRevision          uint64
	ObservedAt, LastWorkingAt       time.Time
}

type Repository interface {
	Load() ([]Queue, error)
	Save(context.Context, Queue) error
}

type Options struct {
	Repository   Repository
	Observe      func(context.Context, string) (Observation, error)
	Send         func(context.Context, Observation, string) error
	Now          func() time.Time
	NewID        func() string
	StartTimeout time.Duration
	Interval     time.Duration
}
