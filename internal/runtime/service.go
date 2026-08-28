package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

type PaneIDGenerator func() PaneID
type OwnershipTokenGenerator func() (OwnershipToken, error)

type Options struct {
	Registry           *registry.Registry
	Backend            zellij.Backend
	SessionSwitcher    zellij.SessionSwitcher
	NewPaneID          PaneIDGenerator
	NewOwnershipToken  OwnershipTokenGenerator
	EventBus           *eventbus.Bus
	SubscriptionRunner SubscriptionRunner
	PaneObserver       PaneObserver
}

type Service struct {
	registry          *registry.Registry
	backend           zellij.Backend
	switcher          zellij.SessionSwitcher
	newPaneID         PaneIDGenerator
	newOwnershipToken OwnershipTokenGenerator
	bus               *eventbus.Bus
	subs              *SubscriptionManager
	observer          PaneObserver
	createMu          sync.Mutex
	creates           map[PaneID]*createPaneCall
}

type createPaneCall struct {
	request       CreatePaneRequest
	done          chan struct{}
	response      CreatePaneResponse
	createdRecord registry.PaneRecord
	err           error
}

func NewService(opts Options) *Service {
	reg := opts.Registry
	if reg == nil {
		reg = registry.New()
	}

	backend := opts.Backend
	if backend == nil {
		backend = zellij.NewBackend(zellij.Options{})
	}
	switcher := opts.SessionSwitcher
	if isNilInterface(switcher) {
		switcher = nil
		if candidate, ok := backend.(zellij.SessionSwitcher); ok && !isNilInterface(candidate) {
			switcher = candidate
		}
	}

	newPaneID := opts.NewPaneID
	if newPaneID == nil {
		newPaneID = sequentialPaneIDGenerator()
	}
	newOwnershipToken := opts.NewOwnershipToken
	if newOwnershipToken == nil {
		newOwnershipToken = randomOwnershipToken
	}

	bus := opts.EventBus
	if bus == nil {
		bus = eventbus.New()
	}

	var subs *SubscriptionManager
	if opts.SubscriptionRunner != nil {
		subs = NewSubscriptionManager(SubscriptionManagerOptions{
			Registry: reg,
			Backend:  backend,
			Bus:      bus,
			Runner:   opts.SubscriptionRunner,
			Observer: opts.PaneObserver,
		})
	}

	return &Service{
		registry:          reg,
		backend:           backend,
		switcher:          switcher,
		newPaneID:         newPaneID,
		newOwnershipToken: newOwnershipToken,
		bus:               bus,
		subs:              subs,
		observer:          opts.PaneObserver,
		creates:           make(map[PaneID]*createPaneCall),
	}
}

func isNilInterface(value any) bool {
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

func (s *Service) CreatePane(ctx context.Context, req CreatePaneRequest) (CreatePaneResponse, error) {
	req.ZellijSession = strings.TrimSpace(req.ZellijSession)
	if req.ZellijSession == "" {
		return CreatePaneResponse{}, ErrZellijSessionRequired
	}

	id := req.ID
	if id == "" {
		id = s.newPaneID()
	}
	if id == "" {
		return CreatePaneResponse{}, ErrMissingPaneID
	}
	req.ID = id

	call, leader, err := s.beginCreatePane(req)
	if err != nil {
		return CreatePaneResponse{}, err
	}
	if !leader {
		select {
		case <-ctx.Done():
			return CreatePaneResponse{}, ctx.Err()
		case <-call.done:
			return call.response, call.err
		}
	}

	req, err = s.resolveCreatePaneTarget(ctx, req)
	if err != nil {
		s.finishCreatePane(call, CreatePaneResponse{}, err)
		return CreatePaneResponse{}, err
	}
	response, createErr := s.createPaneOnce(ctx, req, id)
	if createErr == nil {
		if err := s.initializeCreatedPane(ctx, response, req.InitialInput, req.InitialInputReadyText); err != nil {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			cleanupErr := s.cleanupCreatedPane(cleanupCtx, response.record)
			cancelCleanup()
			createErr = paneInitializationError(err, cleanupErr)
		}
	}
	s.finishCreatePane(call, response, createErr)
	if createErr != nil {
		return CreatePaneResponse{}, createErr
	}
	return response, createErr
}

func (s *Service) beginCreatePane(req CreatePaneRequest) (*createPaneCall, bool, error) {
	s.createMu.Lock()
	defer s.createMu.Unlock()
	id := PaneID(req.ID)
	if existing, ok := s.creates[id]; ok {
		select {
		case <-existing.done:
			record, recordErr := s.registry.GetPane(registry.PaneID(id))
			switch {
			case existing.err == nil:
				if errors.Is(recordErr, registry.ErrNotFound) || (recordErr == nil && record.Status != registry.PaneStatusStarting && record.Status != registry.PaneStatusRunning) {
					delete(s.creates, id)
					break
				}
			case errors.Is(existing.err, ErrCleanupPartial):
				if recordErr == nil && samePaneGeneration(existing.createdRecord, record) && isTerminalStatus(record.Status) {
					_, removeErr := s.registry.RemovePaneGeneration(record.ID, record.Generation)
					if removeErr == nil || errors.Is(removeErr, registry.ErrNotFound) {
						delete(s.creates, id)
						break
					}
				}
			}
		default:
		}
	}
	if existing, ok := s.creates[id]; ok {
		if !reflect.DeepEqual(existing.request, req) {
			return nil, false, fmt.Errorf("%w: pane id %q was used by a different create request", registry.ErrAlreadyExists, id)
		}
		return existing, false, nil
	}
	call := &createPaneCall{request: req, done: make(chan struct{})}
	s.creates[id] = call
	return call, true, nil
}

func (s *Service) finishCreatePane(call *createPaneCall, response CreatePaneResponse, err error) {
	s.createMu.Lock()
	if response.record.ID != "" {
		call.createdRecord = response.record
	}
	if err == nil {
		call.response = response
	}
	call.err = err
	close(call.done)
	if err != nil && !errors.Is(err, ErrCleanupPartial) {
		delete(s.creates, PaneID(call.request.ID))
	}
	s.createMu.Unlock()
}

func samePaneGeneration(want, got registry.PaneRecord) bool {
	return want.ID != "" &&
		want.ID == got.ID &&
		want.Generation == got.Generation &&
		want.ZellijPaneID == got.ZellijPaneID
}

func (s *Service) createPaneOnce(ctx context.Context, req CreatePaneRequest, id PaneID) (CreatePaneResponse, error) {
	ownershipToken, err := s.newOwnershipToken()
	if err != nil {
		return CreatePaneResponse{}, fmt.Errorf("generate pane ownership token: %w", err)
	}
	zellijID, tabID, tabName, cleanup, err := s.createBackendPane(ctx, req)
	if err != nil {
		return CreatePaneResponse{}, createPaneCleanupError(err, cleanup(ctx))
	}

	var regTabID registry.TabID
	if tabID != nil {
		regTabID = registry.TabID(strconv.Itoa(int(*tabID)))
	} else if tabName != "" {
		regTabID = registry.TabID(tabName)
	}

	record, err := s.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:             registry.PaneID(id),
		OwnershipToken: registry.OwnershipToken(ownershipToken),
		SessionID:      registry.SessionID(req.ZellijSession),
		TabID:          regTabID,
		TaskID:         registry.TaskID(req.TaskID),
		AgentID:        registry.AgentID(req.AgentID),
		ZellijPaneID:   registry.ZellijPaneID(zellijID),
		ZellijTabID:    registryTabID(tabID),
		TabName:        tabName,
		Role:           string(req.Role),
		Command:        cloneStrings(req.Command),
		CWD:            req.CWD,
	})
	if err != nil {
		return CreatePaneResponse{}, createPaneCleanupError(err, cleanup(ctx))
	}

	if s.observer != nil {
		s.observer.PaneOpened(record)
	}
	if s.subs != nil {
		s.subs.StartPane(registry.PaneID(id))
	}

	return CreatePaneResponse{Pane: paneFromRecord(record), record: record}, nil
}

func createPaneCleanupError(cause, cleanupErr error) error {
	if cleanupErr == nil {
		return cause
	}
	return errors.Join(cause, fmt.Errorf("%w: %v", ErrCleanupPartial, cleanupErr))
}

func (s *Service) resolveCreatePaneTarget(ctx context.Context, req CreatePaneRequest) (CreatePaneRequest, error) {
	if req.NewTab && req.ZellijTabID != nil {
		return req, ErrInvalidPaneTarget
	}
	if req.SameTabAsZellijPaneID != "" {
		if req.NewTab || req.ZellijTabID != nil || req.SameTabAsPaneID != "" {
			return req, ErrInvalidPaneTarget
		}

		panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: req.ZellijSession})
		if err != nil {
			return req, err
		}
		matches := make([]zellij.Pane, 0, 1)
		for _, pane := range panes {
			if pane.ID == zellij.PaneID(req.SameTabAsZellijPaneID) {
				matches = append(matches, pane)
			}
		}
		switch len(matches) {
		case 0:
			return req, fmt.Errorf("%w: source Zellij pane %s", ErrPaneNotFound, req.SameTabAsZellijPaneID)
		case 1:
			if matches[0].IsPlugin {
				return req, fmt.Errorf("%w: source Zellij pane %s is a plugin pane", ErrInvalidPaneTarget, req.SameTabAsZellijPaneID)
			}
			tabID := ZellijTabID(matches[0].TabID)
			req.ZellijTabID = &tabID
			return req, nil
		default:
			return req, fmt.Errorf("%w: source Zellij pane %s is ambiguous", ErrInvalidPaneTarget, req.SameTabAsZellijPaneID)
		}
	}
	if req.SameTabAsPaneID == "" {
		return req, nil
	}
	if req.NewTab || req.ZellijTabID != nil {
		return req, ErrInvalidPaneTarget
	}

	anchor, err := s.lookupPane(req.SameTabAsPaneID)
	if err != nil {
		return req, err
	}
	if anchor.Status != registry.PaneStatusStarting && anchor.Status != registry.PaneStatusRunning {
		return req, fmt.Errorf("%w: anchor %s is %s", ErrInvalidPaneTarget, anchor.ID, anchor.Status)
	}
	if string(anchor.SessionID) != req.ZellijSession {
		return req, fmt.Errorf("%w: anchor %s belongs to zellij session %q", ErrInvalidPaneTarget, anchor.ID, anchor.SessionID)
	}
	if anchor.ZellijTabID == nil {
		return req, fmt.Errorf("%w: anchor %s has no tab", ErrInvalidPaneTarget, anchor.ID)
	}

	tabID := ZellijTabID(*anchor.ZellijTabID)
	req.ZellijTabID = &tabID
	return req, nil
}

func (s *Service) FocusPane(ctx context.Context, req FocusPaneRequest) (FocusPaneResponse, error) {
	record, err := s.lookupPane(req.PaneID)
	if err != nil {
		return FocusPaneResponse{}, err
	}
	if record.Status != registry.PaneStatusStarting && record.Status != registry.PaneStatusRunning {
		return FocusPaneResponse{}, fmt.Errorf("%w: pane %s is %s", ErrInvalidPaneTarget, record.ID, record.Status)
	}

	sourceSession := strings.TrimSpace(req.SourceZellijSession)
	sourcePaneID := strings.TrimSpace(string(req.SourceZellijPaneID))
	targetSession := strings.TrimSpace(string(record.SessionID))
	targetPaneID := strings.TrimSpace(string(record.ZellijPaneID))
	if sourceSession == "" || sourcePaneID == "" {
		return FocusPaneResponse{}, fmt.Errorf("%w: source Zellij context is required", ErrInvalidPaneTarget)
	}
	if targetSession == "" || targetPaneID == "" {
		return FocusPaneResponse{}, fmt.Errorf("%w: pane %s has no Zellij target", ErrInvalidPaneTarget, record.ID)
	}
	if s.switcher == nil {
		return FocusPaneResponse{}, fmt.Errorf("%w: session switcher is not configured", ErrInvalidPaneTarget)
	}

	err = s.switcher.SwitchSession(ctx, zellij.SwitchSessionRequest{
		SourceSession: sourceSession,
		SourcePaneID:  zellij.PaneID(sourcePaneID),
		TargetSession: targetSession,
		TargetPaneID:  zellij.PaneID(targetPaneID),
	})
	if err != nil {
		return FocusPaneResponse{}, err
	}
	return FocusPaneResponse{Pane: paneFromRecord(record)}, nil
}

func (s *Service) FocusSession(ctx context.Context, req FocusSessionRequest) (FocusSessionResponse, error) {
	sourceSession := strings.TrimSpace(req.SourceZellijSession)
	sourcePaneID := strings.TrimSpace(string(req.SourceZellijPaneID))
	targetSession := strings.TrimSpace(req.SessionID)
	if sourceSession == "" || sourcePaneID == "" || targetSession == "" {
		return FocusSessionResponse{}, fmt.Errorf("%w: source and target Zellij context is required", ErrInvalidPaneTarget)
	}
	if s.switcher == nil {
		return FocusSessionResponse{}, fmt.Errorf("%w: session switcher is not configured", ErrInvalidPaneTarget)
	}

	panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: targetSession})
	if err != nil {
		return FocusSessionResponse{}, err
	}
	targetPaneID := focusedTerminalPaneID(panes)
	if targetPaneID == "" {
		return FocusSessionResponse{}, fmt.Errorf("%w: session %s has no active terminal pane", ErrPaneNotFound, targetSession)
	}
	if err := s.switcher.SwitchSession(ctx, zellij.SwitchSessionRequest{
		SourceSession: sourceSession,
		SourcePaneID:  zellij.PaneID(sourcePaneID),
		TargetSession: targetSession,
		TargetPaneID:  targetPaneID,
	}); err != nil {
		return FocusSessionResponse{}, err
	}
	return FocusSessionResponse{SessionID: targetSession, ZellijPaneID: ZellijPaneID(targetPaneID)}, nil
}

func focusedTerminalPaneID(panes []zellij.Pane) zellij.PaneID {
	var fallback zellij.PaneID
	for _, pane := range panes {
		if pane.IsPlugin || pane.Exited || pane.ID == "" {
			continue
		}
		if fallback == "" {
			fallback = pane.ID
		}
		if pane.IsFocused {
			return pane.ID
		}
	}
	return fallback
}

// SubscribeEvents exposes in-process runtime observations published by the daemon.
func (s *Service) SubscribeEvents(ctx context.Context) (<-chan eventbus.Event, func(), error) {
	if s.bus == nil {
		return nil, nil, errors.New("runtime: event bus not configured")
	}
	ch, unsub := s.bus.Subscribe(ctx)
	return ch, unsub, nil
}

func (s *Service) createBackendPane(ctx context.Context, req CreatePaneRequest) (zellij.PaneID, *zellij.TabID, string, func(context.Context) error, error) {
	if req.NewTab {
		tabID, err := s.backend.CreateTab(ctx, zellij.CreateTabRequest{
			Session:      req.ZellijSession,
			Name:         req.TabName,
			CWD:          req.CWD,
			LayoutString: req.LayoutString,
			Command:      cloneStrings(req.Command),
		})
		if err != nil {
			return "", nil, "", nilCleanup, err
		}

		pane, err := s.findPaneInTab(ctx, req.ZellijSession, tabID)
		if err != nil {
			return "", nil, "", func(ctx context.Context) error {
				return s.backend.CloseTab(ctx, zellij.CloseTabRequest{Session: req.ZellijSession, TabID: &tabID})
			}, err
		}

		tabName := req.TabName
		if tabName == "" {
			tabName = pane.TabName
		}

		return pane.ID, &tabID, tabName, func(ctx context.Context) error {
			return s.backend.CloseTab(ctx, zellij.CloseTabRequest{Session: req.ZellijSession, TabID: &tabID})
		}, nil
	}

	var targetTabID *zellij.TabID
	if req.ZellijTabID != nil {
		tabID := zellij.TabID(*req.ZellijTabID)
		targetTabID = &tabID
	}

	zellijID, err := s.backend.CreatePane(ctx, zellij.CreatePaneRequest{
		Session: req.ZellijSession,
		Name:    req.Name,
		CWD:     req.CWD,
		TabID:   targetTabID,
		Command: cloneStrings(req.Command),
	})
	if err != nil {
		return "", nil, "", nilCleanup, err
	}

	tabID := targetTabID
	tabName := req.TabName
	if pane, err := s.findPaneByID(ctx, req.ZellijSession, zellijID); err == nil {
		discoveredTabID := zellij.TabID(pane.TabID)
		tabID = &discoveredTabID
		tabName = pane.TabName
	}

	return zellijID, tabID, tabName, func(ctx context.Context) error {
		return s.backend.ClosePane(ctx, zellij.ClosePaneRequest{Session: req.ZellijSession, PaneID: zellijID})
	}, nil
}

func (s *Service) SendInput(ctx context.Context, req SendInputRequest) error {
	record, err := s.lookupPane(req.PaneID)
	if err != nil {
		return err
	}

	err = s.backend.SendInput(ctx, zellij.SendInputRequest{
		Session: string(record.SessionID),
		PaneID:  zellij.PaneID(record.ZellijPaneID),
		Text:    req.Text,
	})
	if err != nil {
		_, _ = s.registry.UpdatePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusError, err.Error())
		return err
	}
	return nil
}

func (s *Service) SendMessage(ctx context.Context, req SendMessageRequest) (SendMessageResponse, error) {
	if req.FromPaneID == "" {
		return SendMessageResponse{}, fmt.Errorf("%w: from pane id is required", ErrInvalidMessage)
	}
	if req.ToPaneID == "" {
		return SendMessageResponse{}, fmt.Errorf("%w: to pane id is required", ErrInvalidMessage)
	}

	fromRecord, err := s.lookupPane(req.FromPaneID)
	if err != nil {
		return SendMessageResponse{}, err
	}
	toRecord, err := s.lookupPane(req.ToPaneID)
	if err != nil {
		return SendMessageResponse{}, err
	}
	if !sameManagedTab(fromRecord, toRecord) {
		return SendMessageResponse{}, fmt.Errorf("%w: panes %s and %s are not in the same tab", ErrInvalidMessage, req.FromPaneID, req.ToPaneID)
	}

	messageType := strings.TrimSpace(req.Type)
	if messageType == "" {
		messageType = "message"
	}
	deliveredText := formatPaneMessage(fromRecord.ID, messageType, req.Body)

	if err := s.backend.SendInput(ctx, zellij.SendInputRequest{
		Session: string(toRecord.SessionID),
		PaneID:  zellij.PaneID(toRecord.ZellijPaneID),
		Text:    deliveredText,
	}); err != nil {
		_, _ = s.registry.UpdatePaneStatusGeneration(toRecord.ID, toRecord.Generation, registry.PaneStatusError, err.Error())
		return SendMessageResponse{}, err
	}

	if s.bus != nil {
		s.bus.Publish(eventbus.Event{
			Type:         eventbus.TypeMessageSent,
			PaneID:       string(toRecord.ID),
			TaskID:       string(toRecord.TaskID),
			AgentID:      string(toRecord.AgentID),
			ZellijPaneID: string(toRecord.ZellijPaneID),
			Message:      fmt.Sprintf("from=%s to=%s type=%s", fromRecord.ID, toRecord.ID, messageType),
			Time:         time.Now(),
		})
	}

	return SendMessageResponse{
		From:          paneFromRecord(fromRecord),
		To:            paneFromRecord(toRecord),
		Type:          messageType,
		Body:          req.Body,
		DeliveredText: deliveredText,
	}, nil
}

func (s *Service) ListPanes(context.Context) (ListPanesResponse, error) {
	records := s.registry.ListPanes()
	panes := make([]Pane, 0, len(records))
	for _, record := range records {
		panes = append(panes, paneFromRecord(record))
	}
	return ListPanesResponse{Panes: panes}, nil
}

func (s *Service) InspectPane(_ context.Context, req InspectPaneRequest) (InspectPaneResponse, error) {
	record, err := s.lookupPane(req.PaneID)
	if err != nil {
		return InspectPaneResponse{}, err
	}

	return InspectPaneResponse{Pane: paneFromRecord(record)}, nil
}

func (s *Service) SnapshotOutput(ctx context.Context, req SnapshotOutputRequest) (SnapshotOutputResponse, error) {
	record, err := s.lookupPane(req.PaneID)
	if err != nil {
		return SnapshotOutputResponse{}, err
	}

	output, err := s.backend.DumpScreen(ctx, zellij.DumpScreenRequest{
		Session: string(record.SessionID),
		PaneID:  zellij.PaneID(record.ZellijPaneID),
		Full:    req.Full,
		ANSI:    req.ANSI,
	})
	if err != nil {
		_, _ = s.registry.UpdatePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusError, err.Error())
		return SnapshotOutputResponse{}, err
	}

	record, err = s.registry.UpdatePaneOutputGeneration(record.ID, record.Generation, output)
	if err != nil {
		return SnapshotOutputResponse{}, err
	}

	return SnapshotOutputResponse{
		Pane:   paneFromRecord(record),
		Output: output,
	}, nil
}

func (s *Service) ClosePane(ctx context.Context, req ClosePaneRequest) (ClosePaneResponse, error) {
	record, err := s.lookupPane(req.PaneID)
	if err != nil {
		return ClosePaneResponse{}, err
	}

	err = s.backend.ClosePane(ctx, zellij.ClosePaneRequest{
		Session: string(record.SessionID),
		PaneID:  zellij.PaneID(record.ZellijPaneID),
	})
	if err != nil {
		updated, _, updateErr := s.registry.UpdateActivePaneStatusGeneration(record.ID, record.Generation, registry.PaneStatusError, err.Error())
		if updateErr != nil {
			return ClosePaneResponse{}, errors.Join(err, updateErr)
		}
		return ClosePaneResponse{Pane: paneFromRecord(updated)}, err
	}

	updated, claimed, err := s.registry.ClaimPaneClosureGeneration(record.ID, record.Generation, registry.PaneStatusClosed, "closed by runtime service")
	if err != nil {
		return ClosePaneResponse{}, err
	}

	if s.subs != nil {
		s.subs.StopPaneGeneration(record.ID, record.Generation)
	}
	if claimed && s.observer != nil {
		s.observer.PaneClosed(updated)
	}

	return ClosePaneResponse{Pane: paneFromRecord(updated)}, nil
}

func (s *Service) lookupPane(id PaneID) (registry.PaneRecord, error) {
	if id == "" {
		return registry.PaneRecord{}, ErrMissingPaneID
	}

	record, err := s.registry.GetPane(registry.PaneID(id))
	if errors.Is(err, registry.ErrNotFound) {
		return registry.PaneRecord{}, ErrPaneNotFound
	}
	if err != nil {
		return registry.PaneRecord{}, err
	}
	if record.ZellijPaneID == "" {
		return registry.PaneRecord{}, fmt.Errorf("%w: pane %s has no zellij pane id", ErrPaneNotFound, id)
	}

	return record, nil
}

func sameManagedTab(a, b registry.PaneRecord) bool {
	if a.SessionID == "" || a.SessionID != b.SessionID {
		return false
	}
	if a.ZellijTabID != nil && b.ZellijTabID != nil {
		return *a.ZellijTabID == *b.ZellijTabID
	}
	return a.TabID != "" && a.TabID == b.TabID
}

func formatPaneMessage(from registry.PaneID, messageType, body string) string {
	text := fmt.Sprintf("[agentd] message from=%s type=%s\n%s", from, messageType, body)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

func (s *Service) findPaneByID(ctx context.Context, session string, paneID zellij.PaneID) (zellij.Pane, error) {
	panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: session})
	if err != nil {
		return zellij.Pane{}, err
	}
	for _, pane := range panes {
		if pane.ID == paneID {
			return pane, nil
		}
	}
	return zellij.Pane{}, ErrPaneNotFound
}

func (s *Service) findPaneInTab(ctx context.Context, session string, tabID zellij.TabID) (zellij.Pane, error) {
	panes, err := s.backend.ListPanes(ctx, zellij.ListPanesRequest{Session: session})
	if err != nil {
		return zellij.Pane{}, err
	}
	for _, pane := range panes {
		if zellij.TabID(pane.TabID) == tabID && !pane.IsPlugin {
			return pane, nil
		}
	}
	return zellij.Pane{}, ErrPaneNotFound
}

func nilCleanup(context.Context) error {
	return nil
}

func registryTabID(value *zellij.TabID) *registry.ZellijTabID {
	if value == nil {
		return nil
	}

	tabID := registry.ZellijTabID(*value)
	return &tabID
}

func runtimeTabID(value *registry.ZellijTabID) *ZellijTabID {
	if value == nil {
		return nil
	}

	tabID := ZellijTabID(*value)
	return &tabID
}

func sequentialPaneIDGenerator() PaneIDGenerator {
	var next uint64
	return func() PaneID {
		return PaneID(fmt.Sprintf("pane-%d", atomic.AddUint64(&next, 1)))
	}
}

func paneFromRecord(record registry.PaneRecord) Pane {
	return Pane{
		ID:             PaneID(record.ID),
		OwnershipToken: OwnershipToken(record.OwnershipToken),
		SessionID:      SessionID(record.SessionID),
		TabID:          TabID(record.TabID),
		TaskID:         TaskID(record.TaskID),
		AgentID:        AgentID(record.AgentID),
		ZellijPaneID:   ZellijPaneID(record.ZellijPaneID),
		ZellijTabID:    runtimeTabID(record.ZellijTabID),
		TabName:        record.TabName,
		Role:           record.Role,
		Command:        cloneStrings(record.Command),
		CWD:            record.CWD,
		Status:         PaneStatus(record.Status),
		LastOutput:     record.LastOutput,
		StatusMessage:  record.StatusMessage,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}
}

func randomOwnershipToken() (OwnershipToken, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return OwnershipToken(hex.EncodeToString(raw[:])), nil
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}

	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}

func (s *Service) ListSessions(ctx context.Context) ([]SessionRecord, error) {
	return s.registry.ListSessions(), nil
}

func (s *Service) GetSession(ctx context.Context, id SessionID) (SessionRecord, error) {
	session, err := s.registry.GetSession(registry.SessionID(id))
	if errors.Is(err, registry.ErrNotFound) {
		return SessionRecord{}, ErrSessionNotFound
	}
	return session, err
}

func (s *Service) ListTabs(ctx context.Context, sessionID SessionID) ([]TabRecord, error) {
	tabs, err := s.registry.ListTabs(registry.SessionID(sessionID))
	if errors.Is(err, registry.ErrNotFound) {
		return nil, ErrSessionNotFound
	}
	return tabs, err
}

func (s *Service) GetTab(ctx context.Context, sessionID SessionID, tabID TabID) (TabRecord, error) {
	tab, err := s.registry.GetTab(registry.SessionID(sessionID), registry.TabID(tabID))
	if errors.Is(err, registry.ErrNotFound) {
		if _, sessErr := s.registry.GetSession(registry.SessionID(sessionID)); errors.Is(sessErr, registry.ErrNotFound) {
			return TabRecord{}, ErrSessionNotFound
		}
		return TabRecord{}, ErrTabNotFound
	}
	return tab, err
}
