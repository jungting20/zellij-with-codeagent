package supervisor

import (
	"context"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

type ViewOptions struct {
	EventLimit int
	EventTypes []eventbus.EventType
}

type View struct {
	Message      string
	Counts       rt.RuntimeCounts
	Panes        []rt.Pane
	Tasks        []rt.TaskPaneGroup
	Roles        []rt.RolePaneGroup
	Outputs      []rt.PaneOutputSummary
	RecentEvents []rt.EventSummary
}

type ViewService interface {
	rt.RuntimeInspectionService
	rt.EventHistoryService
}

func BuildView(ctx context.Context, service ViewService, opts ViewOptions) (View, error) {
	status, err := service.InspectRuntime(ctx, rt.InspectRuntimeRequest{})
	if err != nil {
		return View{}, err
	}

	events, err := service.RecentEvents(ctx, rt.RecentEventsRequest{
		Limit: opts.EventLimit,
		Types: opts.EventTypes,
	})
	if err != nil {
		return View{}, err
	}

	return View{
		Message:      status.Message,
		Counts:       status.Counts,
		Panes:        status.Panes,
		Tasks:        status.Tasks,
		Roles:        status.Roles,
		Outputs:      status.Outputs,
		RecentEvents: events.Events,
	}, nil
}
