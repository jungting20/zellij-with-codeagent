package agentdashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"zellij-with-codeagent/internal/transport"
)

func TestViewRendersDeterministicGroupedDashboardAtSupportedWidths(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	records := []transport.AgentWithPane{
		viewRecord("agent-codex", "codex", "working", "/repo/zellij-with-codeagent", now.Add(-90*time.Second)),
		viewRecord("agent-claude", "claude", "blocked", "/repo/api-server", now.Add(-12*time.Second)),
		viewRecord("agent-gemini", "gemini", "idle", "/repo/frontend", now.Add(-3*time.Minute-41*time.Second)),
		viewRecord("agent-cursor", "cursor", "unknown", "/repo/mobile", now.Add(-2*time.Second)),
	}
	records[0].Pane.SessionID = "project-alpha"
	records[0].Pane.TabID, records[0].Pane.TabName = "tab-code", "coding"
	records[1].Pane.SessionID = "project-alpha"
	records[1].Pane.TabID, records[1].Pane.TabName = "tab-review", "review"
	records[2].Pane.SessionID = "experiments"
	records[2].Pane.TabID, records[2].Pane.TabName = "tab-lab", "lab"
	records[3].Pane.SessionID = "experiments"
	records[3].Pane.TabID, records[3].Pane.TabName = "tab-lab", "lab"

	for _, width := range []int{80, 120} {
		t.Run(string(rune(width)), func(t *testing.T) {
			m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{SourceSession: "project-alpha"}))
			m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
			m.width, m.height, m.connection, m.loaded, m.lastRefresh = width, 17, "live", true, now
			m.rows = append([]transport.AgentWithPane(nil), records...)
			m.selected, m.selectedID = 1, "agent-claude"
			plain := ansi.Strip(m.View())

			for _, want := range []string{
				"AGENT DASHBOARD", "PROJECT  STATE  AGENT  SINCE",
				"project-alpha (2)  current", "experiments (2)",
				"coding (tab-code) (1)", "review (tab-review) (1)", "lab (tab-lab) (2)",
				"Codex", "Claude", "Gemini", "Cursor",
				"working", "blocked", "idle", "unknown",
				"zellij-with-codeagent", "api-server", "frontend", "mobile",
				"01:30", "> ", "Space pin", "i input", "g worktree", "d close", "Enter focus", "R refresh", "q quit",
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

func TestViewGroupsMissingSessionAndTabAsUngrouped(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.width, m.height, m.connection, m.loaded, m.lastRefresh = 100, 8, "live", true, now
	m.rows = []transport.AgentWithPane{
		viewRecord("agent-one", "codex", "idle", "/repo/one", now.Add(-time.Minute)),
		viewRecord("agent-two", "claude", "working", "/repo/two", now.Add(-time.Minute)),
	}

	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "ungrouped (2)") {
		t.Fatalf("view missing ungrouped session header:\n%s", plain)
	}
	if strings.Count(plain, "ungrouped (2)") != 2 {
		t.Fatalf("view missing nested ungrouped session and tab labels:\n%s", plain)
	}
}

func TestViewOmitsAccessColumn(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.width, m.height, m.connection, m.loaded, m.lastRefresh = 100, 11, "live", true, now
	m.rows = []transport.AgentWithPane{
		viewRecord("agent-read-only", "codex", "idle", "/repo/reviewer", now.Add(-time.Minute)),
		viewRecord("agent-full", "codex", "working", "/repo/default", now.Add(-time.Minute)),
	}
	m.rows[0].Agent.Access = "read-only"

	plain := ansi.Strip(m.View())
	// The output preview identifies its agent, whose ID may contain access words.
	list, _, _ := strings.Cut(plain, "── Pane 출력")
	for _, forbidden := range []string{"ACCESS", "read-only", "full"} {
		if strings.Contains(list, forbidden) {
			t.Fatalf("view contains access column %q:\n%s", forbidden, plain)
		}
	}
}

func TestViewRendersPinnedSectionMarkerAndSpaceHint(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m.width, m.height, m.connection, m.loaded, m.lastRefresh = 100, 10, "live", true, now
	pinned := viewRecord("agent-pinned", "claude", "idle", "/repo/pinned", now.Add(-time.Minute))
	pinned.Agent.Pinned = true
	m.rows = []transport.AgentWithPane{
		pinned,
		viewRecord("agent-normal", "codex", "working", "/repo/normal", now.Add(-time.Minute)),
	}

	plain := ansi.Strip(m.View())
	for _, want := range []string{"── PINNED (1) ─", "* pinned", "── UNPINNED (1) ─", "normal", "Space pin", "i input"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q:\n%s", want, plain)
		}
	}
	heading := lineContaining(plain, "── PINNED")
	if !strings.Contains(heading, " │ ") || !strings.Contains(heading, "── UNPINNED") {
		t.Fatalf("areas are not side by side:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		parts := strings.SplitN(line, " │ ", 2)
		if len(parts) == 2 && (strings.Contains(parts[0], "normal") || strings.Contains(parts[1], "* pinned")) {
			t.Fatalf("agent in wrong area: %s", line)
		}
	}
	t.Logf("Two-column dashboard:\n%s", plain)
}

func TestViewKeepsSectionAndSelectionVisibleWhileScrolling(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	for _, pinnedCount := range []int{0, 5, 10} {
		m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
		m.connection, m.loaded, m.lastRefresh = "live", true, now
		for index := 0; index < 10; index++ {
			record := viewRecord(fmt.Sprintf("agent-%d", index), "codex", "idle", fmt.Sprintf("/repo/p-%d", index), now)
			record.Agent.Pinned = index < pinnedCount
			m.rows = append(m.rows, record)
		}
		for _, width := range []int{20, 80, 120} {
			for _, height := range []int{6, 8, 14, 24} {
				m.width, m.height = width, height
				for selected := range m.rows {
					m.selected = selected
					m.focusPinned = selected < pinnedCount
					plain := ansi.Strip(m.View())
					section := fmt.Sprintf("── UNPINNED (%d)", 10-pinnedCount)
					if selected < pinnedCount {
						section = fmt.Sprintf("── PINNED (%d)", pinnedCount)
					}
					if !strings.Contains(plain, section) || !strings.Contains(lineContaining(plain, "> "), fmt.Sprintf("p-%d", selected)) {
						t.Fatalf("selection or section hidden (pinned=%d, selected=%d, %dx%d):\n%s", pinnedCount, selected, width, height, plain)
					}
					if strings.Count(plain, section) != 1 || len(strings.Split(plain, "\n")) > height {
						t.Fatalf("duplicated section or overflowing height:\n%s", plain)
					}
					for _, line := range strings.Split(plain, "\n") {
						if ansi.StringWidth(line) > width {
							t.Fatalf("overflowing width: %q", line)
						}
					}
				}
			}
		}
	}
}

func TestDisplayIndexForAgentSkipsGroupHeaders(t *testing.T) {
	rows := []displayRow{
		{kind: displayPinned, count: 1},
		{kind: displayAgent, agentIndex: 0},
	}
	if got := displayIndexForAgent(rows, 0); got != 1 {
		t.Fatalf("displayIndexForAgent() = %d, want pinned agent row at 1", got)
	}
}

func TestViewShowsProjectBeforeOtherColumnsInNarrowPane(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m.width, m.height, m.connection, m.loaded, m.lastRefresh = 20, 8, "live", true, now
	m.rows = []transport.AgentWithPane{
		viewRecord("agent-one", "codex", "working", "/repo/first-project", now.Add(-time.Minute)),
	}

	plain := ansi.Strip(m.View())
	header := lineContaining(plain, "PROJECT")
	row := lineContaining(plain, "first-p")
	if header == "" || row == "" {
		t.Fatalf("narrow view does not prioritize project column:\n%s", plain)
	}
	if strings.Contains(row, "working") || strings.Contains(row, "Codex") {
		t.Fatalf("narrow row should spend available width on project first: %q", row)
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
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
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

func TestViewOutputUsesSpareSpaceAndFollowsSelectionAndRefresh(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	rows := []transport.AgentWithPane{
		viewRecord("first", "codex", "idle", "/repo/first", time.Now()),
		viewRecord("second", "codex", "idle", "/repo/second", time.Now()),
	}
	var output []string
	for i := 1; i <= 30; i++ {
		output = append(output, fmt.Sprintf("output-%02d", i))
	}
	rows[0].Pane.LastOutput = "\x1b[31m" + strings.Join(output, "\r\n") + "\x1b[0m\n  \n\n"
	rows[1].Pane.LastOutput = "second output"
	m = applyRefresh(t, m, rows)
	for _, width := range []int{20, 80, 120} {
		for _, height := range []int{6, 12, 40} {
			m.width, m.height = width, height
			plain := ansi.Strip(m.View())
			if len(strings.Split(plain, "\n")) > height || !strings.Contains(plain, "> 1 ") {
				t.Fatalf("preview overflow or hidden selection at %dx%d:\n%s", width, height, plain)
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("preview exceeds width: %q", line)
				}
			}
			if height == 40 && (!strings.Contains(plain, "output-11") || !strings.Contains(plain, "output-30") || strings.Contains(plain, "output-10")) {
				t.Fatalf("preview must show last 20 non-padding screen lines:\n%s", plain)
			}
			if height == 12 && (!strings.Contains(plain, "output-30") || strings.Contains(plain, "output-11")) {
				t.Fatalf("small preview must retain newest output:\n%s", plain)
			}
		}
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if plain := ansi.Strip(m.View()); !strings.Contains(plain, "second output") || strings.Contains(plain, "output-30") {
		t.Fatalf("preview did not follow selection:\n%s", plain)
	}
	rows[1].Pane.LastOutput = "updated output"
	m = applyRefresh(t, m, rows)
	if plain := ansi.Strip(m.View()); !strings.Contains(plain, "updated output") || strings.Contains(plain, "second output") {
		t.Fatalf("preview did not follow refresh:\n%s", plain)
	}
	rows[1].Pane.LastOutput = ""
	m = applyRefresh(t, m, rows)
	if !strings.Contains(m.View(), "아직 수집된 출력이 없습니다") {
		t.Fatal("missing empty output placeholder")
	}
	t.Logf("Dashboard with output preview:\n%s", ansi.Strip(m.View()))
}

func TestViewAreasScrollIndependentlyAndSurviveResize(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	var rows []transport.AgentWithPane
	for index := 0; index < 10; index++ {
		for _, pinned := range []bool{true, false} {
			name := fmt.Sprintf("normal-%d", index)
			if pinned {
				name = fmt.Sprintf("pin-%d", index)
			}
			row := viewRecord(name, "codex", "idle", "/repo/"+name, time.Unix(int64(index), 0))
			row.Agent.Pinned = pinned
			rows = append(rows, row)
		}
	}
	m = applyRefresh(t, m, rows)
	m.width, m.height = 120, 8
	for index := 0; index < 8; index++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	rightColumn := func(view string) string {
		var lines []string
		for _, line := range strings.Split(ansi.Strip(view), "\n") {
			parts := strings.SplitN(line, " │ ", 2)
			if len(parts) == 2 {
				lines = append(lines, parts[1])
			}
		}
		return strings.Join(lines, "\n")
	}
	before := rightColumn(m.View())
	for index := 0; index < 8; index++ {
		m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if after := rightColumn(m.View()); before != after || !strings.Contains(after, "normal-8") {
		t.Fatalf("inactive area scrolled:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 60, Height: 8})
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "pin-8") || strings.Contains(plain, "UNPINNED") {
		t.Fatalf("narrow view did not retain active area:\n%s", plain)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.selectedID != "normal-8" {
		t.Fatalf("selection lost: %q", m.selectedID)
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 8})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "pin-8") || !strings.Contains(plain, "normal-8") {
		t.Fatalf("resize lost area viewports:\n%s", plain)
	}
}

func TestViewNumbersMatchAreaSelectionAcrossScrollAndResize(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	var rows []transport.AgentWithPane
	for i := 0; i < 10; i++ {
		row := viewRecord(fmt.Sprintf("a%d", i), "codex", "idle", fmt.Sprintf("/repo/project%d", i), time.Unix(int64(i), 0))
		rows = append(rows, row)
	}
	m = applyRefresh(t, m, rows)
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	for _, width := range []int{20, 80, 120} {
		m = update(t, m, tea.WindowSizeMsg{Width: width, Height: 8})
		plain := ansi.Strip(m.View())
		if line := lineContaining(plain, "> 9 "); !strings.Contains(line, "project8") {
			t.Fatalf("numbered selection hidden at width %d:\n%s", width, plain)
		}
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	line := lineContaining(ansi.Strip(m.View()), "> ")
	if !strings.Contains(line, ">     project9") {
		t.Fatalf("tenth entry has a shortcut: %q", line)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if line := lineContaining(ansi.Strip(m.View()), "> 1 "); !strings.Contains(line, "project0") {
		t.Fatalf("shortcut did not scroll back: %q", line)
	}
}

func TestViewSharesNumbersBetweenPinnedAndUnpinned(t *testing.T) {
	m := concreteModel(t, NewModel(context.Background(), &fakeClient{}, Options{}))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	var rows []transport.AgentWithPane
	for i := 0; i < 4; i++ {
		row := viewRecord(fmt.Sprintf("a%d", i), "codex", "idle", fmt.Sprintf("/repo/project%d", i), time.Unix(int64(i), 0))
		row.Agent.Pinned = i < 2
		rows = append(rows, row)
	}
	m = applyRefresh(t, m, rows)
	m.width, m.height = 120, 12
	plain := ansi.Strip(m.View())
	for _, want := range []string{"  1 * project0", "  2 * project1", "> 3   project2", "  4   project3"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q:\n%s", want, plain)
		}
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if !strings.Contains(ansi.Strip(m.View()), "> 2 * project1") {
		t.Fatal("number key did not reveal pinned area in narrow view")
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if !strings.Contains(ansi.Strip(m.View()), "> 4   project3") {
		t.Fatal("number key did not reveal unpinned area in narrow view")
	}
}
