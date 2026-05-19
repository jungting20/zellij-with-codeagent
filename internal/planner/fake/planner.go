package fake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	"zellij-with-codeagent/internal/transport"
)

const (
	DefaultTaskID       = "fake-planner-task"
	DefaultAgentID      = "fake-planner"
	DefaultTabName      = "agentd-fake-planner"
	DefaultIdleTimeout  = 30 * time.Second
	DefaultTotalTimeout = 5 * time.Minute
)

type Client interface {
	CreatePane(context.Context, transport.CreatePaneRequest) (transport.CreatePaneResponse, error)
	SendInput(context.Context, string, transport.SendInputRequest) error
	SnapshotOutput(context.Context, string, transport.SnapshotOutputRequest) (transport.SnapshotOutputResponse, error)
	RecentEvents(context.Context, int, ...string) (transport.RecentEventsResponse, error)
	StreamEvents(context.Context) (*transport.EventStream, error)
	Cleanup(context.Context, transport.CleanupRequest) (transport.CleanupResponse, error)
}

type Options struct {
	TaskID       string
	AgentID      string
	TabName      string
	CWD          string
	LeaveOpen    bool
	IdleTimeout  time.Duration
	TotalTimeout time.Duration
}

type Result struct {
	TaskID  string
	Panes   []transport.Pane
	Events  []transport.Event
	Cleanup *transport.CleanupResponse
}

type Planner struct {
	client Client
	opts   Options
}

func New(client Client, opts Options) *Planner {
	if opts.TaskID == "" {
		opts.TaskID = DefaultTaskID
	}
	if opts.AgentID == "" {
		opts.AgentID = DefaultAgentID
	}
	if opts.TabName == "" {
		opts.TabName = DefaultTabName
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.TotalTimeout == 0 {
		opts.TotalTimeout = DefaultTotalTimeout
	}
	return &Planner{client: client, opts: opts}
}

func (p *Planner) Run(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, p.opts.TotalTimeout)
	defer cancel()

	result := Result{TaskID: p.opts.TaskID}
	stream, streamErr := p.client.StreamEvents(ctx)
	if streamErr != nil {
		// RecentEvents and snapshots are still enough to report a useful failure
		// or continue in tests where live streaming is intentionally absent.
		stream = nil
	}
	if stream != nil {
		defer stream.Close()
	}

	coder, err := p.createPane(ctx, "coder", "coder", true, nil)
	if err != nil {
		return result, err
	}
	result.Panes = append(result.Panes, coder)
	if err := p.waitForSnapshot(ctx, coder.ID, "agentd_fake_planner_ready:"+coder.ID); err != nil {
		result.Cleanup = p.cleanupBestEffort(ctx)
		return result, err
	}
	if coder.ZellijTabID == nil {
		result.Cleanup = p.cleanupBestEffort(ctx)
		return result, errors.New("fake planner: first pane did not return zellij tab id")
	}

	tabID := coder.ZellijTabID
	roles := []struct {
		id   string
		role string
	}{
		{id: "test", role: "test"},
		{id: "build", role: "build"},
		{id: "server", role: "server"},
		{id: "log", role: "log"},
	}
	panes := map[string]transport.Pane{"coder": coder}
	for _, req := range roles {
		pane, err := p.createPane(ctx, req.id, req.role, false, tabID)
		if err != nil {
			result.Cleanup = p.cleanupBestEffort(ctx)
			return result, err
		}
		panes[req.id] = pane
		result.Panes = append(result.Panes, pane)
		if err := p.waitForSnapshot(ctx, pane.ID, "agentd_fake_planner_ready:"+pane.ID); err != nil {
			result.Cleanup = p.cleanupBestEffort(ctx)
			return result, err
		}
	}

	steps := []struct {
		paneID    string
		input     string
		waitType  string
		waitPane  string
		wantEvent bool
	}{
		{paneID: panes["test"].ID, input: "handoff coder to test\n"},
		{paneID: panes["build"].ID, input: "handoff test to build\n"},
		{paneID: panes["server"].ID, input: "start server :3000\n", waitType: string(eventbus.TypeServerReady), waitPane: panes["server"].ID, wantEvent: true},
		{paneID: panes["test"].ID, input: "fail\n", waitType: string(eventbus.TypeTestFailed), waitPane: panes["test"].ID, wantEvent: true},
		{paneID: panes["test"].ID, input: "pass\n", waitType: string(eventbus.TypeTestPassed), waitPane: panes["test"].ID, wantEvent: true},
		{paneID: panes["log"].ID, input: "handoff test results to log\n"},
	}

	seen := map[string]bool{}
	for _, step := range steps {
		if err := p.client.SendInput(ctx, step.paneID, transport.SendInputRequest{Text: step.input}); err != nil {
			result.Cleanup = p.cleanupBestEffort(ctx)
			return result, err
		}
		if !step.wantEvent {
			continue
		}
		event, err := p.waitForEvent(ctx, stream, step.waitType, step.waitPane, seen)
		if err != nil {
			result.Cleanup = p.cleanupBestEffort(ctx)
			return result, err
		}
		result.Events = append(result.Events, event)
	}

	if !p.opts.LeaveOpen {
		cleanup, err := p.client.Cleanup(ctx, transport.CleanupRequest{TaskID: p.opts.TaskID})
		result.Cleanup = &cleanup
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (p *Planner) createPane(ctx context.Context, suffix, role string, newTab bool, tabID *int) (transport.Pane, error) {
	id := "fake-planner-" + suffix
	response, err := p.client.CreatePane(ctx, transport.CreatePaneRequest{
		ID:          id,
		TaskID:      p.opts.TaskID,
		AgentID:     p.opts.AgentID,
		Role:        role,
		Name:        id,
		NewTab:      newTab,
		TabName:     p.opts.TabName,
		ZellijTabID: tabID,
		Command:     PaneCommand(id),
		CWD:         p.opts.CWD,
	})
	if err != nil {
		return transport.Pane{}, err
	}
	return response.Pane, nil
}

func (p *Planner) waitForSnapshot(ctx context.Context, paneID, marker string) error {
	ctx, cancel := context.WithTimeout(ctx, p.opts.IdleTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		snapshot, err := p.client.SnapshotOutput(ctx, paneID, transport.SnapshotOutputRequest{Full: true})
		if err == nil && strings.Contains(snapshot.Output, marker) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("fake planner: waiting for snapshot marker %q in %s: %w", marker, paneID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *Planner) waitForEvent(ctx context.Context, stream *transport.EventStream, eventType, paneID string, seen map[string]bool) (transport.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, p.opts.IdleTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-eventChannel(stream):
			if !ok {
				stream = nil
				continue
			}
			if p.matchesEvent(event, eventType, paneID, seen) {
				return event, nil
			}
		case <-ticker.C:
			event, ok := p.findRecentEvent(ctx, eventType, paneID, seen)
			if ok {
				return event, nil
			}
		case <-ctx.Done():
			return transport.Event{}, fmt.Errorf("fake planner: waiting for %s on %s: %w", eventType, paneID, ctx.Err())
		}
	}
}

func eventChannel(stream *transport.EventStream) <-chan transport.Event {
	if stream == nil {
		return nil
	}
	return stream.Events
}

func (p *Planner) findRecentEvent(ctx context.Context, eventType, paneID string, seen map[string]bool) (transport.Event, bool) {
	recent, err := p.client.RecentEvents(ctx, 20, eventType)
	if err != nil {
		return transport.Event{}, false
	}
	for _, event := range recent.Events {
		if p.matchesEvent(event, eventType, paneID, seen) {
			return event, true
		}
	}
	return transport.Event{}, false
}

func (p *Planner) matchesEvent(event transport.Event, eventType, paneID string, seen map[string]bool) bool {
	if event.Type != eventType || event.PaneID != paneID {
		return false
	}
	key := event.Type + ":" + event.PaneID + ":" + event.Message
	if seen[key] {
		return false
	}
	seen[key] = true
	return true
}

func (p *Planner) cleanupBestEffort(ctx context.Context) *transport.CleanupResponse {
	if p.opts.LeaveOpen {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	cleanup, err := p.client.Cleanup(cleanupCtx, transport.CleanupRequest{TaskID: p.opts.TaskID})
	if err != nil && len(cleanup.Closed) == 0 && len(cleanup.Failed) == 0 && len(cleanup.Skipped) == 0 {
		return nil
	}
	return &cleanup
}

func PaneCommand(name string) []string {
	script := fmt.Sprintf(`name=%q
printf 'agentd_fake_planner_ready:%%s\n' "$name"
while IFS= read -r line; do
  printf 'agentd_fake_planner_input:%%s:%%s\n' "$name" "$line"
  case "$line" in
    *":3000"*|*"start server"*) printf 'server listening on :3000 for %%s\n' "$name" ;;
    *"fail"*) printf -- '--- FAIL: %%s\n' "$name" ;;
    *"pass"*) printf 'ok %%s\n' "$name" ;;
    *"handoff"*) printf 'agentd_fake_planner_handoff:%%s:%%s\n' "$name" "$line" ;;
  esac
done`, name)
	return []string{"sh", "-lc", script}
}
