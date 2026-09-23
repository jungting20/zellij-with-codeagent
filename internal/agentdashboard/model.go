package agentdashboard

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/listselector"
	"zellij-with-codeagent/internal/transport"
)

const defaultRefreshInterval = 2 * time.Second

const agentStateChangedEventType = "agent_state_changed"

type Client interface {
	SendInput(context.Context, string, transport.SendInputRequest) error
	ClosePane(context.Context, string) (transport.ClosePaneResponse, error)
	SetAgentTaskAlias(context.Context, string, transport.SetAgentTaskAliasRequest) (transport.SetAgentTaskAliasResponse, error)
	ListAgents(context.Context) (transport.ListAgentsResponse, error)
	FocusAgent(context.Context, string, transport.FocusAgentRequest) (transport.FocusAgentResponse, error)
	SetAgentPinned(context.Context, string, transport.SetAgentPinnedRequest) (transport.SetAgentPinnedResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
}

type eventTypeStreamClient interface {
	StreamEventsByType(context.Context, ...string) (*transport.EventStream, error)
}

type Options struct {
	SocketPath         string
	RequestTimeout     time.Duration
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

type stopResultMsg struct {
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

type panelSelection struct {
	index int
	id    string
}

type Model struct {
	worktrees *worktreeMenu // Transient menu and shell execution results.
	recent    *recentPopup  // Transient zoxide directory and agent selection.
	ticket    *ticketPopup
	followups *followupPopup // Transient editor; the daemon owns the durable queue.
	// Merge selection and debounce are transient dashboard state.
	mergeParent    string
	mergeChildren  []transport.AgentWithPane
	mergeSelected  int
	mergeBusy      bool
	mergeCooldown  time.Time
	worktreeBusy   bool
	worktreePath   string
	worktreeParent transport.AgentWithPane
	worktreePicker *listselector.Model
	worktreeNaming bool
	worktreePrompt textinput.Model
	worktreeError  string
	gitRunning     bool
	editorRunning  bool
	ctx            context.Context
	client         Client
	opts           Options

	width, height int
	rows          []transport.AgentWithPane
	selected      int
	selectedID    string
	focusPinned   bool
	selections    [2]panelSelection
	activities    []agentActivity
	activityNow   time.Time
	loaded        bool
	lastRefresh   time.Time
	listKnown     bool
	listHealthy   bool
	streamKnown   bool
	streamHealthy bool

	refreshing     bool
	refreshDirty   bool
	focusing       bool
	pinning        bool
	stopping       bool
	inputX, inputY int
	inputPane      string
	inputAgent     string
	prompt         textarea.Model
	inputSending   bool
	inputError     string
	aliasTarget    string
	aliasProject   string
	aliasCustom    bool
	aliasPrompt    textinput.Model
	aliasX, aliasY int
	aliasSelected  int
	aliasSaving    bool
	aliasError     string
	stream         *transport.EventStream
	connection     string
	statusText     string
	quitting       bool
}

func NewModel(ctx context.Context, client Client, opts Options) tea.Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = defaultRefreshInterval
	}
	return Model{
		ctx:         ctx,
		client:      client,
		opts:        opts,
		refreshing:  true,
		focusPinned: true,
		connection:  "connecting",
		statusText:  "loading agents",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.connectStreamCmd(), m.tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.saveSelection()
	m.activityNow = time.Now()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeTickets()
		m.resizeFollowups()
		if m.inputPane != "" {
			m.prompt.SetWidth(maxInt(1, m.inputPopupWidth()-4))
		}
		return m, nil
	case worktreeShellResultMsg:
		return m.handleWorktreeShellResult(msg)
	case recentDirectoriesMsg:
		return m.handleRecentDirectories(msg)
	case recentStartedMsg:
		return m.handleRecentStarted(msg)
	case ticketAgentConfigMsg:
		return m.handleTicketAgentConfig(msg)
	case ticketResultMsg:
		return m.handleTicketResult(msg)
	case followupResultMsg:
		return m.handleFollowupResult(msg)
	case refreshTickMsg:
		return m, tea.Batch(m.tickCmd(), m.requestRefresh(), m.requestFollowupRefresh())
	case refreshResultMsg:
		return m.handleRefresh(msg)
	case mergeResultMsg:
		m.mergeBusy = false
		m.mergeParent = ""
		m.mergeChildren = nil
		m.mergeCooldown = time.Now().Add(2 * time.Second)
		if msg.err != nil {
			m.statusText = "merge 요청 실패: " + msg.err.Error()
		} else {
			m.statusText = "merge 요청 전송됨"
		}
		return m, nil
	case worktreeCreatedMsg:
		return m.handleWorktreeCreated(msg)
	case worktreeSelectionMsg:
		return m.startWorktree(msg)
	case worktreeStartedMsg:
		m.worktreeBusy = false
		if msg.err != nil {
			m.statusText = "worktree launch failed: " + msg.err.Error()
			return m, nil
		}
		m.worktreePicker = nil
		m.statusText = "started child agent: " + m.worktreePath
		return m, m.requestRefresh()
	case lazygitResultMsg:
		m.gitRunning = false
		if msg.err != nil {
			m.statusText = "lazygit failed: " + msg.err.Error()
		} else {
			m.statusText = "returned from lazygit"
		}
		return m, m.requestRefresh()
	case editorResultMsg:
		m.editorRunning = false
		if msg.err != nil {
			m.inputError = "Neovim 편집 실패: " + msg.err.Error()
		} else {
			m.prompt.SetValue(msg.text)
			m.prompt.SetHeight(minInt(3, maxInt(1, m.prompt.LineCount())))
		}
		return m, m.prompt.Focus()
	case inputResultMsg:
		m.inputSending = false
		if msg.err != nil {
			m.inputError = "전송 실패: " + msg.err.Error()
			return m, nil
		}
		m.inputPane, m.inputAgent, m.inputError = "", "", ""
		m.prompt.Reset()
		m.statusText = "input sent to " + msg.agentID
		return m, nil
	case aliasResultMsg:
		m.aliasSaving = false
		if msg.err != nil {
			m.aliasError = "별명 저장 실패: " + msg.err.Error()
			return m, nil
		}
		for index := range m.rows {
			if m.rows[index].Agent.ID == msg.agentID {
				m.rows[index].Agent.TaskAlias = msg.alias
			}
		}
		m.aliasTarget = ""
		m.aliasError = ""
		m.statusText = "작업 별명 저장 완료"
		return m, nil
	case stopResultMsg:
		m.stopping = false
		if msg.err != nil {
			m.statusText = "stop failed: " + msg.err.Error()
			return m, nil
		}
		rows := make([]transport.AgentWithPane, 0, len(m.rows))
		for _, row := range m.rows {
			if row.Agent.ID != msg.agentID {
				rows = append(rows, row)
			}
		}
		m.rows = rows
		m.restoreSelection()
		m.statusText = "stopped " + msg.agentID
		return m, m.requestRefresh()
	case focusResultMsg:
		if m.worktrees != nil {
			m.worktrees.busy = false
			if msg.err != nil {
				m.worktrees.summary = "탭 이동 실패: " + msg.err.Error()
			}
		}
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
			m.recordActivity(msg.event)
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
	if m.followups != nil && m.followups.mode != "list" {
		var cmd tea.Cmd
		m.followups.prompt, cmd = m.followups.prompt.Update(msg)
		return m, cmd
	}
	if m.worktrees != nil && m.worktrees.mode == "send" {
		var cmd tea.Cmd
		m.worktrees.prompt, cmd = m.worktrees.prompt.Update(msg)
		return m, cmd
	}
	if m.ticket != nil && m.ticket.mode == "add" {
		var cmd tea.Cmd
		m.ticket.prompt, cmd = m.ticket.prompt.Update(msg)
		return m, cmd
	}
	if m.worktreeNaming {
		var cmd tea.Cmd
		m.worktreePrompt, cmd = m.worktreePrompt.Update(msg)
		return m, cmd
	}
	if m.aliasTarget != "" && m.aliasCustom {
		var cmd tea.Cmd
		m.aliasPrompt, cmd = m.aliasPrompt.Update(msg)
		return m, cmd
	}
	if m.inputPane != "" {
		var cmd tea.Cmd
		m.prompt, cmd = m.prompt.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.followups != nil {
		return m.updateFollowupKey(msg)
	}
	if m.worktrees != nil {
		return m.updateWorktreeMenuKey(msg)
	}
	if m.recent != nil {
		return m.updateRecentKey(msg)
	}
	if m.ticket != nil {
		return m.updateTicketKey(msg)
	}
	if m.mergeParent != "" {
		return m.updateMergeKey(msg)
	}
	if m.worktreeNaming {
		return m.updateWorktreeNameKey(msg)
	}
	if m.worktreePicker != nil {
		return m.updateWorktreeKey(msg)
	}
	if m.worktreeBusy {
		return m, nil
	}
	if m.editorRunning {
		return m, nil
	}
	if m.inputPane != "" {
		return m.updateInputKey(msg)
	}
	if m.aliasTarget != "" {
		return m.updateAliasKey(msg)
	}

	switch msg.String() {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		position := int(msg.String()[0] - '1')
		if position < len(m.rows) {
			m.selected = position
			m.selectedID = m.rows[m.selected].Agent.ID
			m.focusPinned = m.hierarchyRoot(m.rows[m.selected]).Agent.Pinned
		}
	case "t":
		return m.openTickets()
	case "f":
		return m.openFollowups()
	case "m":
		return m.openMerge()
	case "g":
		return m.openWorktreeMenu()
	case "n":
		return m.openRecent()
	case "i":
		return m.openInput()
	case "I":
		return m.openInputEditor()
	case "a":
		return m.openAliasPicker()
	case "q", "ctrl+c":
		m.closeStream()
		m.quitting = true
		return m, tea.Quit
	case "up", "k", "down", "j":
		indices := m.panelIndices(m.focusPinned)
		for position, index := range indices {
			if index != m.selected {
				continue
			}
			if msg.String() == "up" || msg.String() == "k" {
				position--
			} else {
				position++
			}
			if position >= 0 && position < len(indices) {
				m.selected = indices[position]
				m.selectedID = m.rows[m.selected].Agent.ID
			}
			break
		}
	case "tab", "shift+tab":
		m.focusPinned = !m.focusPinned
		m.restoreSelection()
	case "R":
		return m, m.requestRefresh()
	case "d":
		if len(m.panelIndices(m.focusPinned)) == 0 || m.stopping || m.pinning || m.focusing {
			return m, nil
		}
		row := m.rows[m.selected]
		if row.Agent.Pinned {
			return m, nil
		}
		paneID := row.Agent.PaneID
		if paneID == "" {
			paneID = row.Pane.ID
		}
		if paneID == "" {
			m.statusText = "stop failed: agent has no managed pane"
			return m, nil
		}
		m.stopping = true
		m.statusText = "stopping " + row.Agent.ID
		return m, m.stopCmd(row.Agent.ID, paneID)
	case " ":
		if len(m.panelIndices(m.focusPinned)) == 0 || m.pinning || m.stopping {
			return m, nil
		}
		m.pinning = true
		agent := m.rows[m.selected].Agent
		return m, m.pinCmd(agent.ID, !agent.Pinned)
	case "enter":
		if len(m.panelIndices(m.focusPinned)) == 0 || m.focusing || m.stopping {
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
		m.recordRefreshActivities(rows)
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
	defer orderChildren(rows)
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

func panelIndex(pinned bool) int {
	if pinned {
		return 0
	}
	return 1
}

func (m Model) panelIndices(pinned bool) []int {
	var indices []int
	for index, row := range m.rows {
		if m.hierarchyRoot(row).Agent.Pinned == pinned {
			indices = append(indices, index)
		}
	}
	return indices
}

func (m *Model) saveSelection() {
	for position, index := range m.panelIndices(m.focusPinned) {
		if index == m.selected {
			m.selections[panelIndex(m.focusPinned)] = panelSelection{index: position, id: m.rows[index].Agent.ID}
			return
		}
	}
}

func (m *Model) restoreSelection() {
	for _, pinned := range []bool{true, false} {
		indices := m.panelIndices(pinned)
		selection := &m.selections[panelIndex(pinned)]
		if len(indices) == 0 {
			*selection = panelSelection{}
			if pinned == m.focusPinned {
				m.selected, m.selectedID = 0, ""
			}
			continue
		}
		for position, index := range indices {
			if m.rows[index].Agent.ID == selection.id {
				selection.index = position
				break
			}
		}
		selection.index = maxInt(0, minInt(selection.index, len(indices)-1))
		index := indices[selection.index]
		selection.id = m.rows[index].Agent.ID
		if pinned == m.focusPinned {
			m.selected, m.selectedID = index, selection.id
		}
	}
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

func (m Model) stopCmd(agentID, paneID string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.client.ClosePane(m.ctx, paneID)
		return stopResultMsg{agentID: agentID, err: err}
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
