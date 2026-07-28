package codingagent

import (
	"time"

	"zellij-with-codeagent/internal/runtime"
)

type ID string

type State string

const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateUnknown State = "unknown"
)

type Record struct {
	ID             ID
	Kind           Kind
	PaneID         runtime.PaneID
	State          State
	StateReason    string
	MatchedRule    string
	CreatedAt      time.Time
	StateChangedAt time.Time
}

type StateUpdate struct {
	State       State
	Reason      string
	MatchedRule string
}

type StateChange struct {
	Previous Record
	Current  Record
	Changed  bool
}

type Store interface {
	Create(Record) (Record, error)
	Get(ID) (Record, error)
	GetByPane(runtime.PaneID) (Record, error)
	List() ([]Record, error)
	UpdateState(ID, StateUpdate) (StateChange, error)
	Delete(ID) error
}
