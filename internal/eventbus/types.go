package eventbus

import "time"

// EventType categorizes runtime observations published by agentd.
type EventType string

const (
	TypeRawOutput         EventType = "raw_output"
	TypeServerReady       EventType = "server_ready"
	TypeTestFailed        EventType = "test_failed"
	TypeTestPassed        EventType = "test_passed"
	TypePaneClosed        EventType = "pane_closed"
	TypeMessageSent       EventType = "message_sent"
	TypeHealthChanged     EventType = "health_changed"
	TypeSubscribeError    EventType = "subscribe_error"
	TypeAgentStateChanged EventType = "agent_state_changed"
)

// Event is an in-process observation for planners and supervisors.
type Event struct {
	Type EventType

	PaneID        string
	TaskID        string
	AgentID       string
	ZellijPaneID  string
	AgentKind     string
	PreviousState string
	AgentState    string
	MatchedRule   string
	Reason        string

	Message string
	Time    time.Time
}
