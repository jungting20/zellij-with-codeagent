package transport

import (
	"errors"
	"net/http"

	rt "zellij-with-codeagent/internal/runtime"
)

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
	response, err := s.service.Cleanup(ctx, req.ToRuntime())
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
