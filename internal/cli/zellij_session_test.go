package cli

import (
	"errors"
	"testing"
)

func TestResolveZellijSessionPrefersExplicitValue(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "from-env")
	got, err := ResolveZellijSession("  from-flag  ")
	if err != nil || got != "from-flag" {
		t.Fatalf("ResolveZellijSession() = %q, %v, want from-flag, nil", got, err)
	}
}

func TestResolveZellijSessionUsesCallingProcessEnvironment(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "  caller-session  ")
	got, err := ResolveZellijSession("")
	if err != nil || got != "caller-session" {
		t.Fatalf("ResolveZellijSession() = %q, %v, want caller-session, nil", got, err)
	}
}

func TestResolveZellijSessionRejectsMissingValue(t *testing.T) {
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	_, err := ResolveZellijSession("   ")
	if !errors.Is(err, ErrZellijSessionRequired) {
		t.Fatalf("ResolveZellijSession() error = %v, want %v", err, ErrZellijSessionRequired)
	}
}
