package network

import (
	"testing"

	cdpnetwork "github.com/chromedp/cdproto/network"
)

func TestRunRequiresURLFlag(t *testing.T) {
	if code := Run(nil); code == 0 {
		t.Fatalf("Run(nil) = %d, want non-zero", code)
	}
}

func TestShouldWaitForAuthRedirectOnlyOn302(t *testing.T) {
	tests := []struct {
		name   string
		status int64
		want   bool
	}{
		{name: "302 redirect", status: 302, want: true},
		{name: "301 redirect", status: 301, want: false},
		{name: "success", status: 200, want: false},
		{name: "unauthorized", status: 401, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldWaitForAuthRedirect(tt.status); got != tt.want {
				t.Fatalf("shouldWaitForAuthRedirect(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestRedirectStatusFromRequestWillBeSent(t *testing.T) {
	status, ok := redirectStatusFromRequestWillBeSent(&cdpnetwork.EventRequestWillBeSent{
		RedirectResponse: &cdpnetwork.Response{Status: 302},
	})
	if !ok {
		t.Fatal("redirectStatusFromRequestWillBeSent did not report redirect status")
	}
	if status != 302 {
		t.Fatalf("redirect status = %d, want 302", status)
	}
}

func TestRedirectStatusFromRequestWillBeSentWithoutRedirect(t *testing.T) {
	if _, ok := redirectStatusFromRequestWillBeSent(&cdpnetwork.EventRequestWillBeSent{}); ok {
		t.Fatal("redirectStatusFromRequestWillBeSent reported redirect status for empty event")
	}
}
