package agentdashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

func TestViewRendersDeterministicFlatDashboardAtSupportedWidths(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	records := []transport.AgentWithPane{
		viewRecord("agent-codex", "codex", "working", "/repo/zellij-with-codeagent", now.Add(-90*time.Second)),
		viewRecord("agent-claude", "claude", "blocked", "/repo/api-server", now.Add(-12*time.Second)),
		viewRecord("agent-gemini", "gemini", "idle", "/repo/frontend", now.Add(-3*time.Minute-41*time.Second)),
		viewRecord("agent-cursor", "cursor", "unknown", "/repo/mobile", now.Add(-2*time.Second)),
	}

	for _, width := range []int{80, 120} {
		t.Run(string(rune(width)), func(t *testing.T) {
			m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
			m.width, m.height, m.connection, m.loaded, m.lastRefresh = width, 12, "live", true, now
			m.rows = append([]transport.AgentWithPane(nil), records...)
			m.selected, m.selectedID = 1, "agent-claude"
			plain := ansi.Strip(m.View())

			for _, want := range []string{
				"AGENT DASHBOARD", "STATE  AGENT  ACCESS  PROJECT  SINCE",
				"Codex", "Claude", "Gemini", "Cursor",
				"working", "blocked", "idle", "unknown",
				"zellij-with-codeagent", "api-server", "frontend", "mobile",
				"01:30", "> ", "j/k move  Enter focus  R refresh  q quit",
			} {
				if !strings.Contains(plain, want) {
					t.Fatalf("width=%d view missing %q:\n%s", width, want, plain)
				}
			}
			for _, forbidden := range []string{"TREE", "DETAIL", "prompt", "stop", "send", "notification"} {
				if strings.Contains(plain, forbidden) {
					t.Fatalf("width=%d view contains forbidden %q:\n%s", width, forbidden, plain)
				}
			}
			for _, state := range []string{"working", "blocked", "idle", "unknown"} {
				line := lineContaining(plain, state)
				if line == "" || len([]rune(strings.TrimSpace(line))) < len([]rune(state))+2 {
					t.Fatalf("state %q lacks a distinct symbol in line %q", state, line)
				}
			}
		})
	}
}

func TestViewRendersAccessAndDefaultsEmptyAccessToFull(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.width, m.height, m.connection, m.loaded, m.lastRefresh = 100, 8, "live", true, now
	m.rows = []transport.AgentWithPane{
		viewRecord("agent-read-only", "codex", "idle", "/repo/reviewer", now.Add(-time.Minute)),
		viewRecord("agent-full", "codex", "working", "/repo/default", now.Add(-time.Minute)),
	}
	m.rows[0].Agent.Access = "read-only"

	plain := ansi.Strip(m.View())
	for _, want := range []string{"STATE  AGENT  ACCESS  PROJECT  SINCE", "read-only", "full"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q:\n%s", want, plain)
		}
	}
}

func TestViewShowsDegradedConnectionAndLastStatus(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.width, m.height, m.connection, m.loaded = 80, 8, "degraded", true
	m.statusText = "refresh failed: daemon unavailable"
	plain := ansi.Strip(m.View())
	for _, want := range []string{"DEGRADED", "refresh failed: daemon unavailable"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q:\n%s", want, plain)
		}
	}
}

func TestViewHonorsWindowWidthAndHeightWhileKeepingSelectionVisible(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.width, m.height, m.connection, m.loaded, m.lastRefresh = 80, 8, "live", true, now
	for index := 0; index < 10; index++ {
		project := strings.Repeat("project-", 15)
		if index == 9 {
			project = "selected-project"
		}
		m.rows = append(m.rows, viewRecord("agent", "codex", "working", "/repo/"+project, now.Add(-time.Minute)))
	}
	m.selected, m.selectedID = 9, "agent"

	plain := ansi.Strip(m.View())
	lines := strings.Split(plain, "\n")
	if len(lines) > m.height {
		t.Fatalf("view lines=%d, want <=%d:\n%s", len(lines), m.height, plain)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("line width=%d, want <=%d: %q", ansi.StringWidth(line), m.width, line)
		}
	}
	if !strings.Contains(plain, "> ") || !strings.Contains(plain, "selected-project") {
		t.Fatalf("selected row is outside viewport:\n%s", plain)
	}
}

func viewRecord(id, kind, state, cwd string, changed time.Time) transport.AgentWithPane {
	return transport.AgentWithPane{
		Agent: transport.Agent{ID: id, Kind: kind, State: state, CreatedAt: changed.Add(-time.Hour), StateChangedAt: changed},
		Pane:  transport.Pane{ID: "pane-" + id, CWD: cwd},
	}
}

func lineContaining(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}
