package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVoiceNotificationQueued(t *testing.T) {
	service := &fakeVoiceNotificationService{response: VoiceNotificationResponse{Status: "queued"}}
	server := newVoiceTestServer(t, service)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/voice-notifications", strings.NewReader(validVoiceNotificationJSON(t))))

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	var response VoiceNotificationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "queued" {
		t.Fatalf("response = %#v, want queued", response)
	}
	if got := service.request(); got != (VoiceNotificationRequest{RequestID: "request-1", Prefix: "ticket-manager", TicketID: 42, Summary: "tests passed"}) {
		t.Fatalf("service request = %#v, want decoded request", got)
	}
}

func TestNewServerRequiresVoiceNotificationService(t *testing.T) {
	_, err := NewServer(ServerOptions{Service: newFakeRuntimeService(), SocketPath: "unused.sock"})
	if err == nil || err.Error() != "transport: voice notification service is required" {
		t.Fatalf("NewServer() error = %v, want voice notification service requirement", err)
	}
}

func TestVoiceNotificationDuplicate(t *testing.T) {
	service := &fakeVoiceNotificationService{response: VoiceNotificationResponse{Status: "duplicate"}}
	server := newVoiceTestServer(t, service)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/voice-notifications", strings.NewReader(validVoiceNotificationJSON(t))))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestVoiceNotificationRejectsInvalidFieldsAtExactLimits(t *testing.T) {
	validRequestID := strings.Repeat("r", 256)
	validPrefix := strings.Repeat("가", 128)
	validSummary := strings.Repeat("s", 4<<10)
	cases := []struct {
		name string
		req  VoiceNotificationRequest
		want int
	}{
		{name: "maximum valid lengths", req: VoiceNotificationRequest{RequestID: validRequestID, Prefix: validPrefix, TicketID: 1, Summary: validSummary}, want: http.StatusAccepted},
		{name: "missing request id", req: VoiceNotificationRequest{Prefix: "prefix", TicketID: 1}, want: http.StatusBadRequest},
		{name: "request id over limit", req: VoiceNotificationRequest{RequestID: validRequestID + "r", Prefix: "prefix", TicketID: 1}, want: http.StatusBadRequest},
		{name: "blank prefix", req: VoiceNotificationRequest{RequestID: "request-1", Prefix: " \t", TicketID: 1}, want: http.StatusBadRequest},
		{name: "prefix over rune limit", req: VoiceNotificationRequest{RequestID: "request-1", Prefix: validPrefix + "가", TicketID: 1}, want: http.StatusBadRequest},
		{name: "non-positive ticket id", req: VoiceNotificationRequest{RequestID: "request-1", Prefix: "prefix", TicketID: 0}, want: http.StatusBadRequest},
		{name: "summary over limit", req: VoiceNotificationRequest{RequestID: "request-1", Prefix: "prefix", TicketID: 1, Summary: validSummary + "s"}, want: http.StatusBadRequest},
		{name: "summary newline", req: VoiceNotificationRequest{RequestID: "request-1", Prefix: "prefix", TicketID: 1, Summary: "line one\nline two"}, want: http.StatusBadRequest},
		{name: "summary carriage return", req: VoiceNotificationRequest{RequestID: "request-1", Prefix: "prefix", TicketID: 1, Summary: "line one\rline two"}, want: http.StatusBadRequest},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeVoiceNotificationService{response: VoiceNotificationResponse{Status: "queued"}}
			server := newVoiceTestServer(t, service)
			body, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/voice-notifications", bytes.NewReader(body)))

			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.want, recorder.Body.String())
			}
			if tt.want == http.StatusBadRequest {
				assertVoiceAPIError(t, recorder, CodeBadRequest, false)
			}
		})
	}
}

func TestVoiceNotificationEnforcesExactBodyLimit(t *testing.T) {
	validBody := []byte(validVoiceNotificationJSON(t))
	if len(validBody) >= 8<<10 {
		t.Fatalf("valid body length = %d, want less than %d", len(validBody), 8<<10)
	}
	cases := []struct {
		name string
		body []byte
		want int
	}{
		{name: "eight kibibytes accepted", body: append(bytes.Repeat([]byte(" "), (8<<10)-len(validBody)), validBody...), want: http.StatusAccepted},
		{name: "eight kibibytes plus one rejected", body: append(bytes.Repeat([]byte(" "), (8<<10)+1-len(validBody)), validBody...), want: http.StatusBadRequest},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeVoiceNotificationService{response: VoiceNotificationResponse{Status: "queued"}}
			server := newVoiceTestServer(t, service)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/voice-notifications", bytes.NewReader(tt.body)))
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}

func TestVoiceNotificationRejectsWrongMethodAndTrailingJSON(t *testing.T) {
	server := newVoiceTestServer(t, &fakeVoiceNotificationService{response: VoiceNotificationResponse{Status: "queued"}})
	cases := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{name: "wrong method", method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "trailing json", method: http.MethodPost, body: validVoiceNotificationJSON(t) + `{}`, want: http.StatusBadRequest},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(tt.method, "/v1/voice-notifications", strings.NewReader(tt.body)))
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.want, recorder.Body.String())
			}
			assertVoiceAPIError(t, recorder, CodeBadRequest, false)
		})
	}
}

func TestVoiceNotificationQueueFullIsRetryable(t *testing.T) {
	service := &fakeVoiceNotificationService{err: ErrVoiceQueueFull}
	server := newVoiceTestServer(t, service)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/voice-notifications", strings.NewReader(validVoiceNotificationJSON(t))))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	assertVoiceAPIError(t, recorder, CodeQueueFull, true)
}

func TestClientQueuesVoiceNotificationOverUnixSocket(t *testing.T) {
	service := &fakeVoiceNotificationService{response: VoiceNotificationResponse{Status: "queued"}}
	client, cleanup := startUnixVoiceTransport(t, service)
	defer cleanup()

	response, err := client.QueueVoiceNotification(context.Background(), VoiceNotificationRequest{RequestID: "request-1", Prefix: "ticket-manager", TicketID: 42, Summary: "tests passed"})
	if err != nil {
		t.Fatalf("QueueVoiceNotification() error = %v", err)
	}
	if response.Status != "queued" {
		t.Fatalf("QueueVoiceNotification() = %#v, want queued", response)
	}
}

func validVoiceNotificationJSON(t *testing.T) string {
	t.Helper()
	body, err := json.Marshal(VoiceNotificationRequest{RequestID: "request-1", Prefix: "ticket-manager", TicketID: 42, Summary: "tests passed"})
	if err != nil {
		t.Fatalf("marshal valid request: %v", err)
	}
	return string(body)
}

func assertVoiceAPIError(t *testing.T, recorder *httptest.ResponseRecorder, wantCode ErrorCode, wantRetryable bool) {
	t.Helper()
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != wantCode || response.Error.Retryable != wantRetryable {
		t.Fatalf("error response = %#v, want code=%q retryable=%t", response.Error, wantCode, wantRetryable)
	}
}

type fakeVoiceNotificationService struct {
	mu       sync.Mutex
	response VoiceNotificationResponse
	err      error
	req      VoiceNotificationRequest
}

type noopVoiceNotificationService struct{}

func (noopVoiceNotificationService) QueueVoiceNotification(context.Context, VoiceNotificationRequest) (VoiceNotificationResponse, error) {
	return VoiceNotificationResponse{Status: "queued"}, nil
}

func (f *fakeVoiceNotificationService) QueueVoiceNotification(_ context.Context, req VoiceNotificationRequest) (VoiceNotificationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.req = req
	return f.response, f.err
}

func (f *fakeVoiceNotificationService) request() VoiceNotificationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.req
}

func newVoiceTestServer(t *testing.T, voiceNotifications VoiceNotificationService) *Server {
	t.Helper()
	server, err := NewServer(ServerOptions{
		Service:            newFakeRuntimeService(),
		VoiceNotifications: voiceNotifications,
		SocketPath:         "unused.sock",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func startUnixVoiceTransport(t *testing.T, voiceNotifications VoiceNotificationService) (*Client, func()) {
	t.Helper()
	socketPath := shortSocketPath(t)
	server, err := NewServer(ServerOptions{
		Service:            newFakeRuntimeService(),
		VoiceNotifications: voiceNotifications,
		SocketPath:         socketPath,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe(ctx) }()
	client := NewClient(ClientOptions{SocketPath: socketPath})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for voice transport health")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return client, func() {
		cancel()
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("ListenAndServe() error = %v", err)
		}
	}
}
