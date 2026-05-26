package registry

import (
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"
)

var (
	ErrAlreadyExists = errors.New("registry record already exists")
	ErrNotFound      = errors.New("registry record not found")
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
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validatePaneUniqueLocked(req.ID); err != nil {
		return PaneRecord{}, err
	}

	req = applyRegisterPaneDefaults(req)

	now := r.now()
	record := PaneRecord{
		ID:           req.ID,
		SessionID:    req.SessionID,
		TabID:        req.TabID,
		TaskID:       req.TaskID,
		AgentID:      req.AgentID,
		ZellijPaneID: req.ZellijPaneID,
		ZellijTabID:  cloneZellijTabID(req.ZellijTabID),
		TabName:      req.TabName,
		Role:         req.Role,
		Command:      cloneStrings(req.Command),
		CWD:          req.CWD,
		Status:       req.Status,
		CreatedAt:    now,
		UpdatedAt:    now,
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
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
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
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
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
	r.mu.Lock()
	defer r.mu.Unlock()

	loc, session, tab, pane, err := r.resolvePanePathLocked(id)
	if err != nil {
		return PaneRecord{}, err
	}

	delete(tab.Panes, id)
	now := r.now()
	tab.UpdatedAt = now
	session.Tabs[loc.TabID] = tab
	session.UpdatedAt = now
	r.sessions[loc.SessionID] = session

	delete(r.paneToLocation, id)
	if pane.ZellijPaneID != "" && r.latestByZellij[pane.ZellijPaneID] == id {
		delete(r.latestByZellij, pane.ZellijPaneID)
	}

	return clonePaneRecord(pane), nil
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
		return paneLocation{}, SessionRecord{}, TabRecord{}, PaneRecord{}, err
	}
	pane, ok := tab.Panes[id]
	if !ok {
		return paneLocation{}, SessionRecord{}, TabRecord{}, PaneRecord{}, err
	}
	return loc, session, tab, pane, nil
}
