package codingagent_test

import (
	"errors"
	"testing"

	"zellij-with-codeagent/internal/codingagent"
)

func TestParseAccessModeDefaultsToFull(t *testing.T) {
	got, err := codingagent.ParseAccessMode("")
	if err != nil || got != codingagent.AccessFull {
		t.Fatalf("ParseAccessMode(\"\") = %q, %v; want full, nil", got, err)
	}
}

func TestParseAccessModeRejectsUnknownValue(t *testing.T) {
	_, err := codingagent.ParseAccessMode("limited")
	if !errors.Is(err, codingagent.ErrInvalidAccessMode) {
		t.Fatalf("ParseAccessMode(limited) error = %v, want ErrInvalidAccessMode", err)
	}
}
