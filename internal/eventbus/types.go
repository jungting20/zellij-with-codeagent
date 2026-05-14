package eventbus

import "time"

// EventType categorizes runtime observations published by agentd.
type EventType string

const (
	TypeRawOutput      EventType = "raw_output"
	TypeServerReady    EventType = "server_ready"
	TypeTestFailed     EventType = "test_failed"
	TypeTestPassed     EventType = "test_passed"
	TypePaneClosed     EventType = "pane_closed"
	TypeHealthChanged  EventType = "health_changed"
	TypeSubscribeError EventType = "subscribe_error"
)

// Event is an in-process observation for planners and supervisors.
type Event struct {
	Type EventType

	PaneID       string
	TaskID       string
	AgentID      string
	ZellijPaneID string

	Message string
	Time    time.Time
}
