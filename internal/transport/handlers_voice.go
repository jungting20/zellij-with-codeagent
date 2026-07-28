package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (s *Server) handleVoiceNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, BadRequest("voice notifications requires POST"), http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req VoiceNotificationRequest
	if !decodeVoiceNotificationRequest(w, r, &req) {
		return
	}
	if err := validateVoiceNotificationRequest(req); err != nil {
		writeAPIError(w, BadRequest(err.Error()), http.StatusBadRequest)
		return
	}

	ctx, cancel := s.requestContext(r)
	defer cancel()
	response, err := s.voiceNotifications.QueueVoiceNotification(ctx, req)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	switch response.Status {
	case "queued":
		writeJSON(w, http.StatusAccepted, response)
	case "duplicate":
		writeJSON(w, http.StatusOK, response)
	default:
		writeAPIError(w, APIError{
			Code:    CodeRuntimeError,
			Message: fmt.Sprintf("unknown voice notification status %q", response.Status),
		}, http.StatusInternalServerError)
	}
}

func decodeVoiceNotificationRequest(w http.ResponseWriter, r *http.Request, target *VoiceNotificationRequest) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, BadRequest(fmt.Sprintf("invalid json request: %v", err)), http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			writeAPIError(w, BadRequest("invalid json request: trailing data"), http.StatusBadRequest)
		} else {
			writeAPIError(w, BadRequest(fmt.Sprintf("invalid json request: %v", err)), http.StatusBadRequest)
		}
		return false
	}
	return true
}

func validateVoiceNotificationRequest(req VoiceNotificationRequest) error {
	if req.RequestID == "" || len(req.RequestID) > 256 {
		return fmt.Errorf("request_id must be between 1 and 256 bytes")
	}
	prefix := strings.TrimSpace(req.Prefix)
	if prefix == "" || utf8.RuneCountInString(prefix) > 128 {
		return fmt.Errorf("prefix must contain between 1 and 128 runes")
	}
	if req.TicketID <= 0 {
		return fmt.Errorf("ticket_id must be positive")
	}
	if len(req.Summary) > 4<<10 {
		return fmt.Errorf("summary must not exceed 4096 bytes")
	}
	if strings.ContainsAny(req.Summary, "\r\n") {
		return fmt.Errorf("summary must not contain line breaks")
	}
	return nil
}
