package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"zellij-with-codeagent/internal/eventbus"
	rt "zellij-with-codeagent/internal/runtime"
)

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
