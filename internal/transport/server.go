package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	rt "zellij-with-codeagent/internal/runtime"
)

const DefaultRequestTimeout = 30 * time.Second

type ServerOptions struct {
	Service        ServerRuntime
	SocketPath     string
	RequestTimeout time.Duration
	Version        string
}

type ServerRuntime interface {
	rt.PaneService
	rt.RuntimeInspectionService
	rt.EventService
	rt.ReconciliationService
	rt.CleanupService
	rt.ExecutionPlanService
	rt.SessionInspectionService
}

type Server struct {
	service        ServerRuntime
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
	case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions":
		s.handleListSessions(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		s.handleSessionRoute(w, r)
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
	case r.Method == http.MethodPost && r.URL.Path == "/v1/requests":
		s.handleRequests(w, r)
	default:
		writeAPIError(w, APIError{Code: CodeNotFound, Message: "route not found"}, http.StatusNotFound)
	}
}

func (s *Server) requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), s.requestTimeout)
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
