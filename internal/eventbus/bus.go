package eventbus

import (
	"context"
	"sync"
)

const defaultBuffer = 64

// Bus fans out events to subscribers without exposing Zellij wire formats.
type Bus struct {
	mu      sync.RWMutex
	closed  bool
	nextID  int
	subs    map[int]chan Event
	bufferN int
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
		subs:    make(map[int]chan Event),
		bufferN: buffer,
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
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	subs := make([]chan Event, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// drop — subscriber is slower than publisher
		}
	}
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
