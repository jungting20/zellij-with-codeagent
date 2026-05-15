package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"zellij-with-codeagent/internal/eventbus"
)

func (s *Service) InspectRuntime(context.Context, InspectRuntimeRequest) (InspectRuntimeResponse, error) {
	records := s.registry.ListPanes()
	panes := make([]Pane, 0, len(records))
	for _, record := range records {
		panes = append(panes, paneFromRecord(record))
	}

	response := InspectRuntimeResponse{
		Message: runtimeStatusMessage(len(panes)),
		Counts:  countPanes(panes),
		Panes:   panes,
		Tasks:   groupPanesByTask(panes),
		Roles:   groupPanesByRole(panes),
		Outputs: outputSummaries(panes),
	}
	return response, nil
}

func (s *Service) RecentEvents(_ context.Context, req RecentEventsRequest) (RecentEventsResponse, error) {
	if s.bus == nil {
		return RecentEventsResponse{}, errors.New("runtime: event bus not configured")
	}

	typeFilter := eventTypeFilter(req.Types)
	events := s.bus.Recent(0)
	summaries := make([]EventSummary, 0, len(events))
	for _, event := range events {
		if len(typeFilter) > 0 && !typeFilter[event.Type] {
			continue
		}
		summaries = append(summaries, eventSummary(event))
	}

	if req.Limit > 0 && req.Limit < len(summaries) {
		summaries = summaries[len(summaries)-req.Limit:]
	}

	return RecentEventsResponse{Events: summaries}, nil
}

func runtimeStatusMessage(panes int) string {
	if panes == 0 {
		return "no managed panes"
	}
	return fmt.Sprintf("%d managed pane(s)", panes)
}

func countPanes(panes []Pane) RuntimeCounts {
	counts := RuntimeCounts{Managed: len(panes)}
	for _, pane := range panes {
		switch pane.Status {
		case PaneStatusStarting:
			counts.Starting++
			counts.Active++
		case PaneStatusRunning:
			counts.Running++
			counts.Active++
		case PaneStatusExited:
			counts.Exited++
			counts.Terminal++
		case PaneStatusClosed:
			counts.Closed++
			counts.Terminal++
		case PaneStatusLost:
			counts.Lost++
			counts.Terminal++
		case PaneStatusError:
			counts.Error++
		}
	}
	return counts
}

func groupPanesByTask(panes []Pane) []TaskPaneGroup {
	byTask := make(map[TaskID][]Pane)
	for _, pane := range panes {
		byTask[pane.TaskID] = append(byTask[pane.TaskID], pane)
	}

	keys := make([]string, 0, len(byTask))
	for taskID := range byTask {
		keys = append(keys, string(taskID))
	}
	sort.Strings(keys)

	groups := make([]TaskPaneGroup, 0, len(keys))
	for _, key := range keys {
		taskID := TaskID(key)
		groups = append(groups, TaskPaneGroup{
			TaskID: taskID,
			Panes:  clonePanes(byTask[taskID]),
		})
	}
	return groups
}

func groupPanesByRole(panes []Pane) []RolePaneGroup {
	byRole := make(map[PaneRole][]Pane)
	for _, pane := range panes {
		role := pane.Role
		if role == "" {
			role = PaneRoleUnknown
		}
		byRole[role] = append(byRole[role], pane)
	}

	keys := make([]string, 0, len(byRole))
	for role := range byRole {
		keys = append(keys, string(role))
	}
	sort.Strings(keys)

	groups := make([]RolePaneGroup, 0, len(keys))
	for _, key := range keys {
		role := PaneRole(key)
		groups = append(groups, RolePaneGroup{
			Role:  role,
			Panes: clonePanes(byRole[role]),
		})
	}
	return groups
}

func outputSummaries(panes []Pane) []PaneOutputSummary {
	summaries := make([]PaneOutputSummary, 0, len(panes))
	for _, pane := range panes {
		summaries = append(summaries, PaneOutputSummary{
			PaneID:     pane.ID,
			TaskID:     pane.TaskID,
			Role:       pane.Role,
			Status:     pane.Status,
			LastOutput: pane.LastOutput,
			UpdatedAt:  pane.UpdatedAt,
		})
	}
	return summaries
}

func eventTypeFilter(types []eventbus.EventType) map[eventbus.EventType]bool {
	if len(types) == 0 {
		return nil
	}
	filter := make(map[eventbus.EventType]bool, len(types))
	for _, eventType := range types {
		filter[eventType] = true
	}
	return filter
}

func eventSummary(event eventbus.Event) EventSummary {
	return EventSummary{
		Type:         event.Type,
		PaneID:       PaneID(event.PaneID),
		TaskID:       TaskID(event.TaskID),
		AgentID:      AgentID(event.AgentID),
		ZellijPaneID: ZellijPaneID(event.ZellijPaneID),
		Message:      event.Message,
		Time:         event.Time,
	}
}

func clonePanes(panes []Pane) []Pane {
	if panes == nil {
		return nil
	}
	clone := make([]Pane, len(panes))
	for i, pane := range panes {
		clone[i] = pane
		clone[i].Command = cloneStrings(pane.Command)
	}
	return clone
}
