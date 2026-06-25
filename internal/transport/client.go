package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

type ClientOptions struct {
	SocketPath    string
	Timeout       time.Duration
	AutoStart     bool
	DaemonCommand []string
	StartTimeout  time.Duration
	StartLockPath string
	StartDaemon   func(context.Context, DaemonStartOptions) error
}

type DaemonStartOptions struct {
	SocketPath string
	Command    []string
}

type Client struct {
	baseURL      string
	socketPath   string
	http         *http.Client
	autoStart    bool
	startTimeout time.Duration
	startLock    string
	startCommand []string
	startDaemon  func(context.Context, DaemonStartOptions) error
}

type EventStream struct {
	Events <-chan Event
	Errors <-chan error
	Close  func() error
}

func NewClient(opts ClientOptions) *Client {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultRequestTimeout
	}
	startTimeout := opts.StartTimeout
	if startTimeout == 0 {
		startTimeout = 5 * time.Second
	}
	startLock := opts.StartLockPath
	if startLock == "" && opts.SocketPath != "" {
		startLock = opts.SocketPath + ".start.lock"
	}
	startDaemon := opts.StartDaemon
	if startDaemon == nil {
		startDaemon = startDaemonProcess
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", opts.SocketPath)
		},
	}
	return &Client{
		baseURL:      "http://agentd",
		socketPath:   opts.SocketPath,
		autoStart:    opts.AutoStart,
		startTimeout: startTimeout,
		startLock:    startLock,
		startCommand: append([]string(nil), opts.DaemonCommand...),
		startDaemon:  startDaemon,
		http: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}
}

func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var response HealthResponse
	err := c.do(ctx, http.MethodGet, "/v1/health", nil, &response)
	return response, err
}

func (c *Client) CreatePane(ctx context.Context, req CreatePaneRequest) (CreatePaneResponse, error) {
	var response CreatePaneResponse
	err := c.do(ctx, http.MethodPost, "/v1/panes", req, &response)
	return response, err
}

func (c *Client) SendInput(ctx context.Context, paneID string, req SendInputRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/panes/"+url.PathEscape(paneID)+"/input", req, nil)
}

func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (SendMessageResponse, error) {
	var response SendMessageResponse
	err := c.do(ctx, http.MethodPost, "/v1/messages", req, &response)
	return response, err
}

func (c *Client) SnapshotOutput(ctx context.Context, paneID string, req SnapshotOutputRequest) (SnapshotOutputResponse, error) {
	var response SnapshotOutputResponse
	err := c.do(ctx, http.MethodPost, "/v1/panes/"+url.PathEscape(paneID)+"/snapshot", req, &response)
	return response, err
}

func (c *Client) ListPanes(ctx context.Context) (ListPanesResponse, error) {
	var response ListPanesResponse
	err := c.do(ctx, http.MethodGet, "/v1/panes", nil, &response)
	return response, err
}

func (c *Client) InspectRuntime(ctx context.Context) (InspectRuntimeResponse, error) {
	var response InspectRuntimeResponse
	err := c.do(ctx, http.MethodGet, "/v1/runtime", nil, &response)
	return response, err
}

func (c *Client) RecentEvents(ctx context.Context, limit int, types ...string) (RecentEventsResponse, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	for _, eventType := range types {
		query.Add("type", eventType)
	}
	path := "/v1/events/recent"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response RecentEventsResponse
	err := c.do(ctx, http.MethodGet, path, nil, &response)
	return response, err
}

func (c *Client) StreamEvents(ctx context.Context) (*EventStream, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/events/stream", nil)
	if err != nil {
		return nil, err
	}
	streamHTTP := *c.http
	streamHTTP.Timeout = 0
	response, err := streamHTTP.Do(req)
	if err != nil {
		if !c.shouldAutoStart(err) {
			return nil, err
		}
		if startErr := c.ensureDaemon(ctx); startErr != nil {
			return nil, fmt.Errorf("auto-start daemon: %w", startErr)
		}
		response, err = streamHTTP.Do(req)
		if err != nil {
			return nil, err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, decodeClientError(response)
	}

	events := make(chan Event)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		defer response.Body.Close()

		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			var event Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				errs <- err
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- err
		}
	}()

	return &EventStream{
		Events: events,
		Errors: errs,
		Close:  response.Body.Close,
	}, nil
}

func (c *Client) Reconcile(ctx context.Context) (ReconcileResponse, error) {
	var response ReconcileResponse
	err := c.do(ctx, http.MethodPost, "/v1/reconcile", map[string]bool{}, &response)
	return response, err
}

func (c *Client) Cleanup(ctx context.Context, req CleanupRequest) (CleanupResponse, error) {
	var response CleanupResponse
	err := c.do(ctx, http.MethodPost, "/v1/cleanup", req, &response)
	return response, err
}

func (c *Client) SubmitExecutionPlan(ctx context.Context, requestID string, payload ExecutionPlanPayload) (ExecutionPlanResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ExecutionPlanResponse{}, err
	}
	var response ExecutionPlanResponse
	err = c.do(ctx, http.MethodPost, "/v1/requests", RequestEnvelope{
		Type:      RequestTypeExecutionPlan,
		RequestID: requestID,
		Payload:   raw,
	}, &response)
	return response, err
}

func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	response, err := c.doHTTP(ctx, method, path, payload, body != nil)
	if err != nil {
		if !c.shouldAutoStart(err) {
			return err
		}
		if startErr := c.ensureDaemon(ctx); startErr != nil {
			return fmt.Errorf("auto-start daemon: %w", startErr)
		}
		response, err = c.doHTTP(ctx, method, path, payload, body != nil)
		if err != nil {
			return err
		}
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeClientError(response)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (c *Client) doHTTP(ctx context.Context, method, path string, payload []byte, hasBody bool) (*http.Response, error) {
	var reader io.Reader
	if hasBody {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

func decodeClientError(response *http.Response) error {
	var errorResponse ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
		return fmt.Errorf("agentd transport http %d: decode error response: %w", response.StatusCode, err)
	}
	return &ClientError{StatusCode: response.StatusCode, APIError: errorResponse.Error}
}

func (c *Client) shouldAutoStart(err error) bool {
	return c.autoStart && c.socketPath != "" && isDaemonUnavailableError(err)
}

func (c *Client) ensureDaemon(ctx context.Context) error {
	if c.startLock == "" {
		return c.startDaemonAndWait(ctx)
	}
	lockFile, err := os.OpenFile(c.startLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open start lock %s: %w", c.startLock, err)
	}
	defer lockFile.Close()
	if err := flockContext(ctx, lockFile); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	if c.healthReady(ctx) {
		return nil
	}
	return c.startDaemonAndWait(ctx)
}

func (c *Client) startDaemonAndWait(ctx context.Context) error {
	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()

	command := c.startCommand
	if len(command) == 0 {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve current executable: %w", err)
		}
		command = []string{executable, "daemon", "serve", "--socket", c.socketPath}
	}
	if err := c.startDaemon(startCtx, DaemonStartOptions{
		SocketPath: c.socketPath,
		Command:    append([]string(nil), command...),
	}); err != nil {
		return err
	}

	deadline := time.NewTimer(c.startTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c.healthReady(startCtx) {
			return nil
		}
		select {
		case <-startCtx.Done():
			return fmt.Errorf("daemon did not become ready on %s: %w", c.socketPath, startCtx.Err())
		case <-deadline.C:
			return fmt.Errorf("daemon did not become ready on %s within %s", c.socketPath, c.startTimeout)
		case <-ticker.C:
		}
	}
}

func (c *Client) healthReady(ctx context.Context) bool {
	healthCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	client := NewClient(ClientOptions{
		SocketPath: c.socketPath,
		Timeout:    200 * time.Millisecond,
	})
	_, err := client.Health(healthCtx)
	return err == nil
}

func flockContext(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return fmt.Errorf("acquire start lock %s: %w", file.Name(), err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("acquire start lock %s: %w", file.Name(), ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func startDaemonProcess(ctx context.Context, opts DaemonStartOptions) error {
	if len(opts.Command) == 0 {
		return errors.New("daemon command is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", opts.Command[0], err)
	}
	return cmd.Process.Release()
}

func isDaemonUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
