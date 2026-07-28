package codingagent

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"zellij-with-codeagent/internal/runtime"
)

var (
	ErrNotFound      = errors.New("coding agent not found")
	ErrDuplicateID   = errors.New("coding agent id already exists")
	ErrDuplicatePane = errors.New("coding agent pane already exists")
	ErrInvalidRecord = errors.New("invalid coding agent record")
	ErrInvalidState  = errors.New("invalid coding agent state")
)

type memoryStore struct {
	mu     sync.RWMutex
	now    func() time.Time
	byID   map[ID]Record
	byPane map[runtime.PaneID]ID
}

func NewMemoryStore(now func() time.Time) Store {
	if now == nil {
		now = time.Now
	}
	return &memoryStore{
		now:    now,
		byID:   make(map[ID]Record),
		byPane: make(map[runtime.PaneID]ID),
	}
}

func (s *memoryStore) Create(record Record) (Record, error) {
	if err := validateRecord(record); err != nil {
		return Record{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[record.ID]; ok {
		return Record{}, fmt.Errorf("%w: %q", ErrDuplicateID, record.ID)
	}
	if _, ok := s.byPane[record.PaneID]; ok {
		return Record{}, fmt.Errorf("%w: %q", ErrDuplicatePane, record.PaneID)
	}
	s.byID[record.ID] = record
	s.byPane[record.PaneID] = record.ID
	return record, nil
}

func (s *memoryStore) Get(id ID) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.byID[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return record, nil
}

func (s *memoryStore) GetByPane(paneID runtime.PaneID) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byPane[paneID]
	if !ok {
		return Record{}, fmt.Errorf("%w: pane %q", ErrNotFound, paneID)
	}
	record := s.byID[id]
	return record, nil
}

func (s *memoryStore) List() ([]Record, error) {
	s.mu.RLock()
	records := make([]Record, 0, len(s.byID))
	for _, record := range s.byID {
		records = append(records, record)
	}
	s.mu.RUnlock()

	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	return records, nil
}

func (s *memoryStore) UpdateState(id ID, update StateUpdate) (StateChange, error) {
	if !validState(update.State) {
		return StateChange{}, fmt.Errorf("%w: %q", ErrInvalidState, update.State)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, ok := s.byID[id]
	if !ok {
		return StateChange{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}

	current := previous
	if current.State == update.State && current.StateReason == update.Reason && current.MatchedRule == update.MatchedRule {
		return StateChange{Previous: previous, Current: current}, nil
	}
	current.State = update.State
	current.StateReason = update.Reason
	current.MatchedRule = update.MatchedRule
	current.StateChangedAt = s.now()
	s.byID[id] = current
	return StateChange{Previous: previous, Current: current, Changed: true}, nil
}

func (s *memoryStore) Delete(id ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	delete(s.byID, id)
	delete(s.byPane, record.PaneID)
	return nil
}

func validateRecord(record Record) error {
	if record.ID == "" || record.Kind == "" || record.PaneID == "" {
		return fmt.Errorf("%w: id, kind, and pane id are required", ErrInvalidRecord)
	}
	if _, ok := LookupProfile(record.Kind); !ok {
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidRecord, record.Kind)
	}
	if !validState(record.State) {
		return fmt.Errorf("%w: %q", ErrInvalidState, record.State)
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StateIdle, StateWorking, StateBlocked, StateUnknown:
		return true
	default:
		return false
	}
}
