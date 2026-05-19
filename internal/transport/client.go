package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ClientOptions struct {
	SocketPath string
	Timeout    time.Duration
}

type Client struct {
	baseURL string
	http    *http.Client
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
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", opts.SocketPath)
		},
	}
	return &Client{
		baseURL: "http://agentd",
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
	response, err := c.http.Do(req)
	if err != nil {
		return nil, err
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

func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if err != nil {
		return err
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

func decodeClientError(response *http.Response) error {
	var errorResponse ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
		return fmt.Errorf("agentd transport http %d: decode error response: %w", response.StatusCode, err)
	}
	return &ClientError{StatusCode: response.StatusCode, APIError: errorResponse.Error}
}
