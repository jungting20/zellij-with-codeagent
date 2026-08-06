package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zellij-with-codeagent/internal/runtime"
)

var (
	ErrInvalidAgentKind     = errors.New("invalid coding agent kind")
	ErrInvalidAgentCWD      = errors.New("invalid coding agent cwd")
	ErrAgentSourceRequired  = errors.New("coding agent source Zellij context is required")
	ErrAgentIDRequired      = errors.New("coding agent id is required")
	ErrAgentRuntimeRequired = errors.New("coding agent runtime service is required")
	ErrAgentMonitorRequired = errors.New("coding agent lifecycle monitor is required")
)

type AgentWithPane struct {
	Agent Record
	Pane  runtime.Pane
}

type StartAgentRequest struct {
	Kind                Kind
	AccessMode          AccessMode
	CWD                 string
	ExtraArgs           []string
	NotifyOnIdle        bool
	SourceZellijSession string
	SourceZellijPaneID  runtime.ZellijPaneID
}

type StartAgentResponse struct {
	Agent AgentWithPane
}

type ListAgentsResponse struct {
	Agents []AgentWithPane
}

type FocusAgentRequest struct {
	AgentID             ID
	SourceZellijSession string
	SourceZellijPaneID  runtime.ZellijPaneID
}

type FocusAgentResponse struct {
	Agent AgentWithPane
}

type FocusNextAgentRequest struct {
	SourceZellijSession string
	SourceZellijPaneID  runtime.ZellijPaneID
	IdleOnly            bool
}

type FocusNextAgentResponse struct {
	Focused bool
	Agent   AgentWithPane
}

type AgentService interface {
	StartAgent(context.Context, StartAgentRequest) (StartAgentResponse, error)
	ListAgents(context.Context) (ListAgentsResponse, error)
	FocusAgent(context.Context, FocusAgentRequest) (FocusAgentResponse, error)
	FocusNextAgent(context.Context, FocusNextAgentRequest) (FocusNextAgentResponse, error)
}

type LifecycleMonitor interface {
	Start(Record) error
	Stop(ID)
}

type ServiceOptions struct {
	runtime.RuntimeService
	Store
	LifecycleMonitor LifecycleMonitor
	Now              func() time.Time
	NewAgentID       func() ID
}

type Service struct {
	runtime.RuntimeService
	store      Store
	monitor    LifecycleMonitor
	now        func() time.Time
	newAgentID func() ID

	focusMu       sync.Mutex
	lastFocusedID ID

	lifecycleMu sync.Mutex
	nextOwner   uint64
	owners      map[ID]*agentOwnership
}

type agentLifecycle uint8

const (
	agentProvisioning agentLifecycle = iota
	agentActive
	agentCleanupUncertain
	agentCleaning
)

type agentOwnership struct {
	token  uint64
	record Record
	state  agentLifecycle
}

type agentSnapshot struct {
	record    Record
	token     uint64
	lifecycle agentLifecycle
	owned     bool
}

// NewService returns nil when any required runtime, store, or monitor dependency
// is nil, including an interface containing a typed-nil value.
func NewService(opts ServiceOptions) *Service {
	if isNilDependency(opts.RuntimeService) || isNilDependency(opts.Store) || isNilDependency(opts.LifecycleMonitor) {
		return nil
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	newAgentID := opts.NewAgentID
	if newAgentID == nil {
		newAgentID = sequentialAgentIDGenerator()
	}
	return &Service{
		RuntimeService: opts.RuntimeService,
		store:          opts.Store,
		monitor:        opts.LifecycleMonitor,
		now:            now,
		newAgentID:     newAgentID,
		owners:         make(map[ID]*agentOwnership),
	}
}

func (s *Service) StartAgent(ctx context.Context, request StartAgentRequest) (StartAgentResponse, error) {
	profile, ok := LookupProfile(request.Kind)
	if !ok {
		return StartAgentResponse{}, fmt.Errorf("%w: %q", ErrInvalidAgentKind, request.Kind)
	}
	accessMode, err := ParseAccessMode(string(request.AccessMode))
	if err != nil {
		return StartAgentResponse{}, err
	}
	command, err := profile.BuildManagedCommand(accessMode, request.ExtraArgs)
	if err != nil {
		return StartAgentResponse{}, err
	}
	cwd, err := resolveAgentCWD(request.CWD)
	if err != nil {
		return StartAgentResponse{}, err
	}
	sourceSession := strings.TrimSpace(request.SourceZellijSession)
	sourcePaneID := runtime.ZellijPaneID(strings.TrimSpace(string(request.SourceZellijPaneID)))
	if sourceSession == "" || sourcePaneID == "" {
		return StartAgentResponse{}, ErrAgentSourceRequired
	}
	id := s.newAgentID()
	if id == "" {
		return StartAgentResponse{}, ErrAgentIDRequired
	}
	now := s.now()
	record := Record{
		ID:             id,
		Kind:           request.Kind,
		AccessMode:     accessMode,
		PaneID:         runtime.PaneID(id),
		State:          StateUnknown,
		NotifyOnIdle:   request.NotifyOnIdle,
		CreatedAt:      now,
		StateChangedAt: now,
	}
	created, owner, err := s.registerStart(record)
	if err != nil {
		return StartAgentResponse{}, fmt.Errorf("register coding agent %q: %w", id, err)
	}
	if err := s.monitor.Start(created); err != nil {
		_, rollbackErr := s.cleanupOwnedRecord(owner, false)
		return StartAgentResponse{}, errors.Join(
			fmt.Errorf("start coding agent monitor %q: %w", id, err),
			rollbackErr,
		)
	}

	paneResponse, err := s.RuntimeService.ClaimPane(ctx, runtime.ClaimPaneRequest{
		ID:            created.PaneID,
		AgentID:       runtime.AgentID(created.ID),
		Role:          "coding-agent",
		ZellijSession: sourceSession,
		ZellijPaneID:  sourcePaneID,
		Command:       command,
		CWD:           cwd,
	})
	if err != nil {
		_, rollbackErr := s.cleanupOwnedRecord(owner, true)
		return StartAgentResponse{}, errors.Join(
			fmt.Errorf("claim coding agent pane %q: %w", id, err),
			rollbackErr,
		)
	}

	s.markOwnerState(owner, agentActive)
	return StartAgentResponse{Agent: AgentWithPane{Agent: created, Pane: paneResponse.Pane}}, nil
}

func (s *Service) ListAgents(ctx context.Context) (ListAgentsResponse, error) {
	snapshots, err := s.snapshotAgents()
	if err != nil {
		return ListAgentsResponse{}, fmt.Errorf("list coding agents: %w", err)
	}
	paneResponse, err := s.RuntimeService.ListPanes(ctx)
	if err != nil {
		return ListAgentsResponse{}, fmt.Errorf("list runtime panes: %w", err)
	}
	panesByID := make(map[runtime.PaneID]runtime.Pane, len(paneResponse.Panes))
	for _, pane := range paneResponse.Panes {
		panesByID[pane.ID] = pane
	}

	agents := make([]AgentWithPane, 0, len(snapshots))
	var cleanupErr error
	for _, snapshot := range snapshots {
		pane, ok := panesByID[snapshot.record.PaneID]
		if !ok {
			cleanupErr = errors.Join(cleanupErr, s.cleanupOrphan(snapshot))
			continue
		}
		if s.snapshotIsCurrent(snapshot) {
			agents = append(agents, AgentWithPane{Agent: snapshot.record, Pane: pane})
		}
	}
	if cleanupErr != nil {
		return ListAgentsResponse{}, fmt.Errorf("remove orphaned coding agents: %w", cleanupErr)
	}
	return ListAgentsResponse{Agents: agents}, nil
}

func (s *Service) FocusAgent(ctx context.Context, request FocusAgentRequest) (FocusAgentResponse, error) {
	s.focusMu.Lock()
	defer s.focusMu.Unlock()
	return s.focusAgentLocked(ctx, request)
}

func (s *Service) FocusNextAgent(ctx context.Context, request FocusNextAgentRequest) (FocusNextAgentResponse, error) {
	s.focusMu.Lock()
	defer s.focusMu.Unlock()

	sourceSession := strings.TrimSpace(request.SourceZellijSession)
	sourcePaneID := runtime.ZellijPaneID(strings.TrimSpace(string(request.SourceZellijPaneID)))
	if sourceSession == "" || sourcePaneID == "" {
		return FocusNextAgentResponse{}, ErrAgentSourceRequired
	}
	records, err := s.store.List()
	if err != nil {
		return FocusNextAgentResponse{}, fmt.Errorf("list coding agents: %w", err)
	}
	record, ok := nextAgentRecord(records, s.lastFocusedID, request.IdleOnly)
	if !ok {
		return FocusNextAgentResponse{Focused: false}, nil
	}
	response, err := s.focusAgentLocked(ctx, FocusAgentRequest{
		AgentID:             record.ID,
		SourceZellijSession: sourceSession,
		SourceZellijPaneID:  sourcePaneID,
	})
	if err != nil {
		return FocusNextAgentResponse{}, err
	}
	return FocusNextAgentResponse{Focused: true, Agent: response.Agent}, nil
}

func (s *Service) focusAgentLocked(ctx context.Context, request FocusAgentRequest) (FocusAgentResponse, error) {
	sourceSession := strings.TrimSpace(request.SourceZellijSession)
	sourcePaneID := runtime.ZellijPaneID(strings.TrimSpace(string(request.SourceZellijPaneID)))
	if sourceSession == "" || sourcePaneID == "" {
		return FocusAgentResponse{}, ErrAgentSourceRequired
	}
	s.sweepInactiveOwners()
	record, err := s.store.Get(request.AgentID)
	if err != nil {
		return FocusAgentResponse{}, fmt.Errorf("get coding agent %q: %w", request.AgentID, err)
	}
	response, err := s.RuntimeService.FocusPane(ctx, runtime.FocusPaneRequest{
		PaneID:              record.PaneID,
		SourceZellijSession: sourceSession,
		SourceZellijPaneID:  sourcePaneID,
	})
	if err != nil {
		if errors.Is(err, runtime.ErrPaneNotFound) {
			return FocusAgentResponse{}, errors.Join(
				fmt.Errorf("%w: %q", ErrNotFound, request.AgentID),
				fmt.Errorf("focus coding agent %q: %w", request.AgentID, err),
			)
		}
		if errors.Is(err, runtime.ErrInvalidPaneTarget) {
			return FocusAgentResponse{}, s.classifyInvalidFocusTarget(ctx, record, err)
		}
		return FocusAgentResponse{}, fmt.Errorf("focus coding agent %q: %w", request.AgentID, err)
	}
	if terminalAgentPane(response.Pane.Status) {
		return FocusAgentResponse{}, fmt.Errorf("%w: %q runtime pane is %s", ErrNotFound, request.AgentID, response.Pane.Status)
	}
	s.lastFocusedID = record.ID
	return FocusAgentResponse{Agent: AgentWithPane{Agent: record, Pane: response.Pane}}, nil
}

func nextAgentRecord(records []Record, current ID, idleOnly bool) (Record, bool) {
	eligible := make([]Record, 0, len(records))
	for _, record := range records {
		if !idleOnly || record.State == StateIdle {
			eligible = append(eligible, record)
		}
	}
	if len(eligible) == 0 {
		return Record{}, false
	}
	for index := range eligible {
		if eligible[index].ID == current {
			return eligible[(index+1)%len(eligible)], true
		}
	}
	return eligible[0], true
}

func (s *Service) ListSessions(ctx context.Context) ([]runtime.SessionRecord, error) {
	inspector, ok := s.RuntimeService.(runtime.SessionInspectionService)
	if !ok {
		return nil, errors.New("coding agent runtime does not support session inspection")
	}
	return inspector.ListSessions(ctx)
}

func (s *Service) GetSession(ctx context.Context, id runtime.SessionID) (runtime.SessionRecord, error) {
	inspector, ok := s.RuntimeService.(runtime.SessionInspectionService)
	if !ok {
		return runtime.SessionRecord{}, errors.New("coding agent runtime does not support session inspection")
	}
	return inspector.GetSession(ctx, id)
}

func (s *Service) ListTabs(ctx context.Context, sessionID runtime.SessionID) ([]runtime.TabRecord, error) {
	inspector, ok := s.RuntimeService.(runtime.SessionInspectionService)
	if !ok {
		return nil, errors.New("coding agent runtime does not support session inspection")
	}
	return inspector.ListTabs(ctx, sessionID)
}

func (s *Service) GetTab(ctx context.Context, sessionID runtime.SessionID, tabID runtime.TabID) (runtime.TabRecord, error) {
	inspector, ok := s.RuntimeService.(runtime.SessionInspectionService)
	if !ok {
		return runtime.TabRecord{}, errors.New("coding agent runtime does not support session inspection")
	}
	return inspector.GetTab(ctx, sessionID, tabID)
}

func (s *Service) registerStart(record Record) (Record, *agentOwnership, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.sweepInactiveOwnersLocked(nil)

	if existing := s.owners[record.ID]; existing != nil {
		if existing.state == agentProvisioning || existing.state == agentCleaning {
			return Record{}, nil, fmt.Errorf("%w: %q is reserved", ErrDuplicateID, record.ID)
		}
		if _, err := s.store.Get(record.ID); err == nil {
			return Record{}, nil, fmt.Errorf("%w: %q", ErrDuplicateID, record.ID)
		} else if !errors.Is(err, ErrNotFound) {
			return Record{}, nil, err
		}
		delete(s.owners, record.ID)
	}

	s.nextOwner++
	owner := &agentOwnership{token: s.nextOwner, record: record, state: agentProvisioning}
	s.owners[record.ID] = owner
	created, err := s.store.Create(record)
	if err != nil {
		delete(s.owners, record.ID)
		return Record{}, nil, err
	}
	owner.record = created
	return created, owner, nil
}

func (s *Service) snapshotAgents() ([]agentSnapshot, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	records, err := s.store.List()
	if err != nil {
		return nil, err
	}
	currentByID := make(map[ID]Record, len(records))
	for _, record := range records {
		currentByID[record.ID] = record
	}
	s.sweepInactiveOwnersLocked(currentByID)
	snapshots := make([]agentSnapshot, 0, len(records))
	for _, record := range records {
		snapshot := agentSnapshot{record: record}
		if owner := s.owners[record.ID]; owner != nil && sameRecordIdentity(owner.record, record) {
			snapshot.token = owner.token
			snapshot.lifecycle = owner.state
			snapshot.owned = true
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (s *Service) snapshotIsCurrent(snapshot agentSnapshot) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.ownerMatchesSnapshot(snapshot) {
		return false
	}
	current, err := s.store.Get(snapshot.record.ID)
	return err == nil && sameRecordIdentity(current, snapshot.record)
}

func (s *Service) cleanupOrphan(snapshot agentSnapshot) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if !s.ownerMatchesSnapshot(snapshot) {
		return nil
	}
	if snapshot.owned && (snapshot.lifecycle == agentProvisioning || snapshot.lifecycle == agentCleanupUncertain) {
		return nil
	}
	owner := s.owners[snapshot.record.ID]
	if owner != nil {
		switch owner.state {
		case agentProvisioning, agentCleanupUncertain, agentCleaning:
			return nil
		}
	} else {
		s.nextOwner++
		owner = &agentOwnership{
			token:  s.nextOwner,
			record: snapshot.record,
			state:  agentCleaning,
		}
		s.owners[snapshot.record.ID] = owner
	}

	current, err := s.store.Get(snapshot.record.ID)
	if errors.Is(err, ErrNotFound) {
		delete(s.owners, snapshot.record.ID)
		return nil
	}
	if err != nil {
		owner.state = agentCleanupUncertain
		return err
	}
	if !sameRecordIdentity(current, snapshot.record) {
		delete(s.owners, snapshot.record.ID)
		return nil
	}
	owner.state = agentCleaning
	if err := s.store.Delete(snapshot.record.ID); err != nil && !errors.Is(err, ErrNotFound) {
		owner.state = agentCleanupUncertain
		return err
	}
	s.monitor.Stop(snapshot.record.ID)
	delete(s.owners, snapshot.record.ID)
	return nil
}

func (s *Service) ownerMatchesSnapshot(snapshot agentSnapshot) bool {
	owner := s.owners[snapshot.record.ID]
	if snapshot.token == 0 {
		return owner == nil
	}
	return owner != nil && owner.token == snapshot.token && sameRecordIdentity(owner.record, snapshot.record)
}

func (s *Service) cleanupOwnedRecord(owner *agentOwnership, stopMonitor bool) (bool, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if owner == nil || s.owners[owner.record.ID] != owner {
		return false, nil
	}
	current, err := s.store.Get(owner.record.ID)
	if errors.Is(err, ErrNotFound) {
		delete(s.owners, owner.record.ID)
		return false, nil
	}
	if err != nil {
		owner.state = agentCleanupUncertain
		return false, err
	}
	if !sameRecordIdentity(current, owner.record) {
		delete(s.owners, owner.record.ID)
		return false, nil
	}
	owner.state = agentCleaning
	if err := s.store.Delete(owner.record.ID); err != nil && !errors.Is(err, ErrNotFound) {
		owner.state = agentCleanupUncertain
		return false, err
	}
	if stopMonitor {
		s.monitor.Stop(owner.record.ID)
	}
	delete(s.owners, owner.record.ID)
	return true, nil
}

func (s *Service) markOwnerState(owner *agentOwnership, state agentLifecycle) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if owner != nil && s.owners[owner.record.ID] == owner {
		owner.state = state
	}
}

func (s *Service) sweepInactiveOwners() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.sweepInactiveOwnersLocked(nil)
}

func (s *Service) sweepInactiveOwnersLocked(currentByID map[ID]Record) {
	for id, owner := range s.owners {
		if owner.state == agentProvisioning || owner.state == agentCleaning {
			continue
		}
		var (
			current Record
			ok      bool
		)
		if currentByID != nil {
			current, ok = currentByID[id]
		} else {
			var err error
			current, err = s.store.Get(id)
			if err == nil {
				ok = true
			} else if !errors.Is(err, ErrNotFound) {
				continue
			}
		}
		if !ok || !sameRecordIdentity(owner.record, current) {
			delete(s.owners, id)
		}
	}
}

func (s *Service) classifyInvalidFocusTarget(ctx context.Context, record Record, focusErr error) error {
	wrappedFocusErr := fmt.Errorf("focus coding agent %q: %w", record.ID, focusErr)
	response, err := s.RuntimeService.ListPanes(ctx)
	if err != nil {
		return errors.Join(
			wrappedFocusErr,
			fmt.Errorf("confirm coding agent %q focus target: %w", record.ID, err),
		)
	}
	for _, pane := range response.Panes {
		if pane.ID != record.PaneID {
			continue
		}
		if terminalAgentPane(pane.Status) {
			return errors.Join(
				fmt.Errorf("%w: %q runtime pane is %s", ErrNotFound, record.ID, pane.Status),
				wrappedFocusErr,
			)
		}
		return wrappedFocusErr
	}
	return errors.Join(
		fmt.Errorf("%w: %q runtime pane is absent", ErrNotFound, record.ID),
		wrappedFocusErr,
	)
}

func sameRecordIdentity(left, right Record) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.PaneID == right.PaneID && left.CreatedAt.Equal(right.CreatedAt)
}

func terminalAgentPane(status runtime.PaneStatus) bool {
	switch status {
	case runtime.PaneStatusClosed, runtime.PaneStatusExited, runtime.PaneStatusLost:
		return true
	default:
		return false
	}
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return reflected.IsNil()
	default:
		return false
	}
}

func resolveAgentCWD(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("%w: path is required", ErrInvalidAgentCWD)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %v", ErrInvalidAgentCWD, cwd, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%w: access %q: %v", ErrInvalidAgentCWD, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %q is not a directory", ErrInvalidAgentCWD, abs)
	}
	return abs, nil
}

func sequentialAgentIDGenerator() func() ID {
	var next uint64
	return func() ID {
		return ID(fmt.Sprintf("agent-%d", atomic.AddUint64(&next, 1)))
	}
}

var _ AgentService = (*Service)(nil)
var _ runtime.RuntimeService = (*Service)(nil)
