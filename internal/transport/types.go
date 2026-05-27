package transport

import (
	"encoding/json"
	"sort"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

type CreatePaneRequest struct {
	ID          string   `json:"id,omitempty"`
	TaskID      string   `json:"task_id,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
	Role        string   `json:"role,omitempty"`
	Name        string   `json:"name,omitempty"`
	NewTab      bool     `json:"new_tab,omitempty"`
	TabName     string   `json:"tab_name,omitempty"`
	ZellijTabID *int     `json:"zellij_tab_id,omitempty"`
	Command     []string `json:"command,omitempty"`
	CWD         string   `json:"cwd,omitempty"`
}

type CreatePaneResponse struct {
	Pane Pane `json:"pane"`
}

type SendInputRequest struct {
	Text string `json:"text"`
}

type SnapshotOutputRequest struct {
	Full bool `json:"full,omitempty"`
	ANSI bool `json:"ansi,omitempty"`
}

type SnapshotOutputResponse struct {
	Pane   Pane   `json:"pane"`
	Output string `json:"output"`
}

type ListPanesResponse struct {
	Panes []Pane `json:"panes"`
}

type InspectRuntimeResponse struct {
	Message string              `json:"message"`
	Counts  RuntimeCounts       `json:"counts"`
	Panes   []Pane              `json:"panes"`
	Tasks   []TaskPaneGroup     `json:"tasks"`
	Roles   []RolePaneGroup     `json:"roles"`
	Outputs []PaneOutputSummary `json:"outputs"`
}

type RuntimeCounts struct {
	Managed  int `json:"managed"`
	Starting int `json:"starting"`
	Running  int `json:"running"`
	Exited   int `json:"exited"`
	Closed   int `json:"closed"`
	Lost     int `json:"lost"`
	Error    int `json:"error"`
	Active   int `json:"active"`
	Terminal int `json:"terminal"`
}

type TaskPaneGroup struct {
	TaskID string `json:"task_id"`
	Panes  []Pane `json:"panes"`
}

type RolePaneGroup struct {
	Role  string `json:"role"`
	Panes []Pane `json:"panes"`
}

type PaneOutputSummary struct {
	PaneID     string    `json:"pane_id"`
	TaskID     string    `json:"task_id,omitempty"`
	Role       string    `json:"role,omitempty"`
	Status     string    `json:"status"`
	LastOutput string    `json:"last_output,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type RecentEventsResponse struct {
	Events []Event `json:"events"`
}

type ReconcileResponse struct {
	Panes     []Pane   `json:"panes"`
	Active    []Pane   `json:"active"`
	Exited    []Pane   `json:"exited"`
	Lost      []Pane   `json:"lost"`
	Unmanaged []string `json:"unmanaged"`
}

type CleanupRequest struct {
	PaneIDs []string `json:"pane_ids,omitempty"`
	TaskID  string   `json:"task_id,omitempty"`
	Role    string   `json:"role,omitempty"`
}

type CleanupResponse struct {
	Closed  []Pane           `json:"closed"`
	Failed  []CleanupFailure `json:"failed"`
	Skipped []Pane           `json:"skipped"`
}

type CleanupFailure struct {
	Pane  Pane   `json:"pane"`
	Error string `json:"error"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

const RequestTypeExecutionPlan = "execution_plan"

type RequestEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

type ExecutionPlanPayload struct {
	Session string             `json:"session"`
	Layout  string             `json:"layout"`
	Tabs    []ExecutionPlanTab `json:"tabs"`
}

type ExecutionPlanTab struct {
	Name  string              `json:"name"`
	Panes []ExecutionPlanPane `json:"panes"`
}

type ExecutionPlanPane struct {
	ID      string   `json:"id"`
	Role    string   `json:"role,omitempty"`
	AgentID string   `json:"agent_id,omitempty"`
	Command []string `json:"command,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
}

type ExecutionPlanResponse struct {
	RequestID string                     `json:"request_id"`
	Session   string                     `json:"session"`
	Layout    string                     `json:"layout"`
	Tabs      []ExecutionPlanTabResponse `json:"tabs"`
}

type ExecutionPlanTabResponse struct {
	Name  string `json:"name"`
	Panes []Pane `json:"panes"`
}

type Pane struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id,omitempty"`
	TabID         string    `json:"tab_id,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	AgentID       string    `json:"agent_id,omitempty"`
	ZellijPaneID  string    `json:"zellij_pane_id,omitempty"`
	ZellijTabID   *int      `json:"zellij_tab_id,omitempty"`
	TabName       string    `json:"tab_name,omitempty"`
	Role          string    `json:"role,omitempty"`
	Command       []string  `json:"command,omitempty"`
	CWD           string    `json:"cwd,omitempty"`
	Status        string    `json:"status"`
	LastOutput    string    `json:"last_output,omitempty"`
	StatusMessage string    `json:"status_message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Event struct {
	Type         string    `json:"type"`
	PaneID       string    `json:"pane_id,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	ZellijPaneID string    `json:"zellij_pane_id,omitempty"`
	Message      string    `json:"message,omitempty"`
	Time         time.Time `json:"time"`
}

func (req CreatePaneRequest) ToRuntime() rt.CreatePaneRequest {
	var tabID *rt.ZellijTabID
	if req.ZellijTabID != nil {
		value := rt.ZellijTabID(*req.ZellijTabID)
		tabID = &value
	}
	return rt.CreatePaneRequest{
		ID:          rt.PaneID(req.ID),
		TaskID:      rt.TaskID(req.TaskID),
		AgentID:     rt.AgentID(req.AgentID),
		Role:        req.Role,
		Name:        req.Name,
		NewTab:      req.NewTab,
		TabName:     req.TabName,
		ZellijTabID: tabID,
		Command:     cloneStrings(req.Command),
		CWD:         req.CWD,
	}
}

func RuntimeCreatePaneRequest(req CreatePaneRequest) rt.CreatePaneRequest {
	return req.ToRuntime()
}

func (req CleanupRequest) ToRuntime() rt.CleanupRequest {
	paneIDs := make([]rt.PaneID, 0, len(req.PaneIDs))
	for _, id := range req.PaneIDs {
		if id != "" {
			paneIDs = append(paneIDs, rt.PaneID(id))
		}
	}
	return rt.CleanupRequest{
		PaneIDs: paneIDs,
		TaskID:  rt.TaskID(req.TaskID),
		Role:    req.Role,
	}
}

func RuntimeCleanupRequest(req CleanupRequest) rt.CleanupRequest {
	return req.ToRuntime()
}

func PaneFromRuntime(pane rt.Pane) Pane {
	var tabID *int
	if pane.ZellijTabID != nil {
		value := int(*pane.ZellijTabID)
		tabID = &value
	}
	return Pane{
		ID:            string(pane.ID),
		SessionID:     string(pane.SessionID),
		TabID:         string(pane.TabID),
		TaskID:        string(pane.TaskID),
		AgentID:       string(pane.AgentID),
		ZellijPaneID:  string(pane.ZellijPaneID),
		ZellijTabID:   tabID,
		TabName:       pane.TabName,
		Role:          string(pane.Role),
		Command:       cloneStrings(pane.Command),
		CWD:           pane.CWD,
		Status:        string(pane.Status),
		LastOutput:    pane.LastOutput,
		StatusMessage: pane.StatusMessage,
		CreatedAt:     pane.CreatedAt,
		UpdatedAt:     pane.UpdatedAt,
	}
}

func PanesFromRuntime(panes []rt.Pane) []Pane {
	out := make([]Pane, 0, len(panes))
	for _, pane := range panes {
		out = append(out, PaneFromRuntime(pane))
	}
	return out
}

func RuntimeStatusFromRuntime(response rt.InspectRuntimeResponse) InspectRuntimeResponse {
	return InspectRuntimeResponse{
		Message: response.Message,
		Counts:  RuntimeCounts(response.Counts),
		Panes:   PanesFromRuntime(response.Panes),
		Tasks:   taskGroupsFromRuntime(response.Tasks),
		Roles:   roleGroupsFromRuntime(response.Roles),
		Outputs: outputSummariesFromRuntime(response.Outputs),
	}
}

func ReconcileFromRuntime(response rt.ReconcileResponse) ReconcileResponse {
	unmanaged := make([]string, 0, len(response.Unmanaged))
	for _, id := range response.Unmanaged {
		unmanaged = append(unmanaged, string(id))
	}
	return ReconcileResponse{
		Panes:     PanesFromRuntime(response.Panes),
		Active:    PanesFromRuntime(response.Active),
		Exited:    PanesFromRuntime(response.Exited),
		Lost:      PanesFromRuntime(response.Lost),
		Unmanaged: unmanaged,
	}
}

func CleanupFromRuntime(response rt.CleanupResponse) CleanupResponse {
	failures := make([]CleanupFailure, 0, len(response.Failed))
	for _, failure := range response.Failed {
		failures = append(failures, CleanupFailure{
			Pane:  PaneFromRuntime(failure.Pane),
			Error: failure.Error,
		})
	}
	return CleanupResponse{
		Closed:  PanesFromRuntime(response.Closed),
		Failed:  failures,
		Skipped: PanesFromRuntime(response.Skipped),
	}
}

func EventFromRuntime(event eventbus.Event) Event {
	return Event{
		Type:         string(event.Type),
		PaneID:       event.PaneID,
		TaskID:       event.TaskID,
		AgentID:      event.AgentID,
		ZellijPaneID: event.ZellijPaneID,
		Message:      event.Message,
		Time:         event.Time,
	}
}

func EventSummaryFromRuntime(event rt.EventSummary) Event {
	return Event{
		Type:         string(event.Type),
		PaneID:       string(event.PaneID),
		TaskID:       string(event.TaskID),
		AgentID:      string(event.AgentID),
		ZellijPaneID: string(event.ZellijPaneID),
		Message:      event.Message,
		Time:         event.Time,
	}
}

func EventsFromRuntime(events []rt.EventSummary) []Event {
	out := make([]Event, 0, len(events))
	for _, event := range events {
		out = append(out, EventSummaryFromRuntime(event))
	}
	return out
}

func taskGroupsFromRuntime(groups []rt.TaskPaneGroup) []TaskPaneGroup {
	out := make([]TaskPaneGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, TaskPaneGroup{
			TaskID: string(group.TaskID),
			Panes:  PanesFromRuntime(group.Panes),
		})
	}
	return out
}

func roleGroupsFromRuntime(groups []rt.RolePaneGroup) []RolePaneGroup {
	out := make([]RolePaneGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, RolePaneGroup{
			Role:  string(group.Role),
			Panes: PanesFromRuntime(group.Panes),
		})
	}
	return out
}

func outputSummariesFromRuntime(summaries []rt.PaneOutputSummary) []PaneOutputSummary {
	out := make([]PaneOutputSummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, PaneOutputSummary{
			PaneID:     string(summary.PaneID),
			TaskID:     string(summary.TaskID),
			Role:       string(summary.Role),
			Status:     string(summary.Status),
			LastOutput: summary.LastOutput,
			UpdatedAt:  summary.UpdatedAt,
		})
	}
	return out
}

func (payload ExecutionPlanPayload) ToRuntime(reqID string) rt.ApplyExecutionPlanRequest {
	if payload.Tabs == nil {
		return rt.ApplyExecutionPlanRequest{
			RequestID: reqID,
			Session:   payload.Session,
			Layout:    payload.Layout,
			Tabs:      nil,
		}
	}
	tabs := make([]rt.ExecutionPlanTabSpec, 0, len(payload.Tabs))
	for _, tab := range payload.Tabs {
		tabs = append(tabs, tab.ToRuntime())
	}
	return rt.ApplyExecutionPlanRequest{
		RequestID: reqID,
		Session:   payload.Session,
		Layout:    payload.Layout,
		Tabs:      tabs,
	}
}

func (tab ExecutionPlanTab) ToRuntime() rt.ExecutionPlanTabSpec {
	panes := make([]rt.ExecutionPlanPaneSpec, 0, len(tab.Panes))
	for _, pane := range tab.Panes {
		panes = append(panes, pane.ToRuntime())
	}
	return rt.ExecutionPlanTabSpec{
		Name:  tab.Name,
		Panes: panes,
	}
}

func (pane ExecutionPlanPane) ToRuntime() rt.ExecutionPlanPaneSpec {
	return rt.ExecutionPlanPaneSpec{
		ID:      rt.PaneID(pane.ID),
		Role:    pane.Role,
		AgentID: rt.AgentID(pane.AgentID),
		Command: cloneStrings(pane.Command),
		CWD:     pane.CWD,
	}
}

func RuntimeApplyExecutionPlanRequest(reqID string, payload ExecutionPlanPayload) rt.ApplyExecutionPlanRequest {
	return payload.ToRuntime(reqID)
}

func ExecutionPlanFromRuntime(response rt.ApplyExecutionPlanResponse) ExecutionPlanResponse {
	if response.Tabs == nil {
		return ExecutionPlanResponse{
			RequestID: response.RequestID,
			Session:   response.Session,
			Layout:    response.Layout,
			Tabs:      nil,
		}
	}
	tabs := make([]ExecutionPlanTabResponse, 0, len(response.Tabs))
	for _, tab := range response.Tabs {
		tabs = append(tabs, ExecutionPlanTabResponse{
			Name:  tab.Name,
			Panes: PanesFromRuntime(tab.Panes),
		})
	}
	return ExecutionPlanResponse{
		RequestID: response.RequestID,
		Session:   response.Session,
		Layout:    response.Layout,
		Tabs:      tabs,
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

type Session struct {
	ID        string    `json:"id"`
	Tabs      []Tab     `json:"tabs,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Tab struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Panes     []Pane    `json:"panes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SessionResponse struct {
	Session Session `json:"session"`
}

type SessionListResponse struct {
	Sessions []Session `json:"sessions"`
}

type TabResponse struct {
	Tab Tab `json:"tab"`
}

type TabListResponse struct {
	Tabs []Tab `json:"tabs"`
}

func SessionFromRuntime(rSession rt.SessionRecord) Session {
	tabs := make([]Tab, 0, len(rSession.Tabs))
	for _, tab := range rSession.Tabs {
		tabs = append(tabs, TabFromRuntime(tab))
	}
	sortTabsByID(tabs)
	return Session{
		ID:        string(rSession.ID),
		Tabs:      tabs,
		CreatedAt: rSession.CreatedAt,
		UpdatedAt: rSession.UpdatedAt,
	}
}

func PaneFromRuntimeRecord(pane rt.PaneRecord) Pane {
	var tabID *int
	if pane.ZellijTabID != nil {
		value := int(*pane.ZellijTabID)
		tabID = &value
	}
	return Pane{
		ID:            string(pane.ID),
		SessionID:     string(pane.SessionID),
		TabID:         string(pane.TabID),
		TaskID:        string(pane.TaskID),
		AgentID:       string(pane.AgentID),
		ZellijPaneID:  string(pane.ZellijPaneID),
		ZellijTabID:   tabID,
		TabName:       pane.TabName,
		Role:          string(pane.Role),
		Command:       cloneStrings(pane.Command),
		CWD:           pane.CWD,
		Status:        string(pane.Status),
		LastOutput:    pane.LastOutput,
		StatusMessage: pane.StatusMessage,
		CreatedAt:     pane.CreatedAt,
		UpdatedAt:     pane.UpdatedAt,
	}
}

func TabFromRuntime(rTab rt.TabRecord) Tab {
	return Tab{
		ID:        string(rTab.ID),
		Name:      rTab.Name,
		Panes:     PanesFromRuntimeRecords(rTab.Panes),
		CreatedAt: rTab.CreatedAt,
		UpdatedAt: rTab.UpdatedAt,
	}
}

func PanesFromRuntimeRecords(records map[rt.PaneID]rt.PaneRecord) []Pane {
	panes := make([]Pane, 0, len(records))
	for _, pane := range records {
		panes = append(panes, PaneFromRuntimeRecord(pane))
	}
	sortPanesByID(panes)
	return panes
}

func SessionsFromRuntime(rSessions []rt.SessionRecord) []Session {
	out := make([]Session, 0, len(rSessions))
	for _, s := range rSessions {
		out = append(out, SessionFromRuntime(s))
	}
	return out
}

func TabsFromRuntime(rTabs []rt.TabRecord) []Tab {
	out := make([]Tab, 0, len(rTabs))
	for _, t := range rTabs {
		out = append(out, TabFromRuntime(t))
	}
	sortTabsByID(out)
	return out
}

func sortTabsByID(tabs []Tab) {
	sort.Slice(tabs, func(i, j int) bool {
		return tabs[i].ID < tabs[j].ID
	})
}

func sortPanesByID(panes []Pane) {
	sort.Slice(panes, func(i, j int) bool {
		return panes[i].ID < panes[j].ID
	})
}
