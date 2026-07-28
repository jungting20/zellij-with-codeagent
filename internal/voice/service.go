package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

const (
	DefaultCapacity       = 256
	DefaultRecentLimit    = 1024
	MaxSpokenSummaryRunes = 120
)

var (
	ErrQueueFull = errors.New("voice notification queue is full")
	ErrClosed    = errors.New("voice notification service is closed")
)

type Notification struct {
	RequestID string
	Prefix    string
	TicketID  int64
	Summary   string
}

type EnqueueStatus string

const (
	EnqueueStatusQueued    EnqueueStatus = "queued"
	EnqueueStatusDuplicate EnqueueStatus = "duplicate"
)

type Options struct {
	Capacity    int
	RecentLimit int
	Speak       func(context.Context, string) error
	Log         io.Writer
}

type Service struct {
	mu          sync.Mutex
	cond        *sync.Cond
	queue       []Notification
	active      map[string]struct{}
	recent      map[string]struct{}
	recentOrder []string
	closed      bool

	capacity    int
	recentLimit int
	speak       func(context.Context, string) error
	log         io.Writer
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewService(options Options) (*Service, error) {
	if options.Speak == nil {
		return nil, errors.New("voice notification speaker is required")
	}
	if options.Capacity < 0 {
		return nil, errors.New("voice notification capacity must not be negative")
	}
	if options.Capacity > DefaultCapacity {
		return nil, fmt.Errorf("voice notification capacity must not exceed %d", DefaultCapacity)
	}
	if options.RecentLimit < 0 {
		return nil, errors.New("voice notification recent limit must not be negative")
	}
	if options.RecentLimit > DefaultRecentLimit {
		return nil, fmt.Errorf("voice notification recent limit must not exceed %d", DefaultRecentLimit)
	}
	if options.Capacity == 0 {
		options.Capacity = DefaultCapacity
	}
	if options.RecentLimit == 0 {
		options.RecentLimit = DefaultRecentLimit
	}
	if options.Log == nil {
		options.Log = io.Discard
	}

	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		capacity:    options.Capacity,
		recentLimit: options.RecentLimit,
		speak:       options.Speak,
		log:         options.Log,
		active:      make(map[string]struct{}),
		recent:      make(map[string]struct{}),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	service.cond = sync.NewCond(&service.mu)
	go service.run()
	return service, nil
}

func NewNativeService(log io.Writer) *Service {
	backend, err := resolveSpeechBackend(runtime.GOOS, exec.LookPath)
	if err != nil {
		service, serviceErr := NewService(Options{
			Log: log,
			Speak: func(context.Context, string) error {
				return err
			},
		})
		if serviceErr != nil {
			panic(serviceErr)
		}
		return service
	}
	service, serviceErr := NewService(Options{Speak: backend.speak, Log: log})
	if serviceErr != nil {
		panic(serviceErr)
	}
	return service
}

func (s *Service) Enqueue(notification Notification) (EnqueueStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	if _, ok := s.active[notification.RequestID]; ok {
		return EnqueueStatusDuplicate, nil
	}
	if _, ok := s.recent[notification.RequestID]; ok {
		return EnqueueStatusDuplicate, nil
	}
	if len(s.queue) >= s.capacity {
		return "", ErrQueueFull
	}

	s.queue = append(s.queue, notification)
	s.active[notification.RequestID] = struct{}{}
	s.cond.Signal()
	return EnqueueStatusQueued, nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		clear(s.queue)
		s.queue = nil
		clear(s.active)
		s.cancel()
		s.cond.Broadcast()
	}
	done := s.done
	s.mu.Unlock()

	<-done
	return nil
}

func (s *Service) run() {
	defer close(s.done)
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.cond.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		notification := s.queue[0]
		s.queue[0] = Notification{}
		s.queue = s.queue[1:]
		s.mu.Unlock()

		if err := s.speak(s.ctx, formatMessage(notification)); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(s.log, "voice notification failed: %v\n", err)
		}
		s.finish(notification.RequestID)
	}
}

func (s *Service) finish(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, requestID)
	if s.closed {
		return
	}
	s.recent[requestID] = struct{}{}
	s.recentOrder = append(s.recentOrder, requestID)
	if len(s.recentOrder) > s.recentLimit {
		oldest := s.recentOrder[0]
		s.recentOrder[0] = ""
		s.recentOrder = s.recentOrder[1:]
		delete(s.recent, oldest)
	}
}

func formatMessage(notification Notification) string {
	base := fmt.Sprintf("%s %d 완료", strings.TrimSpace(notification.Prefix), notification.TicketID)
	summary := normalizeSummary(notification.Summary)
	if summary == "" {
		return base
	}
	return base + ". " + summary
}

func normalizeSummary(summary string) string {
	summary = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		return r
	}, summary)
	summary = strings.Join(strings.Fields(summary), " ")
	runes := []rune(summary)
	if len(runes) > MaxSpokenSummaryRunes {
		return string(runes[:MaxSpokenSummaryRunes])
	}
	return summary
}
