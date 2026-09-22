package codingagent

import (
	"embed"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

//go:embed testdata/*/*.txt
var embeddedManifestFixtures embed.FS

type embeddedManifestCase struct {
	name           string
	kind           Kind
	fixture        string
	inputField     string
	oscProgress    string
	wantState      State
	wantRule       string
	wantPriority   int
	wantIdle       bool
	wantWorking    bool
	wantBlocker    bool
	wantSkipUpdate bool
	wantFallback   bool
	wantReason     string
}

func TestEmbeddedManifests(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatalf("LoadEmbeddedDetector() errors = %v, want none", loadErrors)
	}

	tests := []embeddedManifestCase{
		{name: "codex osc action required", kind: KindCodex, fixture: "testdata/codex/blocked-osc-action-required.txt", inputField: "osc_title", wantState: StateBlocked, wantRule: "osc_title_blocked", wantPriority: 1100, wantBlocker: true},
		{name: "codex osc spinner", kind: KindCodex, fixture: "testdata/codex/working-osc-spinner.txt", inputField: "osc_title", wantState: StateWorking, wantRule: "osc_title_working", wantPriority: 1050, wantWorking: true},
		{name: "codex transcript viewer", kind: KindCodex, fixture: "testdata/codex/skip-transcript-viewer.txt", inputField: "screen", wantRule: "transcript_viewer", wantPriority: 1000, wantSkipUpdate: true},
		{name: "codex live strong blocker", kind: KindCodex, fixture: "testdata/codex/blocked-live-strong.txt", inputField: "screen", wantState: StateBlocked, wantRule: "live_strong_blocker", wantPriority: 900, wantBlocker: true},
		{name: "codex weak blocker", kind: KindCodex, fixture: "testdata/codex/blocked-weak-yes-no.txt", inputField: "screen", wantState: StateBlocked, wantRule: "weak_blocker", wantPriority: 600},
		{name: "codex working footer", kind: KindCodex, fixture: "testdata/codex/working-screen-footer.txt", inputField: "screen", wantState: StateWorking, wantRule: "screen_working_fallback", wantPriority: 500, wantWorking: true},
		{name: "codex background terminal above prompt", kind: KindCodex, fixture: "testdata/codex/working-background-terminal.txt", inputField: "screen", wantState: StateWorking, wantRule: "screen_working_fallback", wantPriority: 500, wantWorking: true},
		{name: "codex reconnecting footer", kind: KindCodex, fixture: "testdata/codex/working-reconnecting-footer.txt", inputField: "screen", wantState: StateWorking, wantRule: "screen_working_fallback", wantPriority: 500, wantWorking: true},
		{name: "codex thinking footer", kind: KindCodex, fixture: "testdata/codex/working-thinking-footer.txt", inputField: "screen", wantState: StateWorking, wantRule: "screen_working_fallback", wantPriority: 500, wantWorking: true},
		{name: "codex interrupted conversation excludes working footer", kind: KindCodex, fixture: "testdata/codex/idle-conversation-interrupted.txt", inputField: "screen", wantState: StateIdle, wantRule: "conversation_interrupted", wantPriority: 550, wantIdle: true},
		{name: "codex prompt idle", kind: KindCodex, fixture: "testdata/codex/idle-screen-prompt.txt", inputField: "screen", wantState: StateIdle, wantRule: "screen_prompt_idle", wantPriority: 200, wantIdle: true},
		{name: "codex osc idle", kind: KindCodex, fixture: "testdata/codex/idle-osc-title.txt", inputField: "osc_title", wantState: StateIdle, wantRule: "osc_title_idle", wantPriority: 100, wantIdle: true},
		{name: "codex unmatched idle candidate", kind: KindCodex, fixture: "testdata/codex/idle-unmatched.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},

		{name: "gemini apply confirmation wins", kind: KindGemini, fixture: "testdata/gemini/blocked-apply-confirmation.txt", inputField: "screen", wantState: StateBlocked, wantRule: "apply_or_allow_change", wantPriority: 300, wantBlocker: true},
		{name: "gemini working cancel hint", kind: KindGemini, fixture: "testdata/gemini/working-esc-cancel.txt", inputField: "screen", wantState: StateWorking, wantRule: "esc_cancel_working", wantPriority: 100, wantWorking: true},
		{name: "gemini unmatched fallback", kind: KindGemini, fixture: "testdata/gemini/idle-unmatched.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},

		{name: "cursor write file approval", kind: KindCursor, fixture: "testdata/cursor/blocked-write-file.txt", inputField: "screen", wantState: StateBlocked, wantRule: "write_file_approval", wantPriority: 320, wantBlocker: true},
		{name: "cursor command approval", kind: KindCursor, fixture: "testdata/cursor/blocked-command-approval.txt", inputField: "screen", wantState: StateBlocked, wantRule: "approval_prompt", wantPriority: 300, wantBlocker: true},
		{name: "cursor stop hint", kind: KindCursor, fixture: "testdata/cursor/working-stop-hint.txt", inputField: "screen", wantState: StateWorking, wantRule: "stop_hint_working", wantPriority: 100, wantWorking: true},
		{name: "cursor background task", kind: KindCursor, fixture: "testdata/cursor/working-background-task.txt", inputField: "screen", wantState: StateWorking, wantRule: "background_task_status_working", wantPriority: 95, wantWorking: true},
		{name: "cursor spinner", kind: KindCursor, fixture: "testdata/cursor/working-spinner.txt", inputField: "screen", wantState: StateWorking, wantRule: "spinner_working", wantPriority: 90, wantWorking: true},
		{name: "cursor non ascii spinner suffix is not working", kind: KindCursor, fixture: "testdata/cursor/idle-non-ascii-spinner.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},
		{name: "cursor unmatched fallback", kind: KindCursor, fixture: "testdata/cursor/idle-unmatched.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},

		{name: "claude osc spinner", kind: KindClaude, fixture: "testdata/claude/working-osc-spinner.txt", inputField: "osc_title", wantState: StateWorking, wantRule: "osc_title_working", wantPriority: 1100, wantWorking: true},
		{name: "claude transcript viewer", kind: KindClaude, fixture: "testdata/claude/skip-transcript-viewer.txt", inputField: "screen", wantRule: "transcript_viewer", wantPriority: 1000, wantSkipUpdate: true},
		{name: "claude live blocked form wins equal priority", kind: KindClaude, fixture: "testdata/claude/blocked-live-selection-form.txt", inputField: "screen", wantState: StateBlocked, wantRule: "live_blocked_form", wantPriority: 980, wantBlocker: true},
		{name: "claude dynamic workflow", kind: KindClaude, fixture: "testdata/claude/blocked-dynamic-workflow.txt", inputField: "screen", wantState: StateBlocked, wantRule: "dynamic_workflow_prompt", wantPriority: 980, wantBlocker: true},
		{name: "claude btw overlay", kind: KindClaude, fixture: "testdata/claude/working-btw-overlay.txt", inputField: "screen", wantState: StateWorking, wantRule: "btw_overlay_working", wantPriority: 975, wantWorking: true},
		{name: "claude prompt box", kind: KindClaude, fixture: "testdata/claude/idle-prompt-box.txt", inputField: "screen", wantState: StateIdle, wantRule: "live_prompt_box", wantPriority: 950, wantIdle: true},
		{name: "claude model picker", kind: KindClaude, fixture: "testdata/claude/skip-model-picker.txt", inputField: "screen", wantRule: "model_picker_menu", wantPriority: 900, wantSkipUpdate: true},
		{name: "claude model picker excludes permission prompt", kind: KindClaude, fixture: "testdata/claude/blocked-model-picker-permission.txt", inputField: "screen", wantState: StateBlocked, wantRule: "bash_permission_prompt", wantPriority: 850, wantBlocker: true},
		{name: "claude model picker excludes selection form", kind: KindClaude, fixture: "testdata/claude/idle-model-picker-selection.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},
		{name: "claude bash permission", kind: KindClaude, fixture: "testdata/claude/blocked-bash-permission.txt", inputField: "screen", wantState: StateBlocked, wantRule: "bash_permission_prompt", wantPriority: 850, wantBlocker: true},
		{name: "claude generic permission", kind: KindClaude, fixture: "testdata/claude/blocked-generic-permission.txt", inputField: "screen", wantState: StateBlocked, wantRule: "generic_permission_prompt", wantPriority: 840, wantBlocker: true},
		{name: "claude legacy blocker", kind: KindClaude, fixture: "testdata/claude/blocked-legacy.txt", inputField: "screen", wantState: StateBlocked, wantRule: "legacy_no_prompt_blocker", wantPriority: 300},
		{name: "claude legacy blocker excludes empty prompt", kind: KindClaude, fixture: "testdata/claude/idle-legacy-empty-prompt.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},
		{name: "claude osc idle title wins equal priority", kind: KindClaude, fixture: "testdata/claude/idle-osc-title.txt", inputField: "osc_title", oscProgress: "4;0", wantState: StateIdle, wantRule: "osc_title_idle", wantPriority: 250, wantIdle: true},
		{name: "claude osc progress idle", kind: KindClaude, fixture: "testdata/claude/idle-osc-progress.txt", inputField: "osc_progress", wantState: StateIdle, wantRule: "osc_progress_idle", wantPriority: 250},
		{name: "claude unmatched fallback", kind: KindClaude, fixture: "testdata/claude/idle-unmatched.txt", inputField: "screen", wantState: StateIdle, wantFallback: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents, err := fs.ReadFile(embeddedManifestFixtures, tt.fixture)
			if err != nil {
				t.Fatalf("read fixture %s: %v", tt.fixture, err)
			}
			input := fixtureInput(tt.inputField, string(contents))
			if tt.oscProgress != "" {
				input.OSCProgress = tt.oscProgress
			}
			detection, err := detector.Detect(tt.kind, input)
			if err != nil {
				t.Fatalf("Detect(%q) error = %v", tt.kind, err)
			}
			assertEmbeddedDetection(t, detector, tt, detection)
		})
	}
}

func TestCodexWorkingScreenFooterAllowsZellijRightPadding(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatalf("LoadEmbeddedDetector() errors = %v, want none", loadErrors)
	}
	contents, err := fs.ReadFile(embeddedManifestFixtures, "testdata/codex/working-screen-footer.txt")
	if err != nil {
		t.Fatalf("read working fixture: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	for index := range lines {
		lines[index] += "        "
	}
	detection, err := detector.Detect(KindCodex, DetectionInput{Screen: strings.Join(lines, "\n")})
	if err != nil {
		t.Fatalf("Detect(codex) error = %v", err)
	}
	if detection.State != StateWorking || detection.RuleID != "screen_working_fallback" {
		t.Fatalf("detection = state:%q rule:%q, want working/screen_working_fallback", detection.State, detection.RuleID)
	}
}

func TestEmbeddedManifestsInvalidKindIsIsolated(t *testing.T) {
	files := make(fstest.MapFS)
	for _, name := range embeddedManifestFiles {
		contents, err := fs.ReadFile(embeddedManifests, "manifests/"+name)
		if err != nil {
			t.Fatalf("read embedded manifest %s: %v", name, err)
		}
		files["manifests/"+name] = &fstest.MapFile{Data: contents}
	}
	files["manifests/claude.yaml"] = &fstest.MapFile{Data: []byte("not: valid: yaml")}

	detector, loadErrors := loadEmbeddedDetector(files)
	if len(loadErrors) != 1 || loadErrors[KindClaude] == nil {
		t.Fatalf("load errors = %v, want only claude", loadErrors)
	}
	if !strings.Contains(loadErrors[KindClaude].Error(), "claude.yaml") {
		t.Fatalf("claude error = %q, want filename", loadErrors[KindClaude])
	}
	for _, kind := range []Kind{KindCodex, KindGemini, KindCursor} {
		detection, err := detector.Detect(kind, DetectionInput{OSCTitle: "Action Required"})
		if err != nil {
			t.Fatalf("valid %s kind unavailable: %v", kind, err)
		}
		if kind == KindCodex && detection.RuleID != "osc_title_blocked" {
			t.Fatalf("Codex detection rule = %q, want osc_title_blocked", detection.RuleID)
		}
	}
	if _, err := detector.Detect(KindClaude, DetectionInput{}); err == nil {
		t.Fatal("invalid Claude kind remained available")
	}
}

func fixtureInput(field, contents string) DetectionInput {
	contents = strings.TrimSuffix(contents, "\n")
	switch field {
	case "screen":
		return DetectionInput{Screen: contents}
	case "osc_title":
		return DetectionInput{OSCTitle: contents}
	case "osc_progress":
		return DetectionInput{OSCProgress: contents}
	default:
		panic("unknown fixture input field " + field)
	}
}

func assertEmbeddedDetection(t *testing.T, detector *Detector, tt embeddedManifestCase, got Detection) {
	t.Helper()
	if got.State != tt.wantState || got.RuleID != tt.wantRule {
		t.Fatalf("detection = state:%q rule:%q, want state:%q rule:%q", got.State, got.RuleID, tt.wantState, tt.wantRule)
	}
	if got.VisibleIdle != tt.wantIdle || got.VisibleWorking != tt.wantWorking || got.VisibleBlocker != tt.wantBlocker || got.SkipStateUpdate != tt.wantSkipUpdate || got.Fallback != tt.wantFallback {
		t.Fatalf("flags = idle:%t working:%t blocker:%t skip:%t fallback:%t, want idle:%t working:%t blocker:%t skip:%t fallback:%t", got.VisibleIdle, got.VisibleWorking, got.VisibleBlocker, got.SkipStateUpdate, got.Fallback, tt.wantIdle, tt.wantWorking, tt.wantBlocker, tt.wantSkipUpdate, tt.wantFallback)
	}
	if tt.wantFallback {
		wantReason := tt.wantReason
		if wantReason == "" {
			wantReason = "default_known_agent_idle_fallback"
		}
		if got.Reason != wantReason {
			t.Fatalf("fallback reason = %q, want %q", got.Reason, wantReason)
		}
		return
	}
	for _, rule := range detector.rules[tt.kind] {
		if rule.ID == got.RuleID {
			if rule.Priority != tt.wantPriority {
				t.Fatalf("winning rule %q priority = %d, want %d", got.RuleID, rule.Priority, tt.wantPriority)
			}
			return
		}
	}
	t.Fatalf("winning rule %q not found in detector", got.RuleID)
}

func TestCodexAnimatedPrompt(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatalf("LoadEmbeddedDetector() errors = %v", loadErrors)
	}
	for _, prompt := range []string{"›", "»"} {
		for _, separator := range []string{" ", "⠁", "⠁ "} {
			for _, working := range []bool{false, true} {
				name := prompt + separator
				if working {
					name += "working"
				}
				t.Run(name, func(t *testing.T) {
					header := "Worked for 3m 30s · done 5:41 PM"
					want := StateIdle
					if working {
						header = "• Working (59s • esc to interrupt)"
						want = StateWorking
					}
					screen := header + "\n\n⠁   ⠈         ⠄\n" + prompt + separator +
						"Ask Codex to do anything    ⠈\n  ⠠⢀      ⠄\n  gpt-6-astra ultra · ~/project"
					got, err := detector.Detect(KindCodex, DetectionInput{Screen: screen})
					if err != nil || got.State != want {
						t.Fatalf("Detect() = %+v, %v; want %s", got, err, want)
					}
				})
			}
		}
	}
}

func TestCodexLiveActivitySurvivesLayoutAndInterruptHintChanges(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatalf("LoadEmbeddedDetector() errors = %v", loadErrors)
	}
	for _, activity := range []string{
		"• Working (1m 32s • esc to i…",
		"• Working (1m 32s • ctrl+c to interrupt)",
		"• Working (1m 32s)",
		"Working (1m 32s)",
		"◦ Reading source files (12s • esc to interrupt)",
		"• Working (1m 32s • esc to\n  interrupt)",
		"• Working (1m\n  32s • esc to interrupt)",
		"• Waiting for background terminal (2m 25s • esc to interrupt) · 1 background terminal running · /ps to view",
		"• Working (25s • esc to interrupt)\n\n• Queued follow-up inputs\n  queued message one\n  queued message two\n  queued message three\n  queued message four\n  queued message five",
		"• Working (25s • esc to interrupt)\n\n• Messages to be submitted after next tool call (press ctrl+c to interrupt and send immediately)\n  queued follow-up",
		"• Working (25s • esc to interrupt)\n\n• Messages to be submitted at end of turn\n  queued follow-up",
		"■ Conversation interrupted\n• Working (3s • esc to interrupt)",
	} {
		t.Run(activity, func(t *testing.T) {
			screen := "› previous request\n• Previous response\n\n" + activity + "\n\n⠁     ⠈\n»⠁Ask Codex to do anything\n  ⠠⢀\n  model footer"
			got, err := detector.Detect(KindCodex, DetectionInput{Screen: screen})
			if err != nil || got.State != StateWorking || got.RuleID != "screen_working_fallback" || !got.VisibleWorking {
				t.Fatalf("Detect() = %+v, %v, want visible working from current activity", got, err)
			}
		})
	}
}

func TestCodexHistoricalSignalsDoNotOverrideCurrentIdlePrompt(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatalf("LoadEmbeddedDetector() errors = %v", loadErrors)
	}
	for _, history := range []string{
		"• Working (12s • esc to interrupt)\n• Finished the change.",
		"• Working (12s)\n✓ Finished",
		"• Working (12s)\n✗ Failed",
		"• Working (12s)\n■ Conversation interrupted",
		"• Reconnect failed — check the endpoint, then relaunch (12s)",
		"allow command?\npress enter to confirm or esc to cancel\n• Done",
		"Do you want to allow this? [y/n]\n• Done",
	} {
		t.Run(history, func(t *testing.T) {
			screen := history + "\n»⠁Ask Codex to do anything\n  model footer"
			got, err := detector.Detect(KindCodex, DetectionInput{Screen: screen})
			if err != nil || got.State != StateIdle || !got.VisibleIdle {
				t.Fatalf("Detect() = %+v, %v, want visible idle despite historical signals", got, err)
			}
		})
	}
	got, err := detector.Detect(KindCodex, DetectionInput{Screen: "»⠁Do you want to allow command? [y/n]\n  model footer"})
	if err != nil || got.State != StateIdle {
		t.Fatalf("prompt contents misclassified as blocker: %+v, %v", got, err)
	}
}

func TestCodexLiveBlockersAndTranscriptViewerAfterAnimatedPrompt(t *testing.T) {
	detector, loadErrors := LoadEmbeddedDetector()
	if len(loadErrors) != 0 {
		t.Fatalf("LoadEmbeddedDetector() errors = %v", loadErrors)
	}
	for _, test := range []struct {
		name   string
		screen string
		state  State
		rule   string
		skip   bool
	}{
		{"approval", "press enter to confirm or esc to cancel", StateBlocked, "live_strong_blocker", false},
		{"question", "enter to submit all", StateBlocked, "live_strong_blocker", false},
		{"legacy approval", "Do you want to continue? [y/n]", StateBlocked, "weak_blocker", false},
		{"viewer", "↑/↓ to scroll · pgup/pgdn to page · home/end to jump\nq to quit · esc/← to edit prev", "", "transcript_viewer", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := detector.Detect(KindCodex, DetectionInput{Screen: "»⠁previous request\n" + test.screen})
			if err != nil || got.State != test.state || got.RuleID != test.rule || got.SkipStateUpdate != test.skip {
				t.Fatalf("Detect() = %+v, %v, want %s/%s skip:%t", got, err, test.state, test.rule, test.skip)
			}
		})
	}
}
