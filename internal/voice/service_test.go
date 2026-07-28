package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceFormatsMessage(t *testing.T) {
	tests := []struct {
		name string
		n    Notification
		want string
	}{
		{
			name: "without summary",
			n:    Notification{Prefix: "  ticket-manager  ", TicketID: 42},
			want: "ticket-manager 42 완료",
		},
		{
			name: "normalizes control characters and whitespace",
			n:    Notification{Prefix: "ticket-manager", TicketID: 42, Summary: "  로그인\t재시도\x00 로직\n개선  "},
			want: "ticket-manager 42 완료. 로그인 재시도 로직 개선",
		},
		{
			name: "truncates summary to 120 runes",
			n:    Notification{Prefix: "ticket-manager", TicketID: 42, Summary: strings.Repeat("가", MaxSpokenSummaryRunes+1)},
			want: "ticket-manager 42 완료. " + strings.Repeat("가", MaxSpokenSummaryRunes),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMessage(tt.n); got != tt.want {
				t.Fatalf("formatMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServicePreservesFIFOAndSerializesSpeech(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	service := newTestService(t, Options{Speak: func(ctx context.Context, message string) error {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		defer active.Add(-1)
		started <- message
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})

	for _, id := range []string{"one", "two", "three"} {
		if status, err := service.Enqueue(Notification{RequestID: id, Prefix: "ticket", TicketID: int64(len(id))}); err != nil || status != EnqueueStatusQueued {
			t.Fatalf("Enqueue(%q) = (%q, %v), want (queued, nil)", id, status, err)
		}
	}

	for _, want := range []string{"ticket 3 완료", "ticket 3 완료", "ticket 5 완료"} {
		wantSpeech(t, started, want)
		release <- struct{}{}
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent speech = %d, want 1", got)
	}
}

func TestServiceDeduplicatesQueuedInflightAndRecentIDs(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	service := newTestService(t, Options{Speak: func(ctx context.Context, message string) error {
		started <- message
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})

	first := Notification{RequestID: "first", Prefix: "ticket", TicketID: 1}
	second := Notification{RequestID: "second", Prefix: "ticket", TicketID: 2}
	mustEnqueue(t, service, first)
	wantSpeech(t, started, "ticket 1 완료")
	mustDuplicate(t, service, first)
	mustEnqueue(t, service, second)
	mustDuplicate(t, service, second)

	release <- struct{}{}
	wantSpeech(t, started, "ticket 2 완료")
	release <- struct{}{}
	mustDuplicate(t, service, first)
	mustDuplicate(t, service, second)
}

func TestServiceRejectsFullQueueWithoutRememberingRejectedID(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	service := newTestService(t, Options{Capacity: 1, Speak: func(ctx context.Context, message string) error {
		started <- message
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})

	mustEnqueue(t, service, Notification{RequestID: "active", Prefix: "ticket", TicketID: 1})
	wantSpeech(t, started, "ticket 1 완료")
	mustEnqueue(t, service, Notification{RequestID: "pending", Prefix: "ticket", TicketID: 2})
	if status, err := service.Enqueue(Notification{RequestID: "retry", Prefix: "ticket", TicketID: 3}); status != "" || !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue Enqueue() = (%q, %v), want (empty, %v)", status, err, ErrQueueFull)
	}

	release <- struct{}{}
	wantSpeech(t, started, "ticket 2 완료")
	release <- struct{}{}
	mustEnqueue(t, service, Notification{RequestID: "retry", Prefix: "ticket", TicketID: 3})
	wantSpeech(t, started, "ticket 3 완료")
	release <- struct{}{}
}

func TestServiceEvictsOldestRecentID(t *testing.T) {
	service := newTestService(t, Options{Capacity: DefaultRecentLimit + 2, Speak: func(context.Context, string) error { return nil }})
	for i := 0; i <= DefaultRecentLimit; i++ {
		mustEnqueue(t, service, Notification{RequestID: fmt.Sprintf("request-%d", i), Prefix: "ticket", TicketID: int64(i)})
	}
	if status, err := service.Enqueue(Notification{RequestID: "request-0", Prefix: "ticket", TicketID: 0}); err != nil || status != EnqueueStatusQueued {
		t.Fatalf("evicted ID Enqueue() = (%q, %v), want (queued, nil)", status, err)
	}
}

func TestServiceContinuesAfterSpeechFailure(t *testing.T) {
	var log bytes.Buffer
	started := make(chan string, 2)
	service := newTestService(t, Options{
		Log: &log,
		Speak: func(_ context.Context, message string) error {
			started <- message
			if strings.Contains(message, "1 완료") {
				return errors.New("speaker broke")
			}
			return nil
		},
	})
	mustEnqueue(t, service, Notification{RequestID: "bad", Prefix: "ticket", TicketID: 1})
	mustEnqueue(t, service, Notification{RequestID: "next", Prefix: "ticket", TicketID: 2})
	wantSpeech(t, started, "ticket 1 완료")
	wantSpeech(t, started, "ticket 2 완료")
	if got := log.String(); !strings.Contains(got, "speaker broke") {
		t.Fatalf("log = %q, want speaker error", got)
	}
}

func TestServiceCloseCancelsActiveAndDiscardsPending(t *testing.T) {
	started := make(chan string, 2)
	cancelled := make(chan struct{})
	service := newTestService(t, Options{Speak: func(ctx context.Context, message string) error {
		started <- message
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}})
	mustEnqueue(t, service, Notification{RequestID: "active", Prefix: "ticket", TicketID: 1})
	wantSpeech(t, started, "ticket 1 완료")
	mustEnqueue(t, service, Notification{RequestID: "pending", Prefix: "ticket", TicketID: 2})
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("Close returned before active speaker observed cancellation")
	}
	select {
	case message := <-started:
		t.Fatalf("pending message %q was spoken", message)
	default:
	}
	if status, err := service.Enqueue(Notification{RequestID: "late"}); status != "" || !errors.Is(err, ErrClosed) {
		t.Fatalf("Enqueue after Close = (%q, %v), want (empty, %v)", status, err, ErrClosed)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceConcurrentEnqueueAndClose(t *testing.T) {
	service := newTestService(t, Options{Speak: func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	const count = 128
	start := make(chan struct{})
	results := make(chan error, count)
	var enqueues sync.WaitGroup
	for i := 0; i < count; i++ {
		enqueues.Add(1)
		go func(i int) {
			defer enqueues.Done()
			<-start
			_, err := service.Enqueue(Notification{RequestID: fmt.Sprintf("request-%d", i)})
			results <- err
		}(i)
	}
	closed := make(chan error, 1)
	go func() {
		<-start
		closed <- service.Close()
	}()
	close(start)

	done := make(chan struct{})
	go func() { enqueues.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Enqueue calls hung during Close")
	}
	for i := 0; i < count; i++ {
		if err := <-results; err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("concurrent Enqueue error = %v", err)
		}
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung while racing with Enqueue")
	}
}

func newTestService(t *testing.T, options Options) *Service {
	t.Helper()
	if options.Log == nil {
		options.Log = io.Discard
	}
	service, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func mustEnqueue(t *testing.T, service *Service, notification Notification) {
	t.Helper()
	if status, err := service.Enqueue(notification); err != nil || status != EnqueueStatusQueued {
		t.Fatalf("Enqueue(%q) = (%q, %v), want (queued, nil)", notification.RequestID, status, err)
	}
}

func mustDuplicate(t *testing.T, service *Service, notification Notification) {
	t.Helper()
	if status, err := service.Enqueue(notification); err != nil || status != EnqueueStatusDuplicate {
		t.Fatalf("duplicate Enqueue(%q) = (%q, %v), want (duplicate, nil)", notification.RequestID, status, err)
	}
}

func wantSpeech(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("spoken message = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}
