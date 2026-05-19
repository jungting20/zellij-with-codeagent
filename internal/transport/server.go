package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

const DefaultRequestTimeout = 30 * time.Second

type ServerOptions struct {
	Service        rt.RuntimeService
	SocketPath     string
	RequestTimeout time.Duration
	Version        string
}

type Server struct {
	service        rt.RuntimeService
	socketPath     string
	requestTimeout time.Duration
	version        string
	httpServer     *http.Server
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Service == nil {
		return nil, errors.New("transport: runtime service is required")
	}
	if opts.SocketPath == "" {
		return nil, errors.New("transport: socket path is required")
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	server := &Server{
		service:        opts.Service,
		socketPath:     opts.SocketPath,
		requestTimeout: requestTimeout,
		version:        opts.Version,
	}
	server.httpServer = &http.Server{Handler: server}
	return server, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prepareSocket(s.socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	defer os.Remove(s.socketPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			_ = listener.Close()
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return ctx.Err()
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/health":
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok", Version: s.version})
	case r.URL.Path == "/v1/panes":
		s.handlePanes(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/panes/"):
		s.handlePaneAction(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/runtime":
		s.handleInspectRuntime(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/events/recent":
		s.handleRecentEvents(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/events/stream":
		s.handleEventStream(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/reconcile":
		s.handleReconcile(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/cleanup":
		s.handleCleanup(w, r)
	default:
		writeAPIError(w, APIError{Code: CodeNotFound, Message: "route not found"}, http.StatusNotFound)
	}
}

func (s *Server) handlePanes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := s.requestContext(r)
		defer cancel()
		response, err := s.service.ListPanes(ctx)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ListPanesResponse{Panes: PanesFromRuntime(response.Panes)})
	case http.MethodPost:
		var req CreatePaneRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		ctx, cancel := s.requestContext(r)
		defer cancel()
		response, err := s.service.CreatePane(ctx, RuntimeCreatePaneRequest(req))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, CreatePaneResponse{Pane: PaneFromRuntime(response.Pane)})
	default:
		writeAPIError(w, BadRequest("unsupported method for /v1/panes"), http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePaneAction(w http.ResponseWriter, r *http.Request) {
	paneID, action, ok := splitPaneAction(r.URL.Path)
	if !ok || paneID == "" {
		writeAPIError(w, BadRequest("pane id and action are required"), http.StatusBadRequest)
		return
	}
	switch action {
	case "input":
		if r.Method != http.MethodPost {
			writeAPIError(w, BadRequest("input requires POST"), http.StatusMethodNotAllowed)
			return
		}
		var req SendInputRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		ctx, cancel := s.requestContext(r)
		defer cancel()
		if err := s.service.SendInput(ctx, rt.SendInputRequest{PaneID: rt.PaneID(paneID), Text: req.Text}); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "snapshot":
		if r.Method != http.MethodPost {
			writeAPIError(w, BadRequest("snapshot requires POST"), http.StatusMethodNotAllowed)
			return
		}
		var req SnapshotOutputRequest
		if !decodeOptionalRequest(w, r, &req) {
			return
		}
		ctx, cancel := s.requestContext(r)
		defer cancel()
		response, err := s.service.SnapshotOutput(ctx, rt.SnapshotOutputRequest{
			PaneID: rt.PaneID(paneID),
			Full:   req.Full,
			ANSI:   req.ANSI,
		})
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, SnapshotOutputResponse{
			Pane:   PaneFromRuntime(response.Pane),
			Output: response.Output,
		})
	default:
		writeAPIError(w, APIError{Code: CodeNotFound, Message: "pane action not found"}, http.StatusNotFound)
	}
}

func (s *Server) handleInspectRuntime(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.InspectRuntime(ctx, rt.InspectRuntimeRequest{})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, RuntimeStatusFromRuntime(response))
}

func (s *Server) handleRecentEvents(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writeAPIError(w, BadRequest("limit must be a non-negative integer"), http.StatusBadRequest)
			return
		}
		limit = value
	}
	types := make([]eventbus.EventType, 0, len(r.URL.Query()["type"]))
	for _, value := range r.URL.Query()["type"] {
		if value != "" {
			types = append(types, eventbus.EventType(value))
		}
	}

	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.RecentEvents(ctx, rt.RecentEventsRequest{Limit: limit, Types: types})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, RecentEventsResponse{Events: EventsFromRuntime(response.Events)})
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	events, unsubscribe, err := s.service.SubscribeEvents(r.Context())
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	encoder := json.NewEncoder(w)

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := encoder.Encode(EventFromRuntime(event)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.Reconcile(ctx, rt.ReconcileRequest{})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileFromRuntime(response))
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	var req CleanupRequest
	if !decodeOptionalRequest(w, r, &req) {
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.Cleanup(ctx, RuntimeCleanupRequest(req))
	if err != nil && !errors.Is(err, rt.ErrCleanupPartial) {
		writeRuntimeError(w, err)
		return
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusConflict
	}
	writeJSON(w, status, CleanupFromRuntime(response))
}

func (s *Server) requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), s.requestTimeout)
}

func splitPaneAction(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/v1/panes/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, BadRequest(fmt.Sprintf("invalid json request: %v", err)), http.StatusBadRequest)
		return false
	}
	return true
}

func decodeOptionalRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	return decodeRequest(w, r, target)
}

func writeRuntimeError(w http.ResponseWriter, err error) {
	apiError, status := ErrorFor(err)
	writeAPIError(w, apiError, status)
}

func writeAPIError(w http.ResponseWriter, apiError APIError, status int) {
	writeJSON(w, status, ErrorResponse{Error: apiError})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func prepareSocket(path string) error {
	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return fmt.Errorf("transport: socket %s is already in use", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("transport: remove stale socket %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
