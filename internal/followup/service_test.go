package followup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testRepository struct {
	mu         sync.Mutex
	queues     map[string]Queue
	saves      []Queue
	beforeSave func(Queue) error
}

func (r *testRepository) Load() ([]Queue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []Queue
	for _, q := range r.queues {
		result = append(result, clone(q))
	}
	return result, nil
}

func (r *testRepository) Save(_ context.Context, q Queue) error {
	r.mu.Lock()
	hook := r.beforeSave
	r.mu.Unlock()
	if hook != nil {
		if err := hook(clone(q)); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.queues == nil {
		r.queues = make(map[string]Queue)
	}
	r.queues[q.AgentID] = clone(q)
	r.saves = append(r.saves, clone(q))
	return nil
}

func (r *testRepository) saved(id string) Queue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return clone(r.queues[id])
}

func (r *testRepository) onSave(hook func(Queue) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeSave = hook
}

type followupHarness struct {
	t           *testing.T
	mu          sync.Mutex
	now         time.Time
	observation Observation
	observeErr  error
	sent        []string
	beforeSend  func(Observation, string) error
	ids         atomic.Uint64
	service     *Service
}

func newFollowupHarness(t *testing.T, repository Repository) *followupHarness {
	t.Helper()
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	h := &followupHarness{t: t, now: now, observation: Observation{
		AgentID: "agent", PaneID: "pane", OwnershipToken: "owner", Running: true,
		Ready: true, State: "idle", Epoch: 7, WorkingRevision: 10, ObservedAt: now,
	}}
	service, err := New(h.options(repository))
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	return h
}

func (h *followupHarness) options(repository Repository) Options {
	return Options{
		Repository: repository,
		Now:        func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		NewID:      func() string { return fmt.Sprintf("instruction-%d", h.ids.Add(1)) },
		Observe: func(context.Context, string) (Observation, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.observation, h.observeErr
		},
		Send: func(_ context.Context, observation Observation, text string) error {
			h.mu.Lock()
			h.sent = append(h.sent, text)
			hook := h.beforeSend
			h.mu.Unlock()
			if hook != nil {
				return hook(observation, text)
			}
			return nil
		},
	}
}

func (h *followupHarness) update(update Update) Queue {
	h.t.Helper()
	q, err := h.service.Update(context.Background(), "agent", update)
	if err != nil {
		h.t.Fatal(err)
	}
	return q
}

func (h *followupHarness) add(text string) Queue {
	h.t.Helper()
	return h.update(Update{Action: "add", Text: text})
}

func (h *followupHarness) queue() Queue {
	h.t.Helper()
	q, err := h.service.Get(context.Background(), "agent")
	if err != nil {
		h.t.Fatal(err)
	}
	return q
}

func (h *followupHarness) observe(change func(*Observation, time.Time)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(time.Second)
	change(&h.observation, h.now)
}

func (h *followupHarness) sentTexts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.sent...)
}

func (h *followupHarness) step() { h.service.Step(context.Background()) }

func TestFIFORequiresObservedWorkThenFreshIdleBeforeNextInstruction(t *testing.T) {
	repo := &testRepository{}
	h := newFollowupHarness(t, repo)
	q := h.add("first")
	firstID := q.Items[0].ID
	h.add("second")
	h.beforeSend = func(_ Observation, text string) error {
		claim := repo.saved("agent")
		if claim.Phase != Sending || claim.ActiveItemID == "" {
			t.Errorf("input %q had no durable sending claim: %+v", text, claim)
		}
		return nil
	}
	h.step()
	q = h.queue()
	if sent := h.sentTexts(); len(sent) != 1 || sent[0] != "first" || q.Phase != AwaitingWork || q.ActiveItemID != firstID {
		t.Fatalf("first dispatch: sent=%v, queue=%+v", sent, q)
	}
	for i := 0; i < 5; i++ {
		h.step()
	}
	if len(h.sentTexts()) != 1 {
		t.Fatal("duplicate idle ticks sent another instruction")
	}
	h.observe(func(o *Observation, now time.Time) { o.LastWorkingAt = now; o.ObservedAt = now.Add(time.Second) })
	h.step()
	if h.queue().Phase != AwaitingWork {
		t.Fatal("timestamp alone counted as a new work observation")
	}
	h.observe(func(o *Observation, now time.Time) {
		o.WorkingRevision++
		o.LastWorkingAt = q.AttemptedAt.Add(-time.Second)
		o.ObservedAt = now
	})
	h.step()
	if h.queue().Phase != AwaitingWork {
		t.Fatal("pre-delivery work timestamp completed the instruction")
	}
	h.observe(func(o *Observation, now time.Time) {
		o.Ready = false
		o.State = "working"
		o.LastWorkingAt = now
		o.ObservedAt = now
	})
	h.step()
	if h.queue().Phase != Working || len(h.sentTexts()) != 1 {
		t.Fatal("working observation did not retain one active instruction")
	}
	h.observe(func(o *Observation, _ time.Time) { o.Ready = true; o.State = "idle"; o.ObservedAt = o.LastWorkingAt })
	h.step()
	if len(h.sentTexts()) != 1 {
		t.Fatal("idle observation no newer than work released the next instruction")
	}
	h.observe(func(o *Observation, now time.Time) { o.ObservedAt = now })
	h.step()
	q = h.queue()
	if sent := h.sentTexts(); len(sent) != 2 || sent[1] != "second" || q.ActiveItemID == firstID || q.Phase != AwaitingWork {
		t.Fatalf("FIFO release: sent=%v, queue=%+v", sent, q)
	}
}

func TestPausedAndUncertainObservationsNeverDispatch(t *testing.T) {
	for _, state := range []string{"blocked", "unknown", "working", "idle"} {
		t.Run(state, func(t *testing.T) {
			h := newFollowupHarness(t, nil)
			h.add("later")
			h.observe(func(o *Observation, _ time.Time) { o.State = state; o.Ready = false })
			h.step()
			if len(h.sentTexts()) != 0 {
				t.Fatalf("dispatched without a definite ready observation: %s", state)
			}
			h.update(Update{Action: "pause"})
			h.observe(func(o *Observation, now time.Time) { o.State = "idle"; o.Ready = true; o.ObservedAt = now })
			h.step()
			if len(h.sentTexts()) != 0 {
				t.Fatal("paused queue dispatched")
			}
			h.update(Update{Action: "resume"})
			h.step()
			if len(h.sentTexts()) != 1 {
				t.Fatal("resumed ready queue did not dispatch")
			}
		})
	}
}

func TestPartialSendErrorRequiresExplicitResolutionAndNeverRetries(t *testing.T) {
	h := newFollowupHarness(t, nil)
	q := h.add("first")
	h.add("second")
	h.beforeSend = func(Observation, string) error { return errors.New("text written; enter delivery unknown") }
	h.step()
	q = h.queue()
	if q.Phase != NeedsAttention || !q.Paused || q.Items[0].State != NeedsAttention || q.Items[0].Error == "" {
		t.Fatalf("partial delivery state=%+v", q)
	}
	for i := 0; i < 5; i++ {
		h.step()
	}
	if len(h.sentTexts()) != 1 {
		t.Fatal("uncertain delivery was retried")
	}
	if _, err := h.service.Update(context.Background(), "agent", Update{Action: "resume"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume uncertain queue error=%v", err)
	}
	q = h.update(Update{Action: "resolve", ItemID: q.Items[0].ID})
	if q.Phase != Ready || !q.Paused || q.ActiveItemID != "" {
		t.Fatalf("resolution should retain manual pause: %+v", q)
	}
	h.beforeSend = nil
	h.step()
	if len(h.sentTexts()) != 1 {
		t.Fatal("resolve automatically dispatched another instruction")
	}
	h.update(Update{Action: "resume"})
	h.step()
	if sent := h.sentTexts(); len(sent) != 2 || sent[1] != "second" {
		t.Fatalf("resolved queue sent=%v", sent)
	}
}

func TestEditedAndCanceledItemsUseLatestQueuedContent(t *testing.T) {
	h := newFollowupHarness(t, nil)
	q := h.add("discard")
	h.add("old")
	h.update(Update{Action: "cancel", ItemID: q.Items[0].ID})
	q = h.queue()
	h.update(Update{Action: "edit", ItemID: q.Items[1].ID, Text: "updated"})
	h.step()
	if sent := h.sentTexts(); len(sent) != 1 || sent[0] != "updated" {
		t.Fatalf("edited FIFO sent=%v", sent)
	}
	for _, action := range []string{"edit", "cancel"} {
		_, err := h.service.Update(context.Background(), "agent", Update{Action: action, ItemID: q.Items[1].ID, Text: "too late"})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("%s delivered item error=%v", action, err)
		}
	}
}

func TestConcurrentTicksAndMutationsSerializeWithDispatch(t *testing.T) {
	h := newFollowupHarness(t, nil)
	q := h.add("claimed")
	entered, release := make(chan struct{}), make(chan struct{})
	h.beforeSend = func(Observation, string) error { close(entered); <-release; return nil }
	stepped := make(chan struct{})
	go func() { h.step(); close(stepped) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("send did not begin")
	}
	var ticks sync.WaitGroup
	for i := 0; i < 20; i++ {
		ticks.Add(1)
		go func() { defer ticks.Done(); h.step() }()
	}
	ticks.Wait()
	results := make(chan error, 2)
	for _, action := range []string{"edit", "cancel"} {
		go func(action string) {
			_, err := h.service.Update(context.Background(), "agent", Update{Action: action, ItemID: q.Items[0].ID, Text: "replacement"})
			results <- err
		}(action)
	}
	select {
	case err := <-results:
		t.Fatalf("mutation passed active dispatch lock: %v", err)
	default:
	}
	close(release)
	<-stepped
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("mutation after claimed send error=%v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("mutation remained blocked")
		}
	}
	if len(h.sentTexts()) != 1 {
		t.Fatal("concurrent ticks duplicated delivery")
	}
}

func TestAddRequestIDIsIdempotentUnderConcurrency(t *testing.T) {
	h := newFollowupHarness(t, &testRepository{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.service.Update(context.Background(), "agent", Update{Action: "add", RequestID: "request", Text: "once"})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	q := h.queue()
	if len(q.Items) != 1 || q.Items[0].RequestID != "request" {
		t.Fatalf("duplicate request items=%+v", q.Items)
	}
	if _, err := h.service.Update(context.Background(), "agent", Update{Action: "add", RequestID: "request", Text: "different"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused request ID error=%v", err)
	}
	q.Items[0].Text = "mutated copy"
	if h.queue().Items[0].Text != "once" {
		t.Fatal("Get returned mutable service-owned items")
	}
}

func TestChangedOwnershipOrStoppedAgentNeverDispatches(t *testing.T) {
	for _, change := range []string{"agent", "pane", "ownership", "stopped", "observe-error"} {
		t.Run(change, func(t *testing.T) {
			h := newFollowupHarness(t, nil)
			h.add("reserved")
			h.observe(func(o *Observation, _ time.Time) {
				switch change {
				case "agent":
					o.AgentID = "replacement"
				case "pane":
					o.PaneID = "replacement"
				case "ownership":
					o.OwnershipToken = "replacement"
				case "stopped":
					o.Running = false
				}
			})
			if change == "observe-error" {
				h.observeErr = errors.New("agent not found")
			}
			h.step()
			h.step()
			if len(h.sentTexts()) != 0 || h.queue().Items[0].State != Queued {
				t.Fatal("unavailable or changed target received instruction")
			}
		})
	}
}

func TestCommitFailuresNeverPermitUnsafeRetry(t *testing.T) {
	for _, phase := range []string{Sending, AwaitingWork} {
		t.Run(phase, func(t *testing.T) {
			repo := &testRepository{}
			h := newFollowupHarness(t, repo)
			h.add("reserved")
			failed := false
			repo.onSave(func(q Queue) error {
				if q.Phase == phase && !failed {
					failed = true
					return errors.New("commit result unknown")
				}
				return nil
			})
			h.step()
			q := h.queue()
			wantSends := 0
			if phase == AwaitingWork {
				wantSends = 1
			}
			if !failed || q.Phase != NeedsAttention || !q.Paused || len(h.sentTexts()) != wantSends {
				t.Fatalf("failure %s: sent=%v, queue=%+v", phase, h.sentTexts(), q)
			}
			h.step()
			h.step()
			if len(h.sentTexts()) != wantSends || repo.saved("agent").Phase != NeedsAttention {
				t.Fatal("failed persistence permitted retry or did not retain attention state")
			}
		})
	}
}

func TestChangedObservationAfterCommitDefersInput(t *testing.T) {
	for _, change := range []string{"agent", "pane", "ownership", "stopped", "not-ready", "epoch", "revision", "observe-error"} {
		t.Run(change, func(t *testing.T) {
			repo := &testRepository{}
			h := newFollowupHarness(t, repo)
			h.add("reserved")
			repo.onSave(func(q Queue) error {
				if q.Phase != Sending {
					return nil
				}
				h.observe(func(o *Observation, _ time.Time) {
					switch change {
					case "agent":
						o.AgentID = "replacement"
					case "pane":
						o.PaneID = "replacement"
					case "ownership":
						o.OwnershipToken = "replacement"
					case "stopped":
						o.Running = false
					case "not-ready":
						o.Ready = false
					case "epoch":
						o.Epoch++
					case "revision":
						o.WorkingRevision++
					}
				})
				if change == "observe-error" {
					h.mu.Lock()
					h.observeErr = errors.New("snapshot unavailable")
					h.mu.Unlock()
				}
				return nil
			})
			h.step()
			q := h.queue()
			if len(h.sentTexts()) != 0 || q.Items[0].State != Queued || q.ActiveItemID != "" {
				t.Fatalf("post-commit change %s: sent=%v, queue=%+v", change, h.sentTexts(), q)
			}
		})
	}
}

func TestWorkStartTimeoutAndMonitorEpochChangeRequireAttention(t *testing.T) {
	for _, cause := range []string{"timeout", "epoch"} {
		t.Run(cause, func(t *testing.T) {
			h := newFollowupHarness(t, nil)
			h.add("reserved")
			h.step()
			h.mu.Lock()
			if cause == "timeout" {
				h.now = h.now.Add(31 * time.Second)
			} else {
				h.observation.Epoch++
			}
			h.mu.Unlock()
			h.step()
			h.step()
			if q := h.queue(); q.Phase != NeedsAttention || !q.Paused || len(h.sentTexts()) != 1 {
				t.Fatalf("%s queue=%+v, sent=%v", cause, q, h.sentTexts())
			}
		})
	}
}
