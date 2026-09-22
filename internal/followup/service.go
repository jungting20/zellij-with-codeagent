package followup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type entry struct {
	mu       sync.Mutex
	queue    Queue
	dirty    bool
	snapshot atomic.Pointer[Queue]
}

func newEntry(q Queue) *entry {
	e := &entry{queue: clone(q)}
	e.publish()
	return e
}

// publish runs under the entry lock; readers never wait for external I/O.
func (e *entry) publish() {
	q := clone(e.queue)
	e.snapshot.Store(&q)
}

type Service struct {
	opts   Options
	mu     sync.Mutex
	queues map[string]*entry
}

func New(opts Options) (*Service, error) {
	if opts.Observe == nil || opts.Send == nil {
		return nil, fmt.Errorf("follow-up runtime callbacks are required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewID == nil {
		opts.NewID = uuid.NewString
	}
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = 30 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	s := &Service{opts: opts, queues: make(map[string]*entry)}
	if opts.Repository == nil {
		return s, nil
	}
	queues, err := opts.Repository.Load()
	if err != nil {
		return nil, err
	}
	for _, q := range queues {
		e := newEntry(q)
		if q.ActiveItemID != "" || q.Phase == AwaitingWork || q.Phase == Working || q.Phase == Sending {
			s.attention(e, "데몬이 재시작되어 이전 지시의 실행 상태를 확인해야 합니다")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = s.save(ctx, e)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("recover follow-up queue: %w", err)
			}
		}
		s.queues[q.AgentID] = e
	}
	return s, nil
}

func clone(q Queue) Queue {
	q.Items = append([]Item{}, q.Items...)
	return q
}

func (s *Service) find(id string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queues[id]
}

func (s *Service) Get(ctx context.Context, id string) (Queue, error) {
	if e := s.find(id); e != nil {
		return clone(*e.snapshot.Load()), nil
	}
	o, err := s.opts.Observe(ctx, id)
	if err != nil {
		return Queue{}, err
	}
	return Queue{AgentID: id, PaneID: o.PaneID, OwnershipToken: o.OwnershipToken, Phase: Ready, Items: []Item{}}, nil
}

// Summary never performs runtime or database I/O and does not create a queue.
func (s *Service) Summary(id string) (count int, paused, attention bool) {
	if e := s.find(id); e != nil {
		q := e.snapshot.Load()
		return q.PendingCount(), q.Paused, q.Phase == NeedsAttention
	}
	return
}

func (s *Service) Update(ctx context.Context, id string, req Update) (Queue, error) {
	if strings.TrimSpace(id) == "" {
		return Queue{}, ErrInvalid
	}
	e := s.find(id)
	if e == nil {
		if req.Action != "add" && req.Action != "pause" && req.Action != "resume" {
			return Queue{}, ErrNotFound
		}
		o, err := s.opts.Observe(ctx, id)
		if err != nil {
			return Queue{}, err
		}
		if !o.Running || o.AgentID != id || o.PaneID == "" || o.OwnershipToken == "" {
			return Queue{}, fmt.Errorf("%w: agent is not running", ErrConflict)
		}
		s.mu.Lock()
		e = s.queues[id]
		if e == nil {
			e = newEntry(Queue{AgentID: id, PaneID: o.PaneID, OwnershipToken: o.OwnershipToken, Phase: Ready, Items: []Item{}})
			s.queues[id] = e
		}
		s.mu.Unlock()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publish()
	q := &e.queue
	index := -1
	for i := range q.Items {
		if q.Items[i].ID == req.ItemID {
			index = i
			break
		}
	}
	switch req.Action {
	case "add":
		if req.RequestID != "" {
			for _, item := range q.Items {
				if item.RequestID == req.RequestID {
					if item.Text != req.Text {
						return Queue{}, fmt.Errorf("%w: request ID already used", ErrConflict)
					}
					if e.dirty {
						if err := s.save(ctx, e); err != nil {
							return Queue{}, err
						}
					}
					return clone(*q), nil
				}
			}
		}
		if err := validText(req.Text); err != nil {
			return Queue{}, err
		}
		if q.PendingCount() >= 100 {
			return Queue{}, fmt.Errorf("%w: maximum 100 pending instructions", ErrInvalid)
		}
		q.Items = append(q.Items, Item{ID: s.opts.NewID(), RequestID: req.RequestID, Text: req.Text, State: Queued, CreatedAt: s.opts.Now()})
	case "edit", "cancel", "resolve":
		if index < 0 {
			return Queue{}, ErrNotFound
		}
		item := &q.Items[index]
		if req.Action == "resolve" {
			if item.State != NeedsAttention || q.ActiveItemID != item.ID {
				return Queue{}, fmt.Errorf("%w: no delivery to confirm", ErrConflict)
			}
			item.State, item.Error = Sent, ""
			q.ActiveItemID, q.Phase, q.Reason, q.Paused = "", Ready, "전달 확인됨 · 자동 전달을 재개하세요", true
		} else if req.Action == "cancel" && item.State == NeedsAttention && q.ActiveItemID == item.ID {
			item.State, item.Error = Canceled, ""
			q.ActiveItemID, q.Phase, q.Reason, q.Paused = "", Ready, "확인 필요 항목 제외됨 · 자동 전달을 재개하세요", true
		} else {
			if item.State != Queued {
				return Queue{}, fmt.Errorf("%w: only queued instructions can be changed", ErrConflict)
			}
			if req.Action == "edit" {
				if err := validText(req.Text); err != nil {
					return Queue{}, err
				}
				item.Text = req.Text
			} else {
				item.State = Canceled
			}
		}
	case "pause":
		q.Paused = true
	case "resume":
		if q.Phase == NeedsAttention {
			return Queue{}, fmt.Errorf("%w: confirm or exclude the uncertain delivery first", ErrConflict)
		}
		q.Paused, q.Reason = false, ""
	default:
		return Queue{}, ErrInvalid
	}
	if err := s.save(ctx, e); err != nil {
		return Queue{}, err
	}
	return clone(*q), nil
}

func validText(text string) error {
	if strings.TrimSpace(text) == "" || len(text) > 64*1024 {
		return fmt.Errorf("%w: instruction must contain 1–65536 bytes", ErrInvalid)
	}
	return nil
}

func (s *Service) save(ctx context.Context, e *entry) error {
	e.queue.UpdatedAt = s.opts.Now()
	e.dirty = true
	e.publish()
	if s.opts.Repository != nil {
		if err := s.opts.Repository.Save(ctx, clone(e.queue)); err != nil {
			return err
		}
	}
	e.dirty = false
	return nil
}

func (s *Service) attention(e *entry, reason string) {
	e.queue.Phase, e.queue.Paused, e.queue.Reason = NeedsAttention, true, reason
	for i := range e.queue.Items {
		if e.queue.Items[i].ID == e.queue.ActiveItemID {
			e.queue.Items[i].State, e.queue.Items[i].Error = NeedsAttention, reason
		}
	}
}

// Step checks each agent independently. No event is trusted as a delivery receipt.
// Per-queue locks serialize edits, cancellation and dispatch, including claims.
func (s *Service) Step(ctx context.Context) {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.queues))
	for _, e := range s.queues {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(e *entry) { defer wg.Done(); s.step(ctx, e) }(e)
	}
	wg.Wait()
}

func (s *Service) step(parent context.Context, e *entry) {
	// Never overlap scheduled scans of the same queue or wait for UI writes.
	if !e.mu.TryLock() {
		return
	}
	defer e.mu.Unlock()
	defer e.publish()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	q := &e.queue
	if e.dirty {
		if err := s.save(ctx, e); err != nil {
			return
		}
	}
	if q.Phase == NeedsAttention || (q.PendingCount() == 0 && q.ActiveItemID == "") {
		return
	}
	o, err := s.opts.Observe(ctx, q.AgentID)
	if err != nil || !o.Running || o.PaneID != q.PaneID || o.OwnershipToken != q.OwnershipToken || o.AgentID != q.AgentID {
		q.Reason = "대상 에이전트의 실행 상태를 확인할 수 없습니다"
		if q.ActiveItemID != "" {
			s.attention(e, q.Reason)
			_ = s.save(ctx, e)
		}
		return
	}
	if q.ActiveItemID != "" {
		if o.Epoch != q.ObservationEpoch {
			s.attention(e, "에이전트 상태 감지가 재시작되어 전달 확인이 필요합니다")
			_ = s.save(ctx, e)
			return
		}
		if o.WorkingRevision > q.BaselineRevision && !o.LastWorkingAt.Before(q.AttemptedAt) {
			if q.Phase != Working {
				q.Phase = Working
				if err := s.save(ctx, e); err != nil {
					return
				}
			}
		}
		if q.Phase == AwaitingWork && s.opts.Now().Sub(q.AttemptedAt) >= s.opts.StartTimeout {
			s.attention(e, "지시 전달 후 작업 시작을 확인하지 못했습니다")
			_ = s.save(ctx, e)
			return
		}
		if q.Phase != Working || !o.Ready || !o.ObservedAt.After(o.LastWorkingAt) {
			return
		}
		q.ActiveItemID, q.Phase, q.Reason = "", Ready, ""
		if err := s.save(ctx, e); err != nil {
			return
		}
	}
	if q.Paused {
		return
	}
	if !o.Ready {
		q.Reason = "명확한 입력 대기 상태를 기다리는 중"
		if o.Reason != "" {
			q.Reason = o.Reason
		}
		return
	}
	index := -1
	for i := range q.Items {
		if q.Items[i].State == Queued {
			index = i
			break
		}
	}
	if index < 0 {
		q.Reason = ""
		return
	}
	q.ActiveItemID, q.Phase = q.Items[index].ID, Sending
	q.Items[index].State = Sending
	q.AttemptedAt, q.BaselineRevision, q.ObservationEpoch = s.opts.Now(), o.WorkingRevision, o.Epoch
	q.Reason = "전달 중"
	// Persist a delivery claim before any external input. A failed commit is never
	// permission to send, even if the asynchronous write eventually succeeds.
	if err := s.save(ctx, e); err != nil {
		s.attention(e, "전송 준비 저장을 확인하지 못했습니다: "+err.Error())
		e.dirty = true
		return
	}
	// Recheck after the durable commit, which may have taken arbitrarily long.
	fresh, err := s.opts.Observe(ctx, q.AgentID)
	if err != nil || !fresh.Ready || !fresh.Running || fresh.AgentID != q.AgentID || fresh.PaneID != q.PaneID || fresh.OwnershipToken != q.OwnershipToken || fresh.Epoch != o.Epoch || fresh.WorkingRevision != o.WorkingRevision {
		q.Items[index].State, q.ActiveItemID, q.Phase, q.Reason = Queued, "", Ready, "입력 상태가 변경되어 전달을 보류했습니다"
		_ = s.save(ctx, e)
		return
	}
	q.AttemptedAt = s.opts.Now()
	if err := s.opts.Send(ctx, fresh, q.Items[index].Text); err != nil {
		if errors.Is(err, ErrNotReady) {
			q.Items[index].State, q.ActiveItemID, q.Phase, q.Reason = Queued, "", Ready, "입력 상태가 변경되어 전달을 보류했습니다"
			_ = s.save(ctx, e)
			return
		}
		s.attention(e, "전송 결과 확인 필요: "+err.Error())
		_ = s.save(ctx, e)
		return
	}
	q.Items[index].State, q.Items[index].SentAt = Sent, s.opts.Now()
	q.Phase, q.Reason = AwaitingWork, "전달됨 · 작업 시작 확인 중"
	if err := s.save(ctx, e); err != nil {
		s.attention(e, "전달 결과 저장 확인 필요: "+err.Error())
		e.dirty = true
	}
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Step(ctx)
		}
	}
}
