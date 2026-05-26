package registry

import (
	"errors"
	"fmt"
	"time"
)

// Logical IDs are daemon-owned and remain stable even when Zellij pane IDs are
// recreated or reused by the backend runtime.
type (
	TaskID       string
	AgentID      string
	PaneID       string
	ZellijPaneID string
	ZellijTabID  int
	SessionID    string
	TabID        string
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
	SessionID     SessionID
	TabID         TabID
	TaskID        TaskID
	AgentID       AgentID
	ZellijPaneID  ZellijPaneID
	ZellijTabID   *ZellijTabID
	TabName       string
	Role          string
	Command       []string
	CWD           string
	Status        PaneStatus
	LastOutput    string
	StatusMessage string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type TabRecord struct {
	ID        TabID
	Name      string
	Panes     map[PaneID]PaneRecord
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SessionRecord struct {
	ID        SessionID
	Tabs      map[TabID]TabRecord
	CreatedAt time.Time
	UpdatedAt time.Time
}

type RegisterPaneRequest struct {
	ID           PaneID
	SessionID    SessionID
	TabID        TabID
	TaskID       TaskID
	AgentID      AgentID
	ZellijPaneID ZellijPaneID
	ZellijTabID  *ZellijTabID
	TabName      string
	Role         string
	Command      []string
	CWD          string
	Status       PaneStatus
}

var ErrInvalidRequest = errors.New("invalid registry request")

// Validate performs stateless validation of the RegisterPaneRequest.
func (req RegisterPaneRequest) Validate() error {
	if req.ID == "" {
		return fmt.Errorf("%w: ID is required", ErrInvalidRequest)
	}
	if req.ZellijTabID != nil {
		if *req.ZellijTabID < 0 {
			return fmt.Errorf("%w: ZellijTabID cannot be negative", ErrInvalidRequest)
		}
	}
	return nil
}
