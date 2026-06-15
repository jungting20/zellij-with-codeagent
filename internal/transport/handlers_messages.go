package transport

import "net/http"

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, BadRequest("messages requires POST"), http.StatusMethodNotAllowed)
		return
	}

	var req SendMessageRequest
	if !decodeRequest(w, r, &req) {
		return
	}

	ctx, cancel := s.requestContext(r)
	defer cancel()

	response, err := s.service.SendMessage(ctx, req.ToRuntime())
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, MessageFromRuntime(response))
}
