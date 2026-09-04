package transport

import (
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := s.requestContext(r)
		defer cancel()
		response, err := s.service.ListAgents(ctx)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ListAgentsFromCodingAgent(response))
	case http.MethodPost:
		var request StartAgentRequest
		if !decodeRequest(w, r, &request) {
			return
		}
		ctx, cancel := s.requestContext(r)
		defer cancel()
		response, err := s.service.StartAgent(ctx, request.ToCodingAgent())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, StartAgentFromCodingAgent(response))
	default:
		writeAPIError(w, BadRequest("unsupported method for /v1/agents"), http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentAction(w http.ResponseWriter, r *http.Request) {
	agentID, action, ok := splitAgentAction(r.URL.EscapedPath())
	if !ok || agentID == "" {
		writeAPIError(w, BadRequest("agent id and action are required"), http.StatusBadRequest)
		return
	}
	if action != "focus" && action != "pin" {
		writeAPIError(w, APIError{Code: CodeNotFound, Message: "agent action not found"}, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, BadRequest(action+" requires POST"), http.StatusMethodNotAllowed)
		return
	}
	if action == "pin" {
		var request SetAgentPinnedRequest
		if !decodeRequest(w, r, &request) {
			return
		}
		ctx, cancel := s.requestContext(r)
		defer cancel()
		response, err := s.service.SetAgentPinned(ctx, request.ToCodingAgent(agentID))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, SetAgentPinnedFromCodingAgent(response))
		return
	}
	var request FocusAgentRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.FocusAgent(ctx, request.ToCodingAgent(agentID))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FocusAgentFromCodingAgent(response))
}

func (s *Server) handleNextAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, BadRequest("next focus requires POST"), http.StatusMethodNotAllowed)
		return
	}
	var request FocusNextAgentRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.FocusNextAgent(ctx, request.ToCodingAgent())
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FocusNextAgentFromCodingAgent(response))
}

func (s *Server) handlePreviousAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, BadRequest("previous focus requires POST"), http.StatusMethodNotAllowed)
		return
	}
	var request FocusPreviousAgentRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.FocusPreviousAgent(ctx, request.ToCodingAgent())
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FocusNextAgentFromCodingAgent(response))
}

func splitAgentAction(escapedPath string) (string, string, bool) {
	rest := strings.TrimPrefix(escapedPath, "/v1/agents/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", false
	}
	action, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", false
	}
	return id, action, true
}
