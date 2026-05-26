package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	rt "zellij-with-codeagent/internal/runtime"
)

const statusClientClosedRequest = 499

type ErrorCode string

const (
	CodeBadRequest     ErrorCode = "bad_request"
	CodeNotFound       ErrorCode = "not_found"
	CodeRuntimeError   ErrorCode = "runtime_error"
	CodeCleanupPartial ErrorCode = "cleanup_partial"
	CodeStreamClosed   ErrorCode = "stream_closed"
	CodeTimeout        ErrorCode = "timeout"
)

type APIError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type ClientError struct {
	StatusCode int
	APIError   APIError
}

func (e *ClientError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("agentd transport %s: %s", e.APIError.Code, e.APIError.Message)
}

func BadRequest(message string) APIError {
	return APIError{Code: CodeBadRequest, Message: message}
}

func StreamClosed(message string) APIError {
	return APIError{Code: CodeStreamClosed, Message: message, Retryable: true}
}

func ErrorFor(err error) (APIError, int) {
	if err == nil {
		return APIError{}, http.StatusOK
	}
	switch {
	case errors.Is(err, rt.ErrPaneNotFound), errors.Is(err, rt.ErrSessionNotFound), errors.Is(err, rt.ErrTabNotFound):
		return APIError{Code: CodeNotFound, Message: err.Error()}, http.StatusNotFound
	case errors.Is(err, rt.ErrMissingPaneID), errors.Is(err, rt.ErrInvalidExecutionPlan):
		return APIError{Code: CodeBadRequest, Message: err.Error()}, http.StatusBadRequest
	case errors.Is(err, rt.ErrCleanupPartial):
		return APIError{Code: CodeCleanupPartial, Message: err.Error(), Retryable: true}, http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		return APIError{Code: CodeTimeout, Message: err.Error(), Retryable: true}, http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return APIError{Code: CodeStreamClosed, Message: err.Error(), Retryable: true}, statusClientClosedRequest
	default:
		return APIError{Code: CodeRuntimeError, Message: err.Error()}, http.StatusInternalServerError
	}
}

func IsNotFound(err error) bool {
	var clientErr *ClientError
	return errors.As(err, &clientErr) && clientErr.APIError.Code == CodeNotFound
}
