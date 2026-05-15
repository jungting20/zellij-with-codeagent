package eventbus

import (
	"context"
	"sync"
)

const (
	defaultBuffer  = 64
	defaultHistory = 128
)

// Bus fans out events to subscribers without exposing Zellij wire formats.
type Bus struct {
	mu       sync.RWMutex
	closed   bool
	nextID   int
	subs     map[int]chan Event
	bufferN  int
	history  []Event
	historyN int
}

// New returns an event bus with buffered subscriber channels.
func New() *Bus {
	return NewWithBuffer(defaultBuffer)
}

// NewWithBuffer returns an event bus with the given per-subscriber buffer size.
func NewWithBuffer(buffer int) *Bus {
	if buffer < 1 {
		buffer = 1
	}
	return &Bus{
		subs:     make(map[int]chan Event),
		bufferN:  buffer,
		historyN: defaultHistory,
	}
}

// Subscribe registers a subscriber. Events are delivered until ctx is done or
// the bus is closed. The returned channel is closed when the subscription ends.
func (b *Bus) Subscribe(ctx context.Context) (<-chan Event, func()) {
	ch := make(chan Event, b.bufferN)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()

	cancelOnce := sync.Once{}
	unregister := func() {
		cancelOnce.Do(func() {
			b.mu.Lock()
			if c, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(c)
			}
			b.mu.Unlock()
		})
	}

	go func() {
		<-ctx.Done()
		unregister()
	}()

	return ch, unregister
}

// Publish delivers an event to all subscribers. Slow subscribers drop events
// when their buffer is full so publishers cannot deadlock.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.history = append(b.history, e)
	if overflow := len(b.history) - b.historyN; overflow > 0 {
		copy(b.history, b.history[overflow:])
		b.history = b.history[:b.historyN]
	}
	subs := make([]chan Event, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// drop — subscriber is slower than publisher
		}
	}
}

// Recent returns recent events in publication order. A non-positive limit
// returns the full retained history.
func (b *Bus) Recent(limit int) []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	start := 0
	if limit > 0 && limit < len(b.history) {
		start = len(b.history) - limit
	}

	events := make([]Event, len(b.history)-start)
	copy(events, b.history[start:])
	return events
}

// Close shuts down the bus and closes all subscriber channels.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		close(ch)
		delete(b.subs, id)
	}
}
