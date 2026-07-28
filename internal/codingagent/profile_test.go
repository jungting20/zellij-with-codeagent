package codingagent_test

import (
	"slices"
	"testing"

	"zellij-with-codeagent/internal/codingagent"
)

func TestProfileBuildCommandWithBypass(t *testing.T) {
	tests := []struct {
		kind codingagent.Kind
		want []string
	}{
		{codingagent.KindCodex, []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}},
		{codingagent.KindClaude, []string{"claude", "--dangerously-skip-permissions"}},
		{codingagent.KindGemini, []string{"agy", "--dangerously-skip-permissions"}},
		{codingagent.KindCursor, []string{"agent", "--yolo", "--trust"}},
	}
	for _, tt := range tests {
		profile, ok := codingagent.LookupProfile(tt.kind)
		if !ok {
			t.Fatalf("LookupProfile(%q) missing", tt.kind)
		}
		if got := profile.BuildCommand(true, nil); !slices.Equal(got, tt.want) {
			t.Fatalf("BuildCommand() = %#v, want %#v", got, tt.want)
		}
	}
}

func TestProfileBuildCommandWithoutBypassAppendsExtra(t *testing.T) {
	profile, ok := codingagent.LookupProfile(codingagent.KindCodex)
	if !ok {
		t.Fatal("LookupProfile(codex) missing")
	}
	want := []string{"codex", "--model", "custom"}
	if got := profile.BuildCommand(false, []string{"--model", "custom"}); !slices.Equal(got, want) {
		t.Fatalf("BuildCommand() = %#v, want %#v", got, want)
	}
}

func TestParseKindRejectsExecutableNames(t *testing.T) {
	for _, value := range []string{"agy", "agent"} {
		if _, err := codingagent.ParseKind(value); err == nil {
			t.Fatalf("ParseKind(%q) error = nil", value)
		}
	}
}
