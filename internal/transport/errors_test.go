package transport

import (
	"context"
	"errors"
	"net/http"
	"testing"

	rt "zellij-with-codeagent/internal/runtime"
)

func TestErrorForPaneNotFound(t *testing.T) {
	apiErr, status := ErrorFor(rt.ErrPaneNotFound)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
	if apiErr.Code != CodeNotFound || apiErr.Retryable {
		t.Fatalf("api error = %#v, want non-retryable not_found", apiErr)
	}
}

func TestErrorForCleanupPartial(t *testing.T) {
	apiErr, status := ErrorFor(errors.Join(rt.ErrCleanupPartial, errors.New("2 pane(s) failed")))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if apiErr.Code != CodeCleanupPartial || !apiErr.Retryable {
		t.Fatalf("api error = %#v, want retryable cleanup_partial", apiErr)
	}
}

func TestErrorForTimeout(t *testing.T) {
	apiErr, status := ErrorFor(context.DeadlineExceeded)
	if status != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", status, http.StatusGatewayTimeout)
	}
	if apiErr.Code != CodeTimeout || !apiErr.Retryable {
		t.Fatalf("api error = %#v, want retryable timeout", apiErr)
	}
}

func TestErrorForUnknownRuntimeError(t *testing.T) {
	apiErr, status := ErrorFor(errors.New("boom"))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if apiErr.Code != CodeRuntimeError || apiErr.Message != "boom" {
		t.Fatalf("api error = %#v, want runtime_error boom", apiErr)
	}
}
