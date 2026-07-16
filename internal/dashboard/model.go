package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zellij-with-codeagent/internal/transport"
)

const (
	defaultRefreshInterval = 2 * time.Second
	defaultEventLimit      = 100
)

type panelFocus string
type detailTab string

const (
	focusTree   panelFocus = "tree"
	focusDetail panelFocus = "detail"
	tabOutput   detailTab  = "output"
	tabEvents   detailTab  = "events"
)

type Client interface {
	InspectRuntime(context.Context) (transport.InspectRuntimeResponse, error)
	RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
	Reconcile(context.Context) (transport.ReconcileResponse, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type Options struct {
	RefreshInterval time.Duration
	EventLimit      int
	TaskID          string
	ReadOnly        bool
	Capacity        int
}

type refreshResultMsg struct {
	status transport.InspectRuntimeResponse
	events transport.RecentEventsResponse
	at     time.Time
	err    error
}

type snapshotResultMsg struct {
	paneID string
	output string
	err    error
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

	width, height  int
	tree           []*treeNode
	rows           []treeRow
	expanded       map[string]bool
	selected       int
	selectedKey    string
	events         []transport.Event
	panes          []transport.Pane
	snapshots      map[string]string
	snapshotNext   string
	focus          panelFocus
	detailTab      detailTab
	treeViewport   viewport
	outputViewport viewport
	eventViewport  viewport
	loaded         bool
	lastRefresh    time.Time

	refreshing, refreshDirty, snapshotting bool
	stream                                 *transport.EventStream
	connection, statusText, actionText     string
	mode                                   string
	input                                  []rune
	inputPane                              string
	confirmTask                            string
	actionInFlight                         bool
}

func NewModel(ctx context.Context, client Client, opts Options) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = defaultRefreshInterval
	}
	if opts.EventLimit <= 0 {
		opts.EventLimit = defaultEventLimit
	}
	return Model{
		ctx:            ctx,
		client:         client,
		opts:           opts,
		expanded:       make(map[string]bool),
		snapshots:      make(map[string]string),
		refreshing:     true,
		connection:     "connecting",
		statusText:     "loading runtime",
		mode:           "normal",
		focus:          focusTree,
		detailTab:      tabOutput,
		outputViewport: newViewport(),
		eventViewport:  newViewport(),
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
		return m.handleRefreshResult(msg)
	case snapshotResultMsg:
		m.snapshotting = false
		if msg.err != nil {
			m.statusText = "snapshot failed: " + msg.err.Error()
		} else {
			m.snapshots[msg.paneID] = msg.output
			if pane := m.selectedPane(); pane != nil && pane.ID == msg.paneID {
				m.outputViewport.setContent(len(m.outputLines()), m.panelBodyHeight())
			}
			m.statusText = "snapshot refreshed for " + msg.paneID
		}
		if next := m.snapshotNext; next != "" {
			m.snapshotNext = ""
			return m, m.requestSnapshot(next)
		}
		return m, nil
	case actionResultMsg:
		return m.handleActionResult(msg)
	case streamReadyMsg:
		if msg.err != nil || msg.stream == nil {
			m.connection = "degraded"
			m.statusText = "event stream failed: " + errorText(msg.err, "unavailable")
			return m, nil
		}
		m.stream = msg.stream
		m.connection = "live"
		m.statusText = "event stream connected"
		return m, m.waitStreamCmd()
	case streamEventMsg:
		return m, tea.Batch(m.waitStreamCmd(), m.requestRefresh())
	case streamClosedMsg:
		m.connection = "degraded"
		m.statusText = "event stream closed: " + errorText(msg.err, "closed")
		m.closeStream()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
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

func (m Model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		status, statusErr := m.client.InspectRuntime(m.ctx)
		events, eventsErr := m.client.RecentEvents(m.ctx, m.opts.EventLimit)
		return refreshResultMsg{status: status, events: events, at: time.Now(), err: errors.Join(statusErr, eventsErr)}
	}
}

func (m Model) connectStreamCmd() tea.Cmd {
	return func() tea.Msg {
		stream, err := m.client.StreamEvents(m.ctx)
		return streamReadyMsg{stream: stream, err: err}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.opts.RefreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (m Model) waitStreamCmd() tea.Cmd {
	stream := m.stream
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
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

func (m Model) handleRefreshResult(msg refreshResultMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
	var cmds []tea.Cmd
	if msg.err != nil {
		m.statusText = "refresh failed: " + msg.err.Error()
	} else {
		oldSelected := m.selected
		oldPaneID := ""
		if pane := m.selectedPane(); pane != nil {
			oldPaneID = pane.ID
		}
		panes := filterPanesByTask(msg.status.Panes, m.opts.TaskID)
		events := filterEventsByTask(msg.events.Events, m.opts.TaskID)
		m.rebuildRows(panes, oldSelected)
		m.panes = panes
		m.events = filterSemanticEvents(events)
		m.loaded = true
		m.lastRefresh = msg.at
		if m.lastRefresh.IsZero() {
			m.lastRefresh = time.Now()
		}
		m.eventViewport.setContent(len(m.eventLines()), m.panelBodyHeight())
		m.statusText = fmt.Sprintf("runtime refreshed: panes=%d events=%d", len(m.panes), len(m.events))
		if pane := m.selectedPane(); pane != nil && (pane.ID != oldPaneID || !hasSnapshot(m.snapshots, pane.ID)) {
			cmds = append(cmds, m.requestSnapshot(pane.ID))
		}
	}
	if m.refreshDirty {
		m.refreshDirty = false
		cmds = append(cmds, m.requestRefresh())
	}
	return m, tea.Batch(cmds...)
}

func filterPanesByTask(panes []transport.Pane, taskID string) []transport.Pane {
	filtered := make([]transport.Pane, 0, len(panes))
	for _, pane := range panes {
		if taskID == "" || pane.TaskID == taskID {
			filtered = append(filtered, pane)
		}
	}
	return filtered
}

func filterEventsByTask(events []transport.Event, taskID string) []transport.Event {
	filtered := make([]transport.Event, 0, len(events))
	for _, event := range events {
		if taskID == "" || event.TaskID == taskID {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func (m *Model) rebuildRows(panes []transport.Pane, oldSelected int) {
	defer func() {
		m.treeViewport.setContent(len(m.rows), m.panelBodyHeight())
		m.treeViewport.ensureVisible(m.selected, len(m.rows), m.panelBodyHeight())
	}()
	newTree := buildTree(panes)
	allExpanded := defaultExpanded(newTree)
	if len(m.tree) > 0 {
		for key := range allExpanded {
			if value, ok := m.expanded[key]; ok {
				allExpanded[key] = value
			}
		}
	}
	m.tree = newTree
	m.expanded = allExpanded
	m.rows = flattenTree(m.tree, m.expanded)
	if len(m.rows) == 0 {
		m.selected, m.selectedKey = 0, ""
		return
	}
	for i, row := range m.rows {
		if m.selectedKey != "" && row.node.key == m.selectedKey {
			m.selected, m.selectedKey = i, row.node.key
			return
		}
	}
	if oldSelected < 0 {
		oldSelected = 0
	}
	if oldSelected >= len(m.rows) {
		oldSelected = len(m.rows) - 1
	}
	m.selected = oldSelected
	m.selectedKey = m.rows[m.selected].node.key
}

func filterSemanticEvents(events []transport.Event) []transport.Event {
	filtered := make([]transport.Event, 0, len(events))
	for _, event := range events {
		if event.Type != "raw_output" {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func (m Model) updateNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.closeStream()
		return m, tea.Quit
	case "tab":
		if m.focus == focusTree {
			m.focus = focusDetail
		} else {
			m.focus = focusTree
		}
		return m, nil
	case "up", "k":
		if m.focus == focusDetail {
			return m.scrollDetail(-1), nil
		}
		return m.moveSelection(-1)
	case "down", "j":
		if m.focus == focusDetail {
			return m.scrollDetail(1), nil
		}
		return m.moveSelection(1)
	case "left", "h":
		if m.focus == focusDetail {
			m.detailTab = tabOutput
		}
		return m, nil
	case "right", "l":
		if m.focus == focusDetail {
			m.detailTab = tabEvents
		}
		return m, nil
	case "pgup":
		if m.focus == focusDetail {
			return m.scrollDetail(-maxInt(1, m.panelBodyHeight()-1)), nil
		}
		return m, nil
	case "pgdown":
		if m.focus == focusDetail {
			return m.scrollDetail(maxInt(1, m.panelBodyHeight()-1)), nil
		}
		return m, nil
	case "g":
		if m.focus == focusDetail {
			if m.detailTab == tabEvents {
				m.eventViewport.top()
			} else {
				m.outputViewport.top()
			}
		}
		return m, nil
	case "G":
		if m.focus == focusDetail {
			height := m.panelBodyHeight()
			if m.detailTab == tabEvents {
				m.eventViewport.bottom(len(m.eventLines()), height)
			} else {
				m.outputViewport.bottom(len(m.outputLines()), height)
			}
		}
		return m, nil
	case "enter":
		if m.focus != focusTree || len(m.rows) == 0 || m.rows[m.selected].node.kind == "pane" {
			return m, nil
		}
		key := m.rows[m.selected].node.key
		m.expanded[key] = !m.expanded[key]
		m.rows = flattenTree(m.tree, m.expanded)
		m.restoreSelection()
		m.treeViewport.setContent(len(m.rows), m.panelBodyHeight())
		m.treeViewport.ensureVisible(m.selected, len(m.rows), m.panelBodyHeight())
		return m, nil
	case "s":
		if pane := m.selectedPane(); pane != nil {
			return m, m.requestSnapshot(pane.ID)
		}
		m.statusText = "snapshot requires a selected pane"
		return m, nil
	case "R":
		return m, m.requestRefresh()
	}
	return m, nil
}

func (m Model) moveSelection(delta int) (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	oldPaneID := ""
	if pane := m.selectedPane(); pane != nil {
		oldPaneID = pane.ID
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	m.selectedKey = m.rows[m.selected].node.key
	m.treeViewport.ensureVisible(m.selected, len(m.rows), m.panelBodyHeight())
	if pane := m.selectedPane(); pane != nil && pane.ID != oldPaneID {
		m.outputViewport = newViewport()
		return m, m.requestSnapshot(pane.ID)
	}
	return m, nil
}

func (m Model) panelBodyHeight() int {
	height := m.height - 7
	if height < 1 {
		return 1
	}
	return height
}

func (m Model) outputLines() []string {
	pane := m.selectedPane()
	if pane == nil {
		return []string{"Select a pane to inspect output"}
	}
	output := m.snapshots[pane.ID]
	if output == "" {
		return []string{"No snapshot output"}
	}
	return strings.Split(strings.TrimSuffix(output, "\n"), "\n")
}

func (m Model) eventLines() []string {
	if len(m.events) == 0 {
		return []string{"No semantic events"}
	}
	lines := make([]string, 0, len(m.events))
	for _, event := range m.events {
		line := event.Type
		if event.PaneID != "" {
			line += " pane=" + event.PaneID
		}
		if event.Message != "" {
			line += " " + event.Message
		}
		lines = append(lines, line)
	}
	return lines
}

func (m Model) scrollDetail(delta int) Model {
	height := m.panelBodyHeight()
	if m.detailTab == tabEvents {
		m.eventViewport.scroll(delta, len(m.eventLines()), height)
	} else {
		m.outputViewport.scroll(delta, len(m.outputLines()), height)
	}
	return m
}

func (m *Model) requestSnapshot(paneID string) tea.Cmd {
	if paneID == "" {
		return nil
	}
	if m.snapshotting {
		m.snapshotNext = paneID
		return nil
	}
	m.snapshotting = true
	return m.snapshotCmd(paneID)
}

func (m Model) snapshotCmd(paneID string) tea.Cmd {
	return func() tea.Msg {
		response, err := m.client.SnapshotOutput(m.ctx, paneID, transport.SnapshotOutputRequest{Full: true, ANSI: false})
		return snapshotResultMsg{paneID: paneID, output: response.Output, err: err}
	}
}

func (m Model) selectedPane() *transport.Pane {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return nil
	}
	return m.rows[m.selected].node.pane
}

func (m *Model) restoreSelection() {
	for i, row := range m.rows {
		if row.node.key == m.selectedKey {
			m.selected = i
			return
		}
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if len(m.rows) > 0 {
		m.selectedKey = m.rows[m.selected].node.key
	}
}

func (m *Model) closeStream() {
	if m.stream != nil && m.stream.Close != nil {
		_ = m.stream.Close()
	}
	m.stream = nil
}

func hasSnapshot(snapshots map[string]string, paneID string) bool {
	_, ok := snapshots[paneID]
	return ok
}

func errorText(err error, fallback string) string {
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		return fallback
	}
	return err.Error()
}
