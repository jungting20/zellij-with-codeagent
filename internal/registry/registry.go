package registry

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

var (
	ErrAlreadyExists = errors.New("registry record already exists")
	ErrNotFound      = errors.New("registry record not found")
	ErrStaleRecord   = errors.New("registry record generation mismatch")
)

type paneLocation struct {
	SessionID SessionID
	TabID     TabID
}

type Registry struct {
	mu             sync.RWMutex
	now            func() time.Time
	sessions       map[SessionID]SessionRecord
	paneToLocation map[PaneID]paneLocation
	latestByZellij map[ZellijPaneID]PaneID
	nextGeneration uint64
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
		sessions:       make(map[SessionID]SessionRecord),
		paneToLocation: make(map[PaneID]paneLocation),
		latestByZellij: make(map[ZellijPaneID]PaneID),
	}
}

func (r *Registry) RegisterPane(req RegisterPaneRequest) (PaneRecord, error) {
	if err := req.Validate(); err != nil {
		return PaneRecord{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	req = applyRegisterPaneDefaults(req)

	if err := r.validatePaneUniqueLocked(req.ID); err != nil {
		return PaneRecord{}, err
	}
	if err := r.validateActiveZellijPaneUniqueLocked(req); err != nil {
		return PaneRecord{}, err
	}
	r.nextGeneration++

	now := r.now()
	record := PaneRecord{
		ID:             req.ID,
		OwnershipToken: req.OwnershipToken,
		Generation:     r.nextGeneration,
		SessionID:      req.SessionID,
		TabID:          req.TabID,
		TaskID:         req.TaskID,
		AgentID:        req.AgentID,
		ZellijPaneID:   req.ZellijPaneID,
		ZellijTabID:    cloneZellijTabID(req.ZellijTabID),
		TabName:        req.TabName,
		Role:           req.Role,
		Command:        cloneStrings(req.Command),
		CWD:            req.CWD,
		Status:         req.Status,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	session, exists := r.sessions[req.SessionID]
	if !exists {
		session = SessionRecord{
			ID:        req.SessionID,
			Tabs:      make(map[TabID]TabRecord),
			CreatedAt: now,
			UpdatedAt: now,
		}
	} else {
		session.UpdatedAt = now
	}

	tab, exists := session.Tabs[req.TabID]
	if !exists {
		tab = TabRecord{
			ID:        req.TabID,
			Name:      req.TabName,
			Panes:     make(map[PaneID]PaneRecord),
			CreatedAt: now,
			UpdatedAt: now,
		}
	} else {
		tab.UpdatedAt = now
		if tab.Name == "" && req.TabName != "" {
			tab.Name = req.TabName
		}
	}

	tab.Panes[record.ID] = record
	session.Tabs[req.TabID] = tab
	r.sessions[req.SessionID] = session

	r.paneToLocation[record.ID] = paneLocation{
		SessionID: req.SessionID,
		TabID:     req.TabID,
	}
	if record.ZellijPaneID != "" {
		r.latestByZellij[record.ZellijPaneID] = record.ID
	}

	return clonePaneRecord(record), nil
}

func (r *Registry) GetPane(id PaneID) (PaneRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, _, _, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
	}

	return clonePaneRecord(pane), nil
}

func (r *Registry) GetLatestByZellijPaneID(id ZellijPaneID) (PaneRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	paneID, ok := r.latestByZellij[id]
	if !ok {
		return PaneRecord{}, ErrNotFound
	}

	_, _, _, pane, err := r.resolvePanePathLocked(paneID)
	if err != nil {
		return PaneRecord{}, err
	}

	return clonePaneRecord(pane), nil
}

func (r *Registry) ListPanes() []PaneRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var records []PaneRecord
	for _, session := range r.sessions {
		for _, tab := range session.Tabs {
			for _, pane := range tab.Panes {
				records = append(records, clonePaneRecord(pane))
			}
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})

	return records
}

func (r *Registry) UpdatePaneStatus(id PaneID, status PaneStatus, message string) (PaneRecord, error) {
	return r.updatePaneStatusGeneration(id, 0, status, message)
}

func (r *Registry) UpdatePaneStatusGeneration(id PaneID, generation uint64, status PaneStatus, message string) (PaneRecord, error) {
	return r.updatePaneStatusGeneration(id, generation, status, message)
}

// UpdateActivePaneStatusGeneration updates a generation only while its pane
// lifecycle is non-terminal. The boolean reports whether the update was
// applied, allowing callers to own one-shot transition side effects.
func (r *Registry) UpdateActivePaneStatusGeneration(id PaneID, generation uint64, status PaneStatus, message string) (PaneRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, false, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, false, ErrStaleRecord
	}
	switch pane.Status {
	case PaneStatusClosed, PaneStatusExited, PaneStatusLost:
		return clonePaneRecord(pane), false, nil
	}

	now := r.now()
	pane.Status = status
	pane.StatusMessage = message
	pane.UpdatedAt = now
	tab.Panes[id] = pane
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	return clonePaneRecord(pane), true, nil
}

// ClaimPaneClosureGeneration atomically transitions an active pane to a
// terminal status and claims the one observer close notification for that
// generation. Already-terminal records retain their first terminal diagnosis,
// while an unclaimed record can still supply the notification owner.
func (r *Registry) ClaimPaneClosureGeneration(id PaneID, generation uint64, status PaneStatus, message string) (PaneRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, false, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, false, ErrStaleRecord
	}

	now := r.now()
	if !isTerminalPaneStatus(pane.Status) {
		pane.Status = status
		pane.StatusMessage = message
		pane.UpdatedAt = now
	}
	claimed := !pane.closeNotificationClaimed
	pane.closeNotificationClaimed = true
	tab.Panes[id] = pane
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	return clonePaneRecord(pane), claimed, nil
}

func (r *Registry) updatePaneStatusGeneration(id PaneID, generation uint64, status PaneStatus, message string) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, ErrStaleRecord
	}

	now := r.now()
	pane.Status = status
	pane.StatusMessage = message
	pane.UpdatedAt = now
	tab.Panes[id] = pane
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	return clonePaneRecord(pane), nil
}

func (r *Registry) UpdatePaneOutput(id PaneID, output string) (PaneRecord, error) {
	return r.updatePaneOutputGeneration(id, 0, output)
}

func (r *Registry) UpdatePaneOutputGeneration(id PaneID, generation uint64, output string) (PaneRecord, error) {
	return r.updatePaneOutputGeneration(id, generation, output)
}

// UpdateActivePaneOutputGeneration updates output only while the pane
// lifecycle is non-terminal.
func (r *Registry) UpdateActivePaneOutputGeneration(id PaneID, generation uint64, output string) (PaneRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, false, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, false, ErrStaleRecord
	}
	if isTerminalPaneStatus(pane.Status) {
		return clonePaneRecord(pane), false, nil
	}

	now := r.now()
	pane.LastOutput = output
	pane.UpdatedAt = now
	tab.Panes[id] = pane
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	return clonePaneRecord(pane), true, nil
}

func (r *Registry) updatePaneOutputGeneration(id PaneID, generation uint64, output string) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, ErrStaleRecord
	}

	now := r.now()
	pane.LastOutput = output
	pane.UpdatedAt = now
	tab.Panes[id] = pane
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	return clonePaneRecord(pane), nil
}

func (r *Registry) RemovePane(id PaneID) (PaneRecord, error) {
	return r.removePaneGeneration(id, 0)
}

func (r *Registry) RemovePaneGeneration(id PaneID, generation uint64) (PaneRecord, error) {
	return r.removePaneGeneration(id, generation)
}

// RemovePaneGenerationClaimingClosure atomically removes a pane and claims its
// one close notification if no earlier terminal transition claimed it.
func (r *Registry) RemovePaneGenerationClaimingClosure(id PaneID, generation uint64) (PaneRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, false, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, false, ErrStaleRecord
	}
	claimed := !pane.closeNotificationClaimed
	pane.closeNotificationClaimed = true
	r.removePaneLocked(loc, session, tab, pane)
	return clonePaneRecord(pane), claimed, nil
}

func (r *Registry) removePaneGeneration(id PaneID, generation uint64) (PaneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
	}
	if generation != 0 && pane.Generation != generation {
		return PaneRecord{}, ErrStaleRecord
	}

	r.removePaneLocked(loc, session, tab, pane)

	return clonePaneRecord(pane), nil
}

func (r *Registry) removePaneLocked(loc paneLocation, session SessionRecord, tab TabRecord, pane PaneRecord) {
	delete(tab.Panes, pane.ID)
	now := r.now()
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	delete(r.paneToLocation, pane.ID)
	if pane.ZellijPaneID != "" && r.latestByZellij[pane.ZellijPaneID] == pane.ID {
		delete(r.latestByZellij, pane.ZellijPaneID)
	}
}

func isTerminalPaneStatus(status PaneStatus) bool {
	switch status {
	case PaneStatusClosed, PaneStatusExited, PaneStatusLost:
		return true
	default:
		return false
	}
}

func (r *Registry) GetSession(id SessionID) (SessionRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	session, err := r.getSessionLocked(id)
	if err != nil {
		return SessionRecord{}, err
	}

	return cloneSessionRecord(session), nil
}

func (r *Registry) ListSessions() []SessionRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []SessionRecord
	for _, session := range r.sessions {
		list = append(list, cloneSessionRecord(session))
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})

	return list
}

func (r *Registry) GetTab(sessionID SessionID, tabID TabID) (TabRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tab, err := r.getTabLocked(sessionID, tabID)
	if err != nil {
		return TabRecord{}, err
	}

	return cloneTabRecord(tab), nil
}

func (r *Registry) ListTabs(sessionID SessionID) ([]TabRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	session, err := r.getSessionLocked(sessionID)
	if err != nil {
		return nil, err
	}

	var list []TabRecord
	for _, tab := range session.Tabs {
		list = append(list, cloneTabRecord(tab))
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})

	return list, nil
}

func cloneSessionRecord(session SessionRecord) SessionRecord {
	if session.Tabs == nil {
		session.Tabs = make(map[TabID]TabRecord)
		return session
	}
	tabs := make(map[TabID]TabRecord, len(session.Tabs))
	for k, v := range session.Tabs {
		tabs[k] = cloneTabRecord(v)
	}
	session.Tabs = tabs
	return session
}

func cloneTabRecord(tab TabRecord) TabRecord {
	if tab.Panes == nil {
		tab.Panes = make(map[PaneID]PaneRecord)
		return tab
	}
	panes := make(map[PaneID]PaneRecord, len(tab.Panes))
	for k, v := range tab.Panes {
		panes[k] = clonePaneRecord(v)
	}
	tab.Panes = panes
	return tab
}

func clonePaneRecord(record PaneRecord) PaneRecord {
	record.Command = cloneStrings(record.Command)
	record.ZellijTabID = cloneZellijTabID(record.ZellijTabID)
	return record
}

func cloneZellijTabID(value *ZellijTabID) *ZellijTabID {
	if value == nil {
		return nil
	}

	clone := *value
	return &clone
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}

	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}

func (r *Registry) validatePaneUniqueLocked(id PaneID) error {
	if _, ok := r.paneToLocation[id]; ok {
		return ErrAlreadyExists
	}
	return nil
}

func (r *Registry) validateActiveZellijPaneUniqueLocked(req RegisterPaneRequest) error {
	if req.ZellijPaneID == "" {
		return nil
	}

	session, ok := r.sessions[req.SessionID]
	if !ok {
		return nil
	}
	for _, tab := range session.Tabs {
		for _, pane := range tab.Panes {
			if pane.ZellijPaneID == req.ZellijPaneID && !isTerminalPaneStatus(pane.Status) {
				return fmt.Errorf("%w: session %q pane %q", ErrZellijPaneAlreadyRegistered, req.SessionID, req.ZellijPaneID)
			}
		}
	}
	return nil
}

func applyRegisterPaneDefaults(req RegisterPaneRequest) RegisterPaneRequest {
	if req.Status == "" {
		req.Status = PaneStatusStarting
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	if req.TabID == "" {
		if req.ZellijTabID != nil {
			req.TabID = TabID(strconv.Itoa(int(*req.ZellijTabID)))
		} else if req.TabName != "" {
			req.TabID = TabID(req.TabName)
		} else {
			req.TabID = "default"
		}
	}
	return req
}

func (r *Registry) getSessionLocked(id SessionID) (SessionRecord, error) {
	session, ok := r.sessions[id]
	if !ok {
		return SessionRecord{}, ErrNotFound
	}
	return session, nil
}

func (r *Registry) getTabLocked(sessionID SessionID, tabID TabID) (TabRecord, error) {
	session, err := r.getSessionLocked(sessionID)
	if err != nil {
		return TabRecord{}, err
	}
	tab, ok := session.Tabs[tabID]
	if !ok {
		return TabRecord{}, ErrNotFound
	}
	return tab, nil
}

func (r *Registry) getPaneLocationLocked(id PaneID) (paneLocation, error) {
	loc, ok := r.paneToLocation[id]
	if !ok {
		return paneLocation{}, ErrNotFound
	}
	return loc, nil
}

func (r *Registry) resolvePanePathLocked(id PaneID) (paneLocation, SessionRecord, TabRecord, PaneRecord, error) {
	loc, err := r.getPaneLocationLocked(id)
	if err != nil {
		return paneLocation{}, SessionRecord{}, TabRecord{}, PaneRecord{}, err
	}
	session, err := r.getSessionLocked(loc.SessionID)
	if err != nil {
		return paneLocation{}, SessionRecord{}, TabRecord{}, PaneRecord{}, err
	}
	tab, ok := session.Tabs[loc.TabID]
	if !ok {
		return paneLocation{}, SessionRecord{}, TabRecord{}, PaneRecord{}, ErrNotFound
	}
	pane, ok := tab.Panes[id]
	if !ok {
		return paneLocation{}, SessionRecord{}, TabRecord{}, PaneRecord{}, ErrNotFound
	}
	return loc, session, tab, pane, nil
}
