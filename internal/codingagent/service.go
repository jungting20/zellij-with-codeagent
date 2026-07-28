package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	CWD                 string
	ExtraArgs           []string
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

type AgentService interface {
	StartAgent(context.Context, StartAgentRequest) (StartAgentResponse, error)
	ListAgents(context.Context) (ListAgentsResponse, error)
	FocusAgent(context.Context, FocusAgentRequest) (FocusAgentResponse, error)
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
}

func NewService(opts ServiceOptions) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	store := opts.Store
	if store == nil {
		store = NewMemoryStore(now)
	}
	newAgentID := opts.NewAgentID
	if newAgentID == nil {
		newAgentID = sequentialAgentIDGenerator()
	}
	return &Service{
		RuntimeService: opts.RuntimeService,
		store:          store,
		monitor:        opts.LifecycleMonitor,
		now:            now,
		newAgentID:     newAgentID,
	}
}

func (s *Service) StartAgent(ctx context.Context, request StartAgentRequest) (StartAgentResponse, error) {
	profile, ok := LookupProfile(request.Kind)
	if !ok {
		return StartAgentResponse{}, fmt.Errorf("%w: %q", ErrInvalidAgentKind, request.Kind)
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
	if s.RuntimeService == nil {
		return StartAgentResponse{}, ErrAgentRuntimeRequired
	}
	if s.monitor == nil {
		return StartAgentResponse{}, ErrAgentMonitorRequired
	}

	id := s.newAgentID()
	if id == "" {
		return StartAgentResponse{}, ErrAgentIDRequired
	}
	now := s.now()
	record := Record{
		ID:             id,
		Kind:           request.Kind,
		PaneID:         runtime.PaneID(id),
		State:          StateUnknown,
		CreatedAt:      now,
		StateChangedAt: now,
	}
	created, err := s.store.Create(record)
	if err != nil {
		return StartAgentResponse{}, fmt.Errorf("register coding agent %q: %w", id, err)
	}
	if err := s.monitor.Start(created); err != nil {
		return StartAgentResponse{}, errors.Join(
			fmt.Errorf("start coding agent monitor %q: %w", id, err),
			s.deleteRecord(id),
		)
	}

	paneResponse, err := s.RuntimeService.CreatePane(ctx, runtime.CreatePaneRequest{
		ID:                    created.PaneID,
		AgentID:               runtime.AgentID(created.ID),
		Role:                  "coding-agent",
		Name:                  fmt.Sprintf("%s-%s", created.Kind, created.ID),
		ZellijSession:         sourceSession,
		SameTabAsZellijPaneID: sourcePaneID,
		Command:               profile.BuildCommand(true, request.ExtraArgs),
		CWD:                   cwd,
	})
	if err != nil {
		s.monitor.Stop(created.ID)
		return StartAgentResponse{}, errors.Join(
			fmt.Errorf("create coding agent pane %q: %w", id, err),
			s.deleteRecord(id),
		)
	}

	return StartAgentResponse{Agent: AgentWithPane{Agent: created, Pane: paneResponse.Pane}}, nil
}

func (s *Service) ListAgents(ctx context.Context) (ListAgentsResponse, error) {
	if s.RuntimeService == nil {
		return ListAgentsResponse{}, ErrAgentRuntimeRequired
	}
	records, err := s.store.List()
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

	agents := make([]AgentWithPane, 0, len(records))
	var cleanupErr error
	for _, record := range records {
		pane, ok := panesByID[record.PaneID]
		if !ok {
			if s.monitor != nil {
				s.monitor.Stop(record.ID)
			}
			cleanupErr = errors.Join(cleanupErr, s.deleteRecord(record.ID))
			continue
		}
		agents = append(agents, AgentWithPane{Agent: record, Pane: pane})
	}
	if cleanupErr != nil {
		return ListAgentsResponse{}, fmt.Errorf("remove orphaned coding agents: %w", cleanupErr)
	}
	return ListAgentsResponse{Agents: agents}, nil
}

func (s *Service) FocusAgent(ctx context.Context, request FocusAgentRequest) (FocusAgentResponse, error) {
	if s.RuntimeService == nil {
		return FocusAgentResponse{}, ErrAgentRuntimeRequired
	}
	record, err := s.store.Get(request.AgentID)
	if err != nil {
		return FocusAgentResponse{}, fmt.Errorf("get coding agent %q: %w", request.AgentID, err)
	}
	response, err := s.RuntimeService.FocusPane(ctx, runtime.FocusPaneRequest{
		PaneID:              record.PaneID,
		SourceZellijSession: request.SourceZellijSession,
		SourceZellijPaneID:  request.SourceZellijPaneID,
	})
	if err != nil {
		return FocusAgentResponse{}, fmt.Errorf("focus coding agent %q: %w", request.AgentID, err)
	}
	return FocusAgentResponse{Agent: AgentWithPane{Agent: record, Pane: response.Pane}}, nil
}

func (s *Service) deleteRecord(id ID) error {
	err := s.store.Delete(id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
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
