package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	var envelope RequestEnvelope
	if !decodeRequest(w, r, &envelope) {
		return
	}
	if envelope.Type == "" {
		writeAPIError(w, BadRequest("request type is required"), http.StatusBadRequest)
		return
	}
	if envelope.RequestID == "" {
		writeAPIError(w, BadRequest("request_id is required"), http.StatusBadRequest)
		return
	}

	switch envelope.Type {
	case RequestTypeExecutionPlan:
		s.handleExecutionPlan(w, r, envelope)
	default:
		writeAPIError(w, BadRequest(fmt.Sprintf("unsupported request type %q", envelope.Type)), http.StatusBadRequest)
	}
}

func (s *Server) handleExecutionPlan(w http.ResponseWriter, r *http.Request, envelope RequestEnvelope) {
	var payload ExecutionPlanPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		writeAPIError(w, BadRequest(fmt.Sprintf("invalid execution_plan payload: %v", err)), http.StatusBadRequest)
		return
	}

	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.service.ApplyExecutionPlan(ctx, payload.ToRuntime(envelope.RequestID))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ExecutionPlanFromRuntime(response))
}
