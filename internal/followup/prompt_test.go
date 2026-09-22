package followup

import (
	"testing"

	"zellij-with-codeagent/internal/codingagent"
)

func TestPromptIsEmptyRequiresCurrentComposerWithoutDraft(t *testing.T) {
	for _, tc := range []struct {
		name, screen string
		kind         codingagent.Kind
		want         bool
	}{
		{"codex empty", "› ", codingagent.KindCodex, true},
		{"codex alternate marker", "response\n»  \n\n", codingagent.KindCodex, true},
		{"codex submitted history only", "› previous request\n• Response complete", codingagent.KindCodex, false},
		{"codex history then current", "› previous request\n• Response complete\n› ", codingagent.KindCodex, true},
		{"codex draft", "› user is typing", codingagent.KindCodex, false},
		{"codex wrapped draft", "› first line\n  second line", codingagent.KindCodex, false},
		{"codex empty first draft line", "› \n  second line", codingagent.KindCodex, false},
		{"codex nested marker in draft", "› draft\n  › ", codingagent.KindCodex, false},
		{"codex multiline first blank then marker", "› \n  typed\n  › ", codingagent.KindCodex, false},
		{"codex historical empty marker then response", "› \n• Response", codingagent.KindCodex, false},
		{"codex quoted marker", "A quote contains › here", codingagent.KindCodex, false},
		{"codex animation unknown", "»⠁Ask Codex to do anything", codingagent.KindCodex, false},
		{"codex styled shortcuts", "› \n\n\x1b[2m? for shortcuts\x1b[0m", codingagent.KindCodex, true},
		{"codex styled context", "› \n\x1b[90m100% context left\x1b[0m", codingagent.KindCodex, true},
		{"codex styled combined footer", "› \n\x1b[2m? for shortcuts       92% context left\x1b[0m", codingagent.KindCodex, true},
		{"codex styled model and path", "› \n\x1b[38;5;244mgpt-5.4 high · /repo/project\x1b[0m", codingagent.KindCodex, true},
		{"codex styled path", "› \n\x1b[2m~/repo/project\x1b[0m", codingagent.KindCodex, true},
		{"codex unstyled footer phrase may be draft", "› \n  ? for shortcuts", codingagent.KindCodex, false},
		{"codex unstyled context may be draft", "› \n  100% context left", codingagent.KindCodex, false},
		{"codex arbitrary styled draft", "› \n\x1b[2mdelete the files\x1b[0m", codingagent.KindCodex, false},
		{"claude boxed empty", "╭────────╮\n│ ❯ \n╰────────╯", codingagent.KindClaude, true},
		{"claude closed frame", "╭────────╮\n│ ❯    │\n│      │\n╰────────╯", codingagent.KindClaude, true},
		{"claude rules without corners", "─────────\n❯ \n─────────", codingagent.KindClaude, true},
		{"claude legacy empty", "response\n❯ ", codingagent.KindClaude, true},
		{"claude legacy blocker text separate", "Waiting for permission\nEsc to cancel\n❯", codingagent.KindClaude, true},
		{"claude boxed draft", "╭────────╮\n│ ❯ draft\n╰────────╯", codingagent.KindClaude, false},
		{"claude empty first draft line", "╭────────╮\n│ ❯ \n│ second line\n╰────────╯", codingagent.KindClaude, false},
		{"claude text before marker inside box", "╭────────╮\n│ first line\n│ ❯ \n╰────────╯", codingagent.KindClaude, false},
		{"claude nested marker draft", "╭────────╮\n│ ❯ draft\n│ ❯ \n╰────────╯", codingagent.KindClaude, false},
		{"claude unboxed nested marker", "❯ draft\n  ❯ ", codingagent.KindClaude, false},
		{"claude partial box", "╭────────╮\n│ ❯ ", codingagent.KindClaude, false},
		{"claude historical box", "╭────────╮\n│ ❯ \n╰────────╯\nResponse follows", codingagent.KindClaude, false},
		{"claude latest box after history", "╭────────╮\n│ ❯ old\n╰────────╯\nResponse\n╭────────╮\n│ ❯ \n╰────────╯", codingagent.KindClaude, true},
		{"claude styled mode footer", "╭────────╮\n│ ❯ \n╰────────╯\n\x1b[2m⏵⏵ bypass permissions on (shift+tab to cycle)\x1b[0m", codingagent.KindClaude, true},
		{"claude menu entry is not empty", "❯ 1. Yes", codingagent.KindClaude, false},
		{"no prompt", "response complete", codingagent.KindCodex, false},
		{"wrong kind marker", "› ", codingagent.KindClaude, false},
		{"unsupported kind", "› ", codingagent.KindGemini, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptIsEmpty(tc.kind, tc.screen); got != tc.want {
				t.Fatalf("promptIsEmpty(%q, %q) = %t, want %t", tc.kind, tc.screen, got, tc.want)
			}
		})
	}
}

func TestPromptPlaceholderNeedsStylingOnEveryContentCharacter(t *testing.T) {
	for _, placeholder := range []struct {
		kind         codingagent.Kind
		marker, text string
	}{
		{codingagent.KindCodex, "› ", "Use /skills to list available skills"},
		{codingagent.KindCodex, "» ", "Find and fix a bug in @filename"},
		{codingagent.KindCodex, "› ", "Explain this codebase"},
		{codingagent.KindClaude, "❯ ", "Ask Claude"},
	} {
		t.Run(placeholder.text, func(t *testing.T) {
			for _, style := range []string{"2", "3", "90", "38;5;244", "38;2;128;128;128", "38:2::128:128:128"} {
				screen := placeholder.marker + "\x1b[" + style + "m" + placeholder.text + "\x1b[0m"
				if !promptIsEmpty(placeholder.kind, screen) {
					t.Errorf("styled placeholder rejected: %q", screen)
				}
			}
			for _, screen := range []string{
				placeholder.marker + placeholder.text,
				"\x1b[2m" + placeholder.marker + "\x1b[0m" + placeholder.text,
				placeholder.marker + "\x1b[2m" + placeholder.text[:1] + "\x1b[22m" + placeholder.text[1:],
				placeholder.marker + "\x1b[31m" + placeholder.text + "\x1b[0m",
				placeholder.marker + "\x1b[48;2;2;3;90m" + placeholder.text + "\x1b[0m",
				placeholder.marker + "\x1b[2;7m" + placeholder.text + "\x1b[0m",
				placeholder.marker + "\x1b[90;39m" + placeholder.text,
				placeholder.marker + "\x1b[3;23m" + placeholder.text,
			} {
				if promptIsEmpty(placeholder.kind, screen) {
					t.Errorf("typed or ambiguously styled text accepted: %q", screen)
				}
			}
		})
	}
	for _, screen := range []string{
		"❯ \x1b[2mTry \"write a function\"\x1b[0m",
		"› \x1b[2mUse /skills to list\navailable skills\x1b[0m",
		"› \x1b[2mUnrecognized suggestion\x1b[0m",
	} {
		if promptIsEmpty(codingagent.KindCodex, screen) || promptIsEmpty(codingagent.KindClaude, screen) {
			t.Fatalf("unknown or wrapped placeholder accepted: %q", screen)
		}
	}
}
