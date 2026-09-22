package agentdashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"zellij-with-codeagent/internal/followup"
	"zellij-with-codeagent/internal/transport"
)

type fakeFollowupClient struct {
	*fakeClient
	queue    followup.Queue
	getErr   error
	writeErr error
	gets     []string
	writes   []transport.UpdateAgentFollowupsRequest
	targets  []string
}

func (f *fakeFollowupClient) GetAgentFollowups(_ context.Context, id string) (transport.AgentFollowupsResponse, error) {
	f.gets = append(f.gets, id)
	return transport.AgentFollowupsResponse{Queue: f.queue}, f.getErr
}

func (f *fakeFollowupClient) UpdateAgentFollowups(_ context.Context, id string, req transport.UpdateAgentFollowupsRequest) (transport.AgentFollowupsResponse, error) {
	f.writes = append(f.writes, req)
	f.targets = append(f.targets, id)
	return transport.AgentFollowupsResponse{Queue: f.queue}, f.writeErr
}

func followupModel(t *testing.T, items ...followup.Item) (Model, *fakeFollowupClient) {
	t.Helper()
	client := &fakeFollowupClient{fakeClient: &fakeClient{}, queue: followup.Queue{AgentID: "target", PaneID: "managed-target", Items: items}}
	m := inputModel(t, client.fakeClient, false)
	m.client = client
	m.width, m.height, m.loaded = 90, 24, true
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if cmd == nil || m.followups == nil {
		t.Fatal("missing followup load")
	}
	next, _ := m.Update(cmd())
	return concreteModel(t, next), client
}

func followupKey(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	return inputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func TestFollowupAddPreservesTargetMultilineAndRequestIDOnRetry(t *testing.T) {
	m, client := followupModel(t)
	m, _ = followupKey(t, m, "a")
	m, _ = followupKey(t, m, "테스트 추가")
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m, _ = followupKey(t, m, "q f p 문서 수정")
	text := "테스트 추가\nq f p 문서 수정"
	if m.followups.prompt.Value() != text {
		t.Fatalf("draft = %q", m.followups.prompt.Value())
	}
	next, _ := m.Update(refreshResultMsg{agents: transport.ListAgentsResponse{Agents: []transport.AgentWithPane{record("other", "codex", "working", time.Now())}}})
	m = concreteModel(t, next)
	client.writeErr = errors.New("response lost")
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("missing save command")
	}
	m, duplicate := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if duplicate != nil {
		t.Fatal("duplicate save command")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.followups.mode != "add" {
		t.Fatal("dismissed in-flight save")
	}
	next, _ = m.Update(cmd())
	m = concreteModel(t, next)
	if len(client.writes) != 1 || client.targets[0] != "target" || client.writes[0].Text != text || client.writes[0].Action != "add" {
		t.Fatalf("write = %+v, targets = %v", client.writes, client.targets)
	}
	if _, err := uuid.Parse(client.writes[0].RequestID); err != nil {
		t.Fatalf("request ID is not UUID: %v", err)
	}
	if m.followups.prompt.Value() != text || !strings.Contains(m.View(), "response lost") {
		t.Fatal("failed save lost draft or error")
	}
	client.writeErr = nil
	client.queue.Items = []followup.Item{{ID: "new", Text: text, State: "queued"}}
	m, cmd = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = m.Update(cmd())
	m = concreteModel(t, next)
	if client.writes[0].RequestID != client.writes[1].RequestID || m.followups.mode != "list" || m.quitting {
		t.Fatal("retry did not reuse request ID and return to list")
	}
	if client.inputCalls != 0 || client.closeCalls != 0 || client.focusCalls != 0 {
		t.Fatal("followup keys escaped to dashboard actions")
	}
}

func TestFollowupEditCancelOnlyQueuedItems(t *testing.T) {
	for _, state := range []string{"sending", "sent", "needs_attention", "canceled"} {
		t.Run(state, func(t *testing.T) {
			m, client := followupModel(t, followup.Item{ID: "item", State: state})
			for _, key := range []string{"e", "d"} {
				var cmd tea.Cmd
				m, cmd = followupKey(t, m, key)
				if cmd != nil || m.followups.mode != "list" || !strings.Contains(m.followups.err, "대기 중인") {
					t.Fatalf("%s allowed in state %s", key, state)
				}
			}
			if len(client.writes) != 0 || client.closeCalls != 0 {
				t.Fatal("invalid mutation reached client")
			}
		})
	}
	m, client := followupModel(t, followup.Item{ID: "queued-item", Text: "old", State: "queued"})
	m, _ = followupKey(t, m, "e")
	if m.followups.prompt.Value() != "old" {
		t.Fatal("edit did not load text")
	}
	m.followups.prompt.SetValue("new\ntext")
	m, cmd := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	if req := client.writes[0]; req.Action != "edit" || req.ItemID != "queued-item" || req.Text != "new\ntext" {
		t.Fatalf("edit request = %+v", req)
	}
	m, cmd = followupKey(t, m, "d")
	cmd()
	if req := client.writes[1]; req.Action != "cancel" || req.ItemID != "queued-item" {
		t.Fatalf("cancel request = %+v", req)
	}
}

func TestFollowupPauseResumeAndResolve(t *testing.T) {
	m, client := followupModel(t, followup.Item{ID: "uncertain", State: "needs_attention", Error: "timeout"})
	m, cmd := followupKey(t, m, "p")
	client.queue.Paused = true
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	if client.writes[0].Action != "pause" || !m.followups.queue.Paused {
		t.Fatal("pause not applied")
	}
	m, cmd = followupKey(t, m, "c")
	next, _ = m.Update(cmd())
	m = concreteModel(t, next)
	if req := client.writes[1]; req.Action != "resolve" || req.ItemID != "uncertain" {
		t.Fatalf("resolve request = %+v", req)
	}
	if !m.followups.queue.Paused || !strings.Contains(m.View(), "일시정지 유지") {
		t.Fatal("resolve must retain pause")
	}
	m, cmd = followupKey(t, m, "p")
	cmd()
	if client.writes[2].Action != "resume" {
		t.Fatal("explicit resume not sent")
	}
}

func TestFollowupCanExcludeUncertainDeliveryWithoutResuming(t *testing.T) {
	m, client := followupModel(t, followup.Item{ID: "uncertain", State: "needs_attention", Error: "timeout"})
	m.followups.queue.ActiveItemID = "uncertain"
	m.followups.queue.Paused = true
	client.queue.Paused = true
	client.queue.Items = []followup.Item{{ID: "uncertain", State: "canceled"}}
	m, cmd := followupKey(t, m, "d")
	if cmd == nil {
		t.Fatal("uncertain delivery cannot be excluded")
	}
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	if req := client.writes[0]; req.Action != "cancel" || req.ItemID != "uncertain" {
		t.Fatalf("exclude request = %+v", req)
	}
	if !m.followups.queue.Paused || m.followups.queue.Items[0].State != "canceled" {
		t.Fatal("excluding uncertain delivery must keep queue paused")
	}
}

func TestFollowupRefreshPreservesDraftAndIgnoresStaleReads(t *testing.T) {
	m, client := followupModel(t, followup.Item{ID: "first", Text: "first", State: "queued"})
	m, _ = followupKey(t, m, "e")
	m.followups.prompt.SetValue("draft in progress")
	refresh := m.requestFollowupRefresh()
	client.queue.Items = []followup.Item{{ID: "first", Text: "first", State: "sent"}}
	next, _ := m.Update(refresh())
	m = concreteModel(t, next)
	if m.followups.mode != "edit" || m.followups.prompt.Value() != "draft in progress" || m.followups.itemID != "first" {
		t.Fatal("refresh discarded edit")
	}
	if m.followups.queue.Items[0].State != "sent" {
		t.Fatal("draft prevented state refresh")
	}
	refresh = m.requestFollowupRefresh()
	stale := refresh()
	m, save := inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	client.queue.Items = []followup.Item{{ID: "first", Text: "saved", State: "queued"}}
	next, _ = m.Update(save())
	m = concreteModel(t, next)
	next, _ = m.Update(stale)
	m = concreteModel(t, next)
	if m.followups.queue.Items[0].Text != "saved" {
		t.Fatal("stale read overwrote mutation")
	}
	refresh = m.requestFollowupRefresh()
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m, reopen := followupKey(t, m, "f")
	next, _ = m.Update(refresh())
	m = concreteModel(t, next)
	if m.followups.loaded {
		t.Fatal("closed popup response applied to new popup")
	}
	next, _ = m.Update(reopen())
	if !concreteModel(t, next).followups.loaded {
		t.Fatal("new popup failed to load")
	}
}

func TestFollowupEmptyCancelUnavailableAndRefreshErrors(t *testing.T) {
	m := inputModel(t, &fakeClient{}, false)
	m, cmd := followupKey(t, m, "f")
	if cmd != nil || m.followups != nil || !strings.Contains(m.statusText, "지원하지 않는") {
		t.Fatal("optional client unavailable should be reported")
	}
	m.rows = nil
	m, cmd = followupKey(t, m, "f")
	if cmd != nil || m.followups != nil {
		t.Fatal("opened with empty selection")
	}
	m, client := followupModel(t)
	m, _ = followupKey(t, m, "a")
	m, cmd = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !strings.Contains(m.followups.err, "입력하세요") {
		t.Fatal("empty draft accepted")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.followups == nil || m.followups.mode != "list" || len(client.writes) != 0 {
		t.Fatal("cancel draft should return to list")
	}
	client.getErr = errors.New("daemon unavailable")
	m, cmd = followupKey(t, m, "r")
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	if !strings.Contains(m.View(), "daemon unavailable") || m.followups.loading {
		t.Fatal("load error hidden or refresh stuck")
	}
	m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.followups != nil {
		t.Fatal("popup did not close")
	}
}

func TestFollowupViewStateBadgesAndSmallScreens(t *testing.T) {
	m, client := followupModel(t,
		followup.Item{ID: "a", Text: "한글 지시 첫째", State: "queued"},
		followup.Item{ID: "b", Text: "한글 지시 둘째", State: "needs_attention", Error: "delivery uncertain"},
	)
	client.queue.Paused = true
	client.queue.Reason = "확인할 때까지 보류"
	m, cmd := followupKey(t, m, "r")
	next, _ := m.Update(cmd())
	m = concreteModel(t, next)
	m, _ = followupKey(t, m, "j")
	view := ansi.Strip(m.View())
	t.Log("\n" + view)
	for _, want := range []string{"후속 지시", "일시정지", "1. [대기]", "2. [전송 확인 필요]", "delivery uncertain", "c 전달 확인", "확인할 때까지 보류"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if badge := followupBadge(m.rows[0].Agent); badge != "[후속 2 확인!]" {
		t.Fatalf("badge = %q", badge)
	}
	if row := ansi.Strip(m.rowView(m.rows[0], false, 80, 1)); !strings.Contains(row, "[후속 2 확인!]") {
		t.Fatalf("agent row missing followup badge: %s", row)
	}
	for _, size := range [][2]int{{120, 20}, {80, 14}, {30, 8}, {8, 3}, {1, 1}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = concreteModel(t, next)
		for _, mode := range []string{"list", "add"} {
			if mode == "add" {
				m, _ = followupKey(t, m, "a")
			}
			lines := strings.Split(ansi.Strip(m.View()), "\n")
			if len(lines) != size[1] {
				t.Fatalf("size %v mode %s: height %d", size, mode, len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("size %v mode %s: overflow %q", size, mode, line)
				}
			}
			if mode == "add" {
				m, _ = inputKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			}
		}
	}
}
