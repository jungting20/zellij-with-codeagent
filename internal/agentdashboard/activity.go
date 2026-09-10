package agentdashboard

import (
	"fmt"
	"sort"
	"time"

	"zellij-with-codeagent/internal/transport"
)

const activityLimit = 20

type agentActivity struct {
	agentID, project, previous, state string
	at                                time.Time
	polled                            bool
}

func (m *Model) recordActivity(event transport.Event) {
	m.addActivity(event, false)
}

func (m *Model) addActivity(event transport.Event, polled bool) {
	if event.AgentID == "" || event.PreviousState == "" || event.AgentState == "" || event.PreviousState == event.AgentState {
		return
	}
	if event.Time.IsZero() {
		event.Time = m.activityNow
	}
	for _, activity := range m.activities {
		if activity.agentID == event.AgentID && activity.state == event.AgentState && activity.at.Equal(event.Time) {
			return
		}
	}
	// Event publication occurs after the store update, so their timestamps
	// need not be identical. Reconcile the latest observation of this agent.
	for index, activity := range m.activities {
		if activity.agentID != event.AgentID {
			continue
		}
		if activity.state == event.AgentState {
			if polled && !activity.at.Before(event.Time) {
				return
			}
			if !polled && activity.polled && !event.Time.Before(activity.at) {
				m.activities = append([]agentActivity(nil), m.activities...)
				m.activities = append(m.activities[:index], m.activities[index+1:]...)
			}
		}
		break
	}
	activity := agentActivity{agentID: event.AgentID, previous: event.PreviousState, state: event.AgentState, at: event.Time, polled: polled}
	for _, row := range m.rows {
		if row.Agent.ID == event.AgentID {
			activity.project = projectName(row.Pane.CWD)
			break
		}
	}
	// Copy before sorting: Bubble Tea models are passed by value.
	m.activities = append(append([]agentActivity(nil), m.activities...), activity)
	sort.SliceStable(m.activities, func(i, j int) bool { return m.activities[i].at.After(m.activities[j].at) })
	if len(m.activities) > activityLimit {
		m.activities = m.activities[:activityLimit]
	}
}

// Polling also captures changes when the event stream is disconnected.
func (m *Model) recordRefreshActivities(rows []transport.AgentWithPane) {
	previous := make(map[string]string, len(m.rows))
	for _, row := range m.rows {
		previous[row.Agent.ID] = row.Agent.State
	}
	for _, row := range rows {
		old, known := previous[row.Agent.ID]
		if known && old != row.Agent.State {
			m.addActivity(transport.Event{AgentID: row.Agent.ID, PreviousState: old, AgentState: row.Agent.State, Time: row.Agent.StateChangedAt}, true)
		}
	}
}

func (m Model) activityView() []string {
	if len(m.activities) == 0 {
		return nil
	}
	count := minInt(3, len(m.activities))
	if m.height > 0 {
		// Keep room for the header, footer, status and three list lines.
		count = minInt(count, m.height-7)
	}
	if count <= 0 {
		return nil
	}
	lines := []string{mutedStyle.Render("── 최근 상태 변화 ──")}
	for _, activity := range m.activities[:count] {
		project := activity.project
		if project == "" {
			project = activity.agentID
			for _, row := range m.rows {
				if row.Agent.ID == activity.agentID {
					project = projectName(row.Pane.CWD)
					break
				}
			}
		}
		lines = append(lines, fmt.Sprintf("%s: %s → %s · %s", project, activityState(activity.previous), activityState(activity.state), activityAge(m.activityNow, activity.at)))
	}
	return lines
}

func activityState(state string) string {
	switch state {
	case "working":
		return "작업 중"
	case "idle":
		return "입력 대기"
	case "blocked":
		return "확인 필요"
	case "unknown":
		return "알 수 없음"
	default:
		return state
	}
}

func activityAge(now, at time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(at)
	switch {
	case age < time.Minute:
		return "방금 전"
	case age < time.Hour:
		return fmt.Sprintf("%d분 전", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%d시간 전", int(age/time.Hour))
	default:
		return fmt.Sprintf("%d일 전", int(age/(24*time.Hour)))
	}
}
