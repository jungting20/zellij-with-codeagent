package runtime

import (
	"context"
	"errors"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/registry"
)

var (
	ErrPaneNotFound   = errors.New("runtime pane not found")
	ErrMissingPaneID  = errors.New("runtime pane id is required")
	ErrCleanupPartial = errors.New("runtime cleanup partially failed")
)

type (
	PaneID       = registry.PaneID
	TaskID       = registry.TaskID
	AgentID      = registry.AgentID
	ZellijPaneID = registry.ZellijPaneID
	ZellijTabID  = registry.ZellijTabID
	PaneRole     = registry.PaneRole
	PaneStatus   = registry.PaneStatus
)

const (
	PaneRoleUnknown = registry.PaneRoleUnknown
	PaneRoleCoder   = registry.PaneRoleCoder
	PaneRoleTest    = registry.PaneRoleTest
	PaneRoleBuild   = registry.PaneRoleBuild
	PaneRoleServer  = registry.PaneRoleServer
	PaneRoleLog     = registry.PaneRoleLog

	PaneStatusStarting = registry.PaneStatusStarting
	PaneStatusRunning  = registry.PaneStatusRunning
	PaneStatusExited   = registry.PaneStatusExited
	PaneStatusClosed   = registry.PaneStatusClosed
	PaneStatusLost     = registry.PaneStatusLost
	PaneStatusError    = registry.PaneStatusError
)

type RuntimeService interface {
	CreatePane(context.Context, CreatePaneRequest) (CreatePaneResponse, error)
	SendInput(context.Context, SendInputRequest) error
	ListPanes(context.Context) (ListPanesResponse, error)
	InspectPane(context.Context, InspectPaneRequest) (InspectPaneResponse, error)
	SnapshotOutput(context.Context, SnapshotOutputRequest) (SnapshotOutputResponse, error)
	InspectRuntime(context.Context, InspectRuntimeRequest) (InspectRuntimeResponse, error)
	RecentEvents(context.Context, RecentEventsRequest) (RecentEventsResponse, error)
	ClosePane(context.Context, ClosePaneRequest) (ClosePaneResponse, error)
	Reconcile(context.Context, ReconcileRequest) (ReconcileResponse, error)
	Cleanup(context.Context, CleanupRequest) (CleanupResponse, error)
	ApplyExecutionPlan(context.Context, ApplyExecutionPlanRequest) (ApplyExecutionPlanResponse, error)
	SubscribeEvents(context.Context) (<-chan eventbus.Event, func(), error)
}

type CreatePaneRequest struct {
	ID      PaneID
	TaskID  TaskID
	AgentID AgentID
	Role    PaneRole
	Name    string
	NewTab  bool
	TabName string
	// ZellijTabID targets an existing tab. When NewTab is true, the created tab
	// ID is returned on the Pane instead.
	ZellijTabID *ZellijTabID
	Command     []string
	CWD         string
}

type CreatePaneResponse struct {
	Pane Pane
}

type SendInputRequest struct {
	PaneID PaneID
	Text   string
}

type ListPanesResponse struct {
	Panes []Pane
}

type InspectPaneRequest struct {
	PaneID PaneID
}

type InspectPaneResponse struct {
	Pane Pane
}

type SnapshotOutputRequest struct {
	PaneID PaneID
	Full   bool
	ANSI   bool
}

type SnapshotOutputResponse struct {
	Pane   Pane
	Output string
}

type InspectRuntimeRequest struct{}

type RuntimeCounts struct {
	Managed  int
	Starting int
	Running  int
	Exited   int
	Closed   int
	Lost     int
	Error    int
	Active   int
	Terminal int
}

type TaskPaneGroup struct {
	TaskID TaskID
	Panes  []Pane
}

type RolePaneGroup struct {
	Role  PaneRole
	Panes []Pane
}

type PaneOutputSummary struct {
	PaneID     PaneID
	TaskID     TaskID
	Role       PaneRole
	Status     PaneStatus
	LastOutput string
	UpdatedAt  time.Time
}

type InspectRuntimeResponse struct {
	Message string
	Counts  RuntimeCounts
	Panes   []Pane
	Tasks   []TaskPaneGroup
	Roles   []RolePaneGroup
	Outputs []PaneOutputSummary
}

type RecentEventsRequest struct {
	Limit int
	Types []eventbus.EventType
}

type EventSummary struct {
	Type         eventbus.EventType
	PaneID       PaneID
	TaskID       TaskID
	AgentID      AgentID
	ZellijPaneID ZellijPaneID
	Message      string
	Time         time.Time
}

type RecentEventsResponse struct {
	Events []EventSummary
}

type ClosePaneRequest struct {
	PaneID PaneID
}

type ClosePaneResponse struct {
	Pane Pane
}

type ReconcileRequest struct{}

type ReconcileResponse struct {
	Panes     []Pane
	Active    []Pane
	Exited    []Pane
	Lost      []Pane
	Unmanaged []ZellijPaneID
}

type CleanupRequest struct {
	PaneIDs []PaneID
	TaskID  TaskID
	Role    PaneRole
}

type CleanupFailure struct {
	Pane  Pane
	Error string
}

type CleanupResponse struct {
	Closed  []Pane
	Failed  []CleanupFailure
	Skipped []Pane
}

type Pane struct {
	ID            PaneID
	TaskID        TaskID
	AgentID       AgentID
	ZellijPaneID  ZellijPaneID
	ZellijTabID   *ZellijTabID
	TabName       string
	Role          PaneRole
	Command       []string
	CWD           string
	Status        PaneStatus
	LastOutput    string
	StatusMessage string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
