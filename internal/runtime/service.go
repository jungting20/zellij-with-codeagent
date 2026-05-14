package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"zellij-with-codeagent/internal/registry"
	"zellij-with-codeagent/internal/zellij"
)

type PaneIDGenerator func() PaneID

type Options struct {
	Registry  *registry.Registry
	Backend   zellij.Backend
	NewPaneID PaneIDGenerator
}

type Service struct {
	registry  *registry.Registry
	backend   zellij.Backend
	newPaneID PaneIDGenerator
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

	newPaneID := opts.NewPaneID
	if newPaneID == nil {
		newPaneID = sequentialPaneIDGenerator()
	}

	return &Service{
		registry:  reg,
		backend:   backend,
		newPaneID: newPaneID,
	}
}

func (s *Service) CreatePane(ctx context.Context, req CreatePaneRequest) (CreatePaneResponse, error) {
	id := req.ID
	if id == "" {
		id = s.newPaneID()
	}
	if id == "" {
		return CreatePaneResponse{}, ErrMissingPaneID
	}

	zellijID, tabID, tabName, cleanup, err := s.createBackendPane(ctx, req)
	if err != nil {
		return CreatePaneResponse{}, errors.Join(err, cleanup(ctx))
	}

	record, err := s.registry.RegisterPane(registry.RegisterPaneRequest{
		ID:           registry.PaneID(id),
		TaskID:       registry.TaskID(req.TaskID),
		AgentID:      registry.AgentID(req.AgentID),
		ZellijPaneID: registry.ZellijPaneID(zellijID),
		ZellijTabID:  registryTabID(tabID),
		TabName:      tabName,
		Role:         registry.PaneRole(req.Role),
		Command:      cloneStrings(req.Command),
		CWD:          req.CWD,
	})
	if err != nil {
		return CreatePaneResponse{}, errors.Join(err, cleanup(ctx))
	}

	return CreatePaneResponse{Pane: paneFromRecord(record)}, nil
}

func (s *Service) createBackendPane(ctx context.Context, req CreatePaneRequest) (zellij.PaneID, *zellij.TabID, string, func(context.Context) error, error) {
	if req.NewTab {
		tabID, err := s.backend.CreateTab(ctx, zellij.CreateTabRequest{
			Name:    req.TabName,
			CWD:     req.CWD,
			Command: cloneStrings(req.Command),
		})
		if err != nil {
			return "", nil, "", nilCleanup, err
		}

		pane, err := s.findPaneInTab(ctx, tabID)
		if err != nil {
			return "", nil, "", func(ctx context.Context) error {
				return s.backend.CloseTab(ctx, zellij.CloseTabRequest{TabID: &tabID})
			}, err
		}

		tabName := req.TabName
		if tabName == "" {
			tabName = pane.TabName
		}

		return pane.ID, &tabID, tabName, func(ctx context.Context) error {
			return s.backend.CloseTab(ctx, zellij.CloseTabRequest{TabID: &tabID})
		}, nil
	}

	var targetTabID *zellij.TabID
	if req.ZellijTabID != nil {
		tabID := zellij.TabID(*req.ZellijTabID)
		targetTabID = &tabID
	}

	zellijID, err := s.backend.CreatePane(ctx, zellij.CreatePaneRequest{
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
	if pane, err := s.findPaneByID(ctx, zellijID); err == nil {
		discoveredTabID := zellij.TabID(pane.TabID)
		tabID = &discoveredTabID
		tabName = pane.TabName
	}

	return zellijID, tabID, tabName, func(ctx context.Context) error {
		return s.backend.ClosePane(ctx, zellij.ClosePaneRequest{PaneID: zellijID})
	}, nil
}

func (s *Service) SendInput(ctx context.Context, req SendInputRequest) error {
	record, err := s.lookupPane(req.PaneID)
	if err != nil {
		return err
	}

	err = s.backend.SendInput(ctx, zellij.SendInputRequest{
		PaneID: zellij.PaneID(record.ZellijPaneID),
		Text:   req.Text,
	})
	if err != nil {
		_, _ = s.registry.UpdatePaneStatus(registry.PaneID(req.PaneID), registry.PaneStatusError, err.Error())
		return err
	}
	return nil
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
		PaneID: zellij.PaneID(record.ZellijPaneID),
		Full:   req.Full,
		ANSI:   req.ANSI,
	})
	if err != nil {
		_, _ = s.registry.UpdatePaneStatus(registry.PaneID(req.PaneID), registry.PaneStatusError, err.Error())
		return SnapshotOutputResponse{}, err
	}

	record, err = s.registry.UpdatePaneOutput(registry.PaneID(req.PaneID), output)
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
		PaneID: zellij.PaneID(record.ZellijPaneID),
	})
	if err != nil {
		updated, updateErr := s.registry.UpdatePaneStatus(registry.PaneID(req.PaneID), registry.PaneStatusError, err.Error())
		if updateErr != nil {
			return ClosePaneResponse{}, errors.Join(err, updateErr)
		}
		return ClosePaneResponse{Pane: paneFromRecord(updated)}, err
	}

	updated, err := s.registry.UpdatePaneStatus(registry.PaneID(req.PaneID), registry.PaneStatusClosed, "closed by runtime service")
	if err != nil {
		return ClosePaneResponse{}, err
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

func (s *Service) findPaneByID(ctx context.Context, paneID zellij.PaneID) (zellij.Pane, error) {
	panes, err := s.backend.ListPanes(ctx)
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

func (s *Service) findPaneInTab(ctx context.Context, tabID zellij.TabID) (zellij.Pane, error) {
	panes, err := s.backend.ListPanes(ctx)
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
		ID:            PaneID(record.ID),
		TaskID:        TaskID(record.TaskID),
		AgentID:       AgentID(record.AgentID),
		ZellijPaneID:  ZellijPaneID(record.ZellijPaneID),
		ZellijTabID:   runtimeTabID(record.ZellijTabID),
		TabName:       record.TabName,
		Role:          PaneRole(record.Role),
		Command:       cloneStrings(record.Command),
		CWD:           record.CWD,
		Status:        PaneStatus(record.Status),
		LastOutput:    record.LastOutput,
		StatusMessage: record.StatusMessage,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}

	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}
