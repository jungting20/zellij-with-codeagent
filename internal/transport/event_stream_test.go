package transport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"zellij-with-codeagent/internal/eventbus"
)

func TestClientStreamsLargeEventAndFollowingEvent(t *testing.T) {
	service := newFakeRuntimeService()
	client, cleanup := startUnixTransport(t, service)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, message := range []string{strings.Repeat("x", 70*1024), "after large event"} {
		service.publish(eventbus.Event{Type: eventbus.TypeRawOutput, Message: message})
		select {
		case got, ok := <-stream.Events:
			if !ok || got.Message != message {
				t.Fatalf("event open=%v, length=%d, want %d bytes", ok, len(got.Message), len(message))
			}
		case err := <-stream.Errors:
			t.Fatalf("stream error: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

type eventStreamTestTransport struct{ body io.ReadCloser }

func (r eventStreamTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: r.body}, nil
}

func TestClientStreamCloseStopsBlockedProducer(t *testing.T) {
	for _, blockedOn := range []string{"delivery", "body read"} {
		t.Run(blockedOn, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var body io.ReadCloser = io.NopCloser(strings.NewReader("{\"type\":\"raw_output\",\"message\":\"pending\"}\n"))
				if blockedOn == "body read" {
					reader, writer := io.Pipe()
					defer writer.Close()
					body = reader
				}
				client := NewClient(ClientOptions{})
				client.http.Transport = eventStreamTestTransport{body: body}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stream, err := client.StreamEvents(ctx)
				if err != nil {
					t.Fatal(err)
				}
				// No event consumer: the producer must stop even when delivery blocks.
				synctest.Wait()
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				select {
				case _, ok := <-stream.Events:
					if ok {
						t.Error("producer was still waiting to deliver an event after Close")
					}
				default:
					t.Error("event channel did not close")
				}
			})
		})
	}
}
