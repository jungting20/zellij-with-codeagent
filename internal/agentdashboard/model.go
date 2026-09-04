package agentdashboard

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

const defaultRefreshInterval = 2 * time.Second

const agentStateChangedEventType = "agent_state_changed"

type Client interface {
	ListAgents(context.Context) (transport.ListAgentsResponse, error)
	FocusAgent(context.Context, string, transport.FocusAgentRequest) (transport.FocusAgentResponse, error)
	SetAgentPinned(context.Context, string, transport.SetAgentPinnedRequest) (transport.SetAgentPinnedResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
}

type eventTypeStreamClient interface {
	StreamEventsByType(context.Context, ...string) (*transport.EventStream, error)
}

type Options struct {
	RefreshInterval    time.Duration
	SourceSession      string
	SourceZellijPaneID string
}

type refreshResultMsg struct {
	agents transport.ListAgentsResponse
	at     time.Time
	err    error
}

type focusResultMsg struct {
	agentID string
	err     error
}

type pinResultMsg struct {
	agentID string
	pinned  bool
	agent   transport.Agent
	err     error
}

type streamReadyMsg struct {
	stream *transport.EventStream
	err    error
}

type streamEventMsg struct{ event transport.Event }
type streamClosedMsg struct{ err error }
type refreshTickMsg struct{}

type Model struct {
	ctx    context.Context
	client Client
	opts   Options

	width, height int
	rows          []transport.AgentWithPane
	selected      int
	selectedID    string
	loaded        bool
	lastRefresh   time.Time
	listKnown     bool
	listHealthy   bool
	streamKnown   bool
	streamHealthy bool

	refreshing   bool
	refreshDirty bool
	focusing     bool
	pinning      bool
	stream       *transport.EventStream
	connection   string
	statusText   string
	quitting     bool
}

func NewModel(ctx context.Context, client Client, opts Options) tea.Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = defaultRefreshInterval
	}
	return Model{
		ctx:        ctx,
		client:     client,
		opts:       opts,
		refreshing: true,
		connection: "connecting",
		statusText: "loading agents",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.connectStreamCmd(), m.tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case refreshTickMsg:
		return m, tea.Batch(m.tickCmd(), m.requestRefresh())
	case refreshResultMsg:
		return m.handleRefresh(msg)
	case focusResultMsg:
		m.focusing = false
		if msg.err != nil {
			m.statusText = "focus failed: " + msg.err.Error()
			return m, m.requestRefresh()
		}
		m.statusText = "focused " + msg.agentID
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	case pinResultMsg:
		m.pinning = false
		if msg.err != nil {
			m.statusText = "pin failed: " + msg.err.Error()
			return m, nil
		}
		for index := range m.rows {
			if m.rows[index].Agent.ID == msg.agentID {
				m.rows[index].Agent.Pinned = msg.agent.Pinned
				break
			}
		}
		sortAgentRows(m.rows, m.opts.SourceSession)
		m.restoreSelection()
		if msg.pinned {
			m.statusText = "pinned " + msg.agentID
		} else {
			m.statusText = "unpinned " + msg.agentID
		}
		return m, nil
	case streamReadyMsg:
		m.streamKnown = true
		if msg.err != nil || msg.stream == nil {
			m.streamHealthy = false
			m.updateConnection()
			m.statusText = "event stream failed: " + errorText(msg.err, "unavailable")
			return m, nil
		}
		m.stream = msg.stream
		m.streamHealthy = true
		m.updateConnection()
		m.statusText = "event stream connected"
		return m, m.waitStreamCmd()
	case streamEventMsg:
		wait := m.waitStreamCmd()
		if msg.event.Type == "agent_state_changed" {
			return m, tea.Batch(wait, m.requestRefresh())
		}
		return m, wait
	case streamClosedMsg:
		m.streamKnown = true
		m.streamHealthy = false
		m.updateConnection()
		m.statusText = "event stream closed: " + errorText(msg.err, "closed")
		m.closeStream()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.selected > 0 {
			m.selected--
			m.selectedID = m.rows[m.selected].Agent.ID
		}
	case "down", "j":
		if m.selected+1 < len(m.rows) {
			m.selected++
			m.selectedID = m.rows[m.selected].Agent.ID
		}
	case "tab":
		if len(m.rows) > 0 {
			m.selected = (m.selected + 1) % len(m.rows)
			m.selectedID = m.rows[m.selected].Agent.ID
		}
	case "shift+tab":
		if len(m.rows) > 0 {
			m.selected = (m.selected - 1 + len(m.rows)) % len(m.rows)
			m.selectedID = m.rows[m.selected].Agent.ID
		}
	case "R":
		return m, m.requestRefresh()
	case " ":
		if len(m.rows) == 0 || m.pinning {
			return m, nil
		}
		m.pinning = true
		agent := m.rows[m.selected].Agent
		return m, m.pinCmd(agent.ID, !agent.Pinned)
	case "enter":
		if len(m.rows) == 0 || m.focusing {
			return m, nil
		}
		m.focusing = true
		return m, m.focusCmd(m.rows[m.selected].Agent.ID)
	}
	return m, nil
}

func (m *Model) requestRefresh() tea.Cmd {
	if m.refreshing {
		m.refreshDirty = true
		return nil
	}
	m.refreshing = true
	return m.refreshCmd()
}

func (m Model) handleRefresh(msg refreshResultMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
	m.listKnown = true
	if msg.err != nil {
		m.listHealthy = false
		m.updateConnection()
		m.statusText = "refresh failed: " + msg.err.Error()
	} else {
		m.listHealthy = true
		rows := append([]transport.AgentWithPane(nil), msg.agents.Agents...)
		sortAgentRows(rows, m.opts.SourceSession)
		m.rows = rows
		m.loaded = true
		m.lastRefresh = msg.at
		m.restoreSelection()
		m.updateConnection()
		m.statusText = fmt.Sprintf("%d agents", len(rows))
	}
	if m.refreshDirty {
		m.refreshDirty = false
		return m, m.requestRefresh()
	}
	return m, nil
}

func sortAgentRows(rows []transport.AgentWithPane, sourceSession string) {
	sourceSession = strings.TrimSpace(sourceSession)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Agent.Pinned != rows[j].Agent.Pinned {
			return rows[i].Agent.Pinned
		}
		leftSession := sessionName(rows[i])
		rightSession := sessionName(rows[j])
		leftCurrent := sourceSession != "" && leftSession == sourceSession
		rightCurrent := sourceSession != "" && rightSession == sourceSession
		if leftCurrent != rightCurrent {
			return leftCurrent
		}
		if leftSession != rightSession {
			return leftSession < rightSession
		}
		leftTab := tabKey(rows[i])
		rightTab := tabKey(rows[j])
		if leftTab != rightTab {
			return leftTab < rightTab
		}
		if !rows[i].Agent.CreatedAt.Equal(rows[j].Agent.CreatedAt) {
			return rows[i].Agent.CreatedAt.Before(rows[j].Agent.CreatedAt)
		}
		return rows[i].Agent.ID < rows[j].Agent.ID
	})
}

func (m Model) pinCmd(agentID string, pinned bool) tea.Cmd {
	return func() tea.Msg {
		response, err := m.client.SetAgentPinned(m.ctx, agentID, transport.SetAgentPinnedRequest{Pinned: pinned})
		return pinResultMsg{agentID: agentID, pinned: pinned, agent: response.Agent, err: err}
	}
}

func sessionName(record transport.AgentWithPane) string {
	if name := strings.TrimSpace(record.Pane.SessionID); name != "" {
		return name
	}
	return "ungrouped"
}

func tabKey(record transport.AgentWithPane) string {
	if id := strings.TrimSpace(record.Pane.TabID); id != "" {
		return "id\x00" + id
	}
	if name := strings.TrimSpace(record.Pane.TabName); name != "" {
		return "name\x00" + name
	}
	return "ungrouped"
}

func tabName(record transport.AgentWithPane) string {
	id := strings.TrimSpace(record.Pane.TabID)
	name := strings.TrimSpace(record.Pane.TabName)
	switch {
	case name != "" && id != "":
		return name + " (" + id + ")"
	case name != "":
		return name
	case id != "":
		return id
	default:
		return "ungrouped"
	}
}

func (m *Model) restoreSelection() {
	if len(m.rows) == 0 {
		m.selected, m.selectedID = 0, ""
		return
	}
	for index := range m.rows {
		if m.rows[index].Agent.ID == m.selectedID {
			m.selected = index
			return
		}
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	m.selectedID = m.rows[m.selected].Agent.ID
}

func (m Model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		agents, err := m.client.ListAgents(m.ctx)
		return refreshResultMsg{agents: agents, at: time.Now(), err: err}
	}
}

func (m Model) focusCmd(agentID string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.client.FocusAgent(m.ctx, agentID, transport.FocusAgentRequest{
			SourceSession:      m.opts.SourceSession,
			SourceZellijPaneID: m.opts.SourceZellijPaneID,
		})
		return focusResultMsg{agentID: agentID, err: err}
	}
}

func (m Model) connectStreamCmd() tea.Cmd {
	return func() tea.Msg {
		var stream *transport.EventStream
		var err error
		if client, ok := m.client.(eventTypeStreamClient); ok {
			stream, err = client.StreamEventsByType(m.ctx, agentStateChangedEventType)
		} else {
			stream, err = m.client.StreamEvents(m.ctx)
		}
		return streamReadyMsg{stream: stream, err: err}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.opts.RefreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (m Model) waitStreamCmd() tea.Cmd {
	stream := m.stream
	return func() tea.Msg {
		if stream == nil {
			return streamClosedMsg{}
		}
		select {
		case event, ok := <-stream.Events:
			if !ok {
				var err error
				select {
				case err = <-stream.Errors:
				default:
				}
				return streamClosedMsg{err: err}
			}
			return streamEventMsg{event: event}
		case err, ok := <-stream.Errors:
			if !ok {
				err = nil
			}
			return streamClosedMsg{err: err}
		case <-m.ctx.Done():
			return streamClosedMsg{err: m.ctx.Err()}
		}
	}
}

func (m *Model) closeStream() {
	if m.stream != nil && m.stream.Close != nil {
		_ = m.stream.Close()
	}
	m.stream = nil
}

func (m *Model) updateConnection() {
	if (m.listKnown && !m.listHealthy) || (m.streamKnown && !m.streamHealthy) {
		m.connection = "degraded"
		return
	}
	if m.listKnown && m.listHealthy && m.streamKnown && m.streamHealthy {
		m.connection = "live"
		return
	}
	m.connection = "connecting"
}

func errorText(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return err.Error()
}
