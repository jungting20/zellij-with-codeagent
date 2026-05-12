package registry

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrAlreadyExists = errors.New("registry record already exists")
	ErrNotFound      = errors.New("registry record not found")
)

type Registry struct {
	mu             sync.RWMutex
	now            func() time.Time
	panes          map[PaneID]PaneRecord
	latestByZellij map[ZellijPaneID]PaneID
}

func New() *Registry {
	return NewWithClock(time.Now)
}

func NewWithClock(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}

	return &Registry{
		now:            now,
		panes:          make(map[PaneID]PaneRecord),
		latestByZellij: make(map[ZellijPaneID]PaneID),
	}
}

func (r *Registry) RegisterPane(req RegisterPaneRequest) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.panes[req.ID]; ok {
		return PaneRecord{}, ErrAlreadyExists
	}

	status := req.Status
	if status == "" {
		status = PaneStatusStarting
	}

	now := r.now()
	record := PaneRecord{
		ID:           req.ID,
		TaskID:       req.TaskID,
		AgentID:      req.AgentID,
		ZellijPaneID: req.ZellijPaneID,
		Role:         req.Role,
		Command:      cloneStrings(req.Command),
		CWD:          req.CWD,
		Status:       status,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	r.panes[record.ID] = record
	if record.ZellijPaneID != "" {
		r.latestByZellij[record.ZellijPaneID] = record.ID
	}

	return clonePaneRecord(record), nil
}

func (r *Registry) GetPane(id PaneID) (PaneRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	record, ok := r.panes[id]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	return clonePaneRecord(record), nil
}

func (r *Registry) GetLatestByZellijPaneID(id ZellijPaneID) (PaneRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	paneID, ok := r.latestByZellij[id]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	record, ok := r.panes[paneID]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	return clonePaneRecord(record), nil
}

func (r *Registry) ListPanes() []PaneRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.panes))
	for id := range r.panes {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)

	records := make([]PaneRecord, 0, len(ids))
	for _, id := range ids {
		records = append(records, clonePaneRecord(r.panes[PaneID(id)]))
	}

	return records
}

func (r *Registry) UpdatePaneStatus(id PaneID, status PaneStatus, message string) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.panes[id]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	record.Status = status
	record.StatusMessage = message
	record.UpdatedAt = r.now()
	r.panes[id] = record

	return clonePaneRecord(record), nil
}

func (r *Registry) UpdatePaneOutput(id PaneID, output string) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.panes[id]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	record.LastOutput = output
	record.UpdatedAt = r.now()
	r.panes[id] = record

	return clonePaneRecord(record), nil
}

func (r *Registry) RemovePane(id PaneID) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.panes[id]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	delete(r.panes, id)
	if record.ZellijPaneID != "" && r.latestByZellij[record.ZellijPaneID] == id {
		delete(r.latestByZellij, record.ZellijPaneID)
	}

	return clonePaneRecord(record), nil
}

func clonePaneRecord(record PaneRecord) PaneRecord {
	record.Command = cloneStrings(record.Command)
	return record
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}

	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}
