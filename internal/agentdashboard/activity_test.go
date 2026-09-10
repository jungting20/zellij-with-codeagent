package agentdashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"zellij-with-codeagent/internal/transport"
)

func TestActivitiesCombineEventsAndPollingWithoutDuplicates(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	row := viewRecord("a", "codex", "working", "/repo/api-server", now.Add(-3*time.Minute))
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	if len(m.activities) != 0 {
		t.Fatal("initial load generated an activity")
	}
	event := transport.Event{Type: agentStateChangedEventType, AgentID: "a", PreviousState: "working", AgentState: "idle", Time: now.Add(-2 * time.Minute)}
	m = update(t, m, streamEventMsg{event: event})
	m = update(t, m, streamEventMsg{event: event})
	row.Agent.State, row.Agent.StateChangedAt = "idle", event.Time
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	if len(m.activities) != 1 {
		t.Fatalf("duplicate activities=%#v", m.activities)
	}
	row.Agent.Pinned = true
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	if len(m.activities) != 1 {
		t.Fatal("pin change generated an activity")
	}
	row.Agent.State, row.Agent.StateChangedAt = "blocked", now.Add(-time.Minute)
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	if len(m.activities) != 2 || m.activities[0].previous != "idle" || m.activities[0].state != "blocked" {
		t.Fatalf("polling change missing: %#v", m.activities)
	}
	m = update(t, m, refreshResultMsg{err: errors.New("disconnected")})
	if len(m.activities) != 2 {
		t.Fatal("refresh failure changed history")
	}
}

func TestActivityViewKeepsHistoryAndSelectionVisible(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	row := viewRecord("a", "codex", "working", "/repo/api-server", now.Add(-3*time.Minute))
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	m = update(t, m, streamEventMsg{event: transport.Event{Type: agentStateChangedEventType, AgentID: "a", PreviousState: "working", AgentState: "idle", Time: now.Add(-2 * time.Minute)}})
	m.activityNow = now
	m.width, m.height = 120, 12
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "api-server: 작업 중 → 입력 대기 · 2분 전") {
		t.Fatalf("missing activity:\n%s", plain)
	}
	t.Logf("Dashboard activity:\n%s", plain)
	// Activity survives the agent disappearing from the managed list.
	m = applyRefresh(t, m, nil)
	m.activityNow = now.Add(time.Minute)
	if !strings.Contains(ansi.Strip(m.View()), "api-server: 작업 중 → 입력 대기 · 3분 전") {
		t.Fatal("history lost after agent removal")
	}
	m = applyRefresh(t, m, []transport.AgentWithPane{row})
	for _, width := range []int{20, 80, 120} {
		for _, height := range []int{6, 8, 12} {
			m = update(t, m, tea.WindowSizeMsg{Width: width, Height: height})
			plain = ansi.Strip(m.View())
			if len(strings.Split(plain, "\n")) > height || !strings.Contains(plain, "> 1 ") {
				t.Fatalf("selection hidden or height overflow at %dx%d:\n%s", width, height, plain)
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("width overflow: %s", line)
				}
			}
		}
	}
}

func TestActivitiesBoundHistoryAndShowLatestThree(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	now := time.Now()
	for i := activityLimit; i >= 0; i-- {
		m = update(t, m, streamEventMsg{event: transport.Event{Type: agentStateChangedEventType, AgentID: fmt.Sprintf("agent-%d", i), PreviousState: "working", AgentState: "idle", Time: now.Add(time.Duration(i) * time.Second)}})
	}
	if len(m.activities) != activityLimit || m.activities[0].agentID != "agent-20" {
		t.Fatalf("history not bounded or sorted: %#v", m.activities)
	}
	lines := m.activityView()
	if len(lines) != 4 || !strings.Contains(lines[1], "agent-20:") || !strings.Contains(lines[3], "agent-18:") {
		t.Fatalf("latest entries=%v", lines)
	}
}

func TestActivitiesDeduplicatePublicationDelayInEitherOrder(t *testing.T) {
	for _, eventFirst := range []bool{true, false} {
		m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
		now := time.Now()
		row := viewRecord("a", "codex", "working", "/repo/api-server", now.Add(-time.Minute))
		m = applyRefresh(t, m, []transport.AgentWithPane{row})
		row.Agent.State, row.Agent.StateChangedAt = "idle", now
		event := streamEventMsg{event: transport.Event{Type: agentStateChangedEventType, AgentID: "a", PreviousState: "working", AgentState: "idle", Time: now.Add(time.Millisecond)}}
		if eventFirst {
			m = update(t, m, event)
		}
		m = applyRefresh(t, m, []transport.AgentWithPane{row})
		if !eventFirst {
			m = update(t, m, event)
		}
		if len(m.activities) != 1 {
			t.Fatalf("eventFirst=%t duplicated activity: %#v", eventFirst, m.activities)
		}
		// A subsequent working -> idle cycle is a new change, not a duplicate.
		row.Agent.State, row.Agent.StateChangedAt = "working", now.Add(time.Second)
		m = applyRefresh(t, m, []transport.AgentWithPane{row})
		row.Agent.State, row.Agent.StateChangedAt = "idle", now.Add(2*time.Second)
		m = applyRefresh(t, m, []transport.AgentWithPane{row})
		if len(m.activities) != 3 {
			t.Fatalf("new cycle lost: %#v", m.activities)
		}
	}
}
