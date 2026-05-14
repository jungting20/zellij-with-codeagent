package registry

import "time"

// Logical IDs are daemon-owned and remain stable even when Zellij pane IDs are
// recreated or reused by the backend runtime.
type (
	TaskID       string
	AgentID      string
	PaneID       string
	ZellijPaneID string
	ZellijTabID  int
)

type PaneRole string

const (
	PaneRoleUnknown PaneRole = "unknown"
	PaneRoleCoder   PaneRole = "coder"
	PaneRoleTest    PaneRole = "test"
	PaneRoleBuild   PaneRole = "build"
	PaneRoleServer  PaneRole = "server"
	PaneRoleLog     PaneRole = "log"
)

type PaneStatus string

const (
	PaneStatusStarting PaneStatus = "starting"
	PaneStatusRunning  PaneStatus = "running"
	PaneStatusExited   PaneStatus = "exited"
	PaneStatusClosed   PaneStatus = "closed"
	PaneStatusLost     PaneStatus = "lost"
	PaneStatusError    PaneStatus = "error"
)

type PaneRecord struct {
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

type RegisterPaneRequest struct {
	ID           PaneID
	TaskID       TaskID
	AgentID      AgentID
	ZellijPaneID ZellijPaneID
	ZellijTabID  *ZellijTabID
	TabName      string
	Role         PaneRole
	Command      []string
	CWD          string
	Status       PaneStatus
}
