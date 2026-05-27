package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
)

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
