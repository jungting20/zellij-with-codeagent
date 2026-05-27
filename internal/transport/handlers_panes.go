package transport

import (
	"net/http"
	"strings"

	rt "zellij-with-codeagent/internal/runtime"
)

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
		response, err := s.service.CreatePane(ctx, req.ToRuntime())
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

func splitPaneAction(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/v1/panes/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
