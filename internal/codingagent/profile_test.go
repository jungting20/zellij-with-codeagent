package codingagent_test

import (
	"errors"
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

func TestProfileBuildManagedCommandReadOnlyCodex(t *testing.T) {
	profile, ok := codingagent.LookupProfile(codingagent.KindCodex)
	if !ok {
		t.Fatal("LookupProfile(codex) missing")
	}

	got, err := profile.BuildManagedCommand(codingagent.AccessReadOnly, "review this repository", nil)
	want := []string{"codex", "--sandbox", "read-only", "--ask-for-approval", "never", "review this repository"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("BuildManagedCommand() = %#v, %v; want %#v, nil", got, err, want)
	}
	if slices.Contains(got, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("read-only command includes permission bypass: %#v", got)
	}
}

func TestProfileBuildManagedCommandFullPreservesBypassCommand(t *testing.T) {
	profile, ok := codingagent.LookupProfile(codingagent.KindCodex)
	if !ok {
		t.Fatal("LookupProfile(codex) missing")
	}

	got, err := profile.BuildManagedCommand(codingagent.AccessFull, "", []string{"implement this"})
	want := []string{"codex", "--dangerously-bypass-approvals-and-sandbox", "implement this"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("BuildManagedCommand() = %#v, %v; want %#v, nil", got, err, want)
	}
}

func TestProfileBuildManagedCommandRejectsUnsupportedAccess(t *testing.T) {
	profile, ok := codingagent.LookupProfile(codingagent.KindCodex)
	if !ok {
		t.Fatal("LookupProfile(codex) missing")
	}
	if _, err := profile.BuildManagedCommand(codingagent.AccessMode("limited"), "", nil); !errors.Is(err, codingagent.ErrInvalidAccessMode) {
		t.Fatalf("BuildManagedCommand(limited) error = %v, want ErrInvalidAccessMode", err)
	}
}

func TestProfileBuildManagedCommandRejectsReadOnlyNonCodex(t *testing.T) {
	profile, ok := codingagent.LookupProfile(codingagent.KindGemini)
	if !ok {
		t.Fatal("LookupProfile(gemini) missing")
	}
	if _, err := profile.BuildManagedCommand(codingagent.AccessReadOnly, "", nil); !errors.Is(err, codingagent.ErrInvalidAccessMode) {
		t.Fatalf("BuildManagedCommand(read-only Gemini) error = %v, want ErrInvalidAccessMode", err)
	}
}

func TestProfileBuildManagedCommandRejectsReadOnlyOptionInjection(t *testing.T) {
	profile, _ := codingagent.LookupProfile(codingagent.KindCodex)
	for _, tc := range []struct {
		name   string
		prompt string
		extra  []string
	}{
		{name: "arbitrary arguments", extra: []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{name: "option-like prompt", prompt: "--config sandbox=workspace-write"},
		{name: "additional writable directory", prompt: "--add-dir /tmp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := profile.BuildManagedCommand(codingagent.AccessReadOnly, tc.prompt, tc.extra); !errors.Is(err, codingagent.ErrInvalidAccessMode) {
				t.Fatalf("BuildManagedCommand() error = %v, want ErrInvalidAccessMode", err)
			}
		})
	}
}

func TestParseKindRejectsExecutableNames(t *testing.T) {
	for _, value := range []string{"agy", "agent"} {
		if _, err := codingagent.ParseKind(value); err == nil {
			t.Fatalf("ParseKind(%q) error = nil", value)
		}
	}
}
