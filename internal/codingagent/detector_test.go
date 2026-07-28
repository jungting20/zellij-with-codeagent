package codingagent

import (
	"strings"
	"testing"
)

const minimalManifestYAML = `
version: 1
agent: codex
rules:
  - id: blocked
    priority: 100
    state: blocked
    region:
      type: bottom_non_empty_lines
      lines: 3
    match:
      all:
        - contains: ["allow command?"]
        - not:
            - contains: ["conversation interrupted"]
    visible_blocker: true
`

func TestLoadManifestParsesTypedRules(t *testing.T) {
	manifest, err := LoadManifest([]byte(minimalManifestYAML))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if manifest.Version != 1 || manifest.Agent != KindCodex {
		t.Fatalf("manifest identity = (%d, %q), want (1, %q)", manifest.Version, manifest.Agent, KindCodex)
	}
	if len(manifest.Rules) != 1 {
		t.Fatalf("len(Rules) = %d, want 1", len(manifest.Rules))
	}
	rule := manifest.Rules[0]
	if rule.ID != "blocked" || rule.Priority != 100 || rule.State != StateBlocked {
		t.Fatalf("rule identity = (%q, %d, %q), want (blocked, 100, blocked)", rule.ID, rule.Priority, rule.State)
	}
	if rule.Region.Type != RegionBottomNonEmptyLines || rule.Region.Lines != 3 {
		t.Fatalf("rule region = (%q, %d), want (bottom_non_empty_lines, 3)", rule.Region.Type, rule.Region.Lines)
	}
	if !rule.VisibleBlocker || rule.VisibleIdle || rule.VisibleWorking || rule.SkipStateUpdate {
		t.Fatalf("rule flags = idle:%t working:%t blocker:%t skip:%t", rule.VisibleIdle, rule.VisibleWorking, rule.VisibleBlocker, rule.SkipStateUpdate)
	}
	if rule.Order != 0 {
		t.Fatalf("rule order = %d, want 0", rule.Order)
	}
}

func TestLoadManifestRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "unknown state",
			yaml:    strings.Replace(minimalManifestYAML, "state: blocked", "state: sleeping", 1),
			wantErr: "state",
		},
		{
			name:    "unknown region",
			yaml:    strings.Replace(minimalManifestYAML, "bottom_non_empty_lines", "middle", 1),
			wantErr: "region",
		},
		{
			name:    "zero bottom lines",
			yaml:    strings.Replace(minimalManifestYAML, "lines: 3", "lines: 0", 1),
			wantErr: "lines",
		},
		{
			name: "duplicate rule id",
			yaml: minimalManifestYAML + `
  - id: blocked
    priority: 1
    state: idle
    region: {type: whole_recent}
    match: {contains: ["ready"]}
`,
			wantErr: "duplicate",
		},
		{
			name:    "matcher without operator",
			yaml:    strings.Replace(minimalManifestYAML, "match:\n      all:\n        - contains: [\"allow command?\"]\n        - not:\n            - contains: [\"conversation interrupted\"]", "match: {}", 1),
			wantErr: "operator",
		},
		{
			name:    "invalid regexp",
			yaml:    strings.Replace(minimalManifestYAML, "all:\n        - contains: [\"allow command?\"]\n        - not:\n            - contains: [\"conversation interrupted\"]", "regex: [\"[\"]", 1),
			wantErr: "regexp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadManifest([]byte(tt.yaml))
			if err == nil {
				t.Fatal("LoadManifest() error = nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
				t.Fatalf("LoadManifest() error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadManifestRejectsEmptyRecursiveMatcherLists(t *testing.T) {
	for _, operator := range []string{"all", "any", "not"} {
		t.Run(operator, func(t *testing.T) {
			yaml := strings.Replace(minimalManifestYAML, "all:\n        - contains: [\"allow command?\"]\n        - not:\n            - contains: [\"conversation interrupted\"]", operator+": []", 1)
			_, err := LoadManifest([]byte(yaml))
			if err == nil {
				t.Fatal("LoadManifest() error = nil")
			}
			want := operator + " matcher must not be empty"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("LoadManifest() error = %q, want substring %q", err, want)
			}
		})
	}
}

func TestRegionBottomNonEmptyLinesPreservesInterveningAndTrailingBlankLines(t *testing.T) {
	input := DetectionInput{Screen: "header\nsecond from bottom\n\nbottom\n\n"}
	region := Region{Type: RegionBottomNonEmptyLines, Lines: 2}
	want := "second from bottom\n\nbottom\n\n"
	if got := selectRegion(region, input); got != want {
		t.Fatalf("selectRegion() = %q, want %q", got, want)
	}
}

func TestRegionAfterLastPromptMarkerUsesLastMarkerOrFullScreen(t *testing.T) {
	screen := "history\n›\nold response\n›\nlatest response"
	region := Region{Type: RegionAfterLastPromptMarker}
	if got, want := selectRegion(region, DetectionInput{Screen: screen}), "\nlatest response"; got != want {
		t.Fatalf("selectRegion(with marker) = %q, want %q", got, want)
	}
	if got := selectRegion(region, DetectionInput{Screen: "full screen"}); got != "full screen" {
		t.Fatalf("selectRegion(without marker) = %q, want full screen", got)
	}
}

func TestRegionPromptBoxBodyUsesUpperBorderAndNextHorizontalRule(t *testing.T) {
	screen := "history\n╭──────╮\n│ ❯ hello\n│ second line\n╰──────╯\nstatus"
	region := Region{Type: RegionPromptBoxBody}
	want := "│ ❯ hello\n│ second line"
	if got := selectRegion(region, DetectionInput{Screen: screen}); got != want {
		t.Fatalf("selectRegion() = %q, want %q", got, want)
	}
}

func TestRegionAfterLastHorizontalRuleUsesLastRule(t *testing.T) {
	screen := "old\n────\nmiddle\n━━━━\nlatest"
	region := Region{Type: RegionAfterLastHorizontalRule}
	if got, want := selectRegion(region, DetectionInput{Screen: screen}), "latest"; got != want {
		t.Fatalf("selectRegion() = %q, want %q", got, want)
	}
}

func TestRegionOSCInputsAreIsolated(t *testing.T) {
	input := DetectionInput{Screen: "screen", OSCTitle: "title", OSCProgress: "progress"}
	if got := selectRegion(Region{Type: RegionOSCTitle}, input); got != "title" {
		t.Fatalf("OSC title region = %q, want title", got)
	}
	if got := selectRegion(Region{Type: RegionOSCProgress}, input); got != "progress" {
		t.Fatalf("OSC progress region = %q, want progress", got)
	}
}

func TestMatcherContainsIsCaseInsensitiveAndRequiresAllValues(t *testing.T) {
	matcher := mustLoadTestMatcher(t, `contains: ["allow command?", "press enter"]`)
	if !matcher.matches("ALLOW COMMAND? then Press Enter") {
		t.Fatal("matches() = false, want true for both case-insensitive values")
	}
	if matcher.matches("allow command?") {
		t.Fatal("matches() = true with one required value missing")
	}
}

func TestMatcherRegexAppliesEveryExpressionToWholeRegion(t *testing.T) {
	matcher := mustLoadTestMatcher(t, `regex: ["(?s)^start.*end$", "middle"]`)
	if !matcher.matches("start\nmiddle\nend") {
		t.Fatal("matches() = false, want true when every regexp matches the full region")
	}
	if matcher.matches("start\nother\nend") {
		t.Fatal("matches() = true with one required regexp missing")
	}
}

func TestMatcherLineRegexMatchesEachExpressionAgainstAtLeastOneLine(t *testing.T) {
	matcher := mustLoadTestMatcher(t, `line_regex: ["^first$", "^third$"]`)
	if !matcher.matches("first\nsecond\nthird") {
		t.Fatal("matches() = false, want true when expressions match separate lines")
	}
	if matcher.matches("first\nsecond\nnot third") {
		t.Fatal("matches() = true with no line matching the second expression")
	}
}

func TestMatcherRecursiveAllAnyAndNot(t *testing.T) {
	matcher := mustLoadTestMatcher(t, `
all:
  - contains: ["required"]
  - any:
      - contains: ["choice-a"]
      - contains: ["choice-b"]
  - not:
      - contains: ["forbidden-a"]
      - contains: ["forbidden-b"]
`)
	if !matcher.matches("required choice-b") {
		t.Fatal("matches() = false, want recursive all/any/not match")
	}
	for _, region := range []string{"choice-b", "required", "required choice-a forbidden-b"} {
		if matcher.matches(region) {
			t.Fatalf("matches(%q) = true, want false", region)
		}
	}
}

func TestDetectorRejectsManifestAgentMismatch(t *testing.T) {
	manifest := mustLoadTestManifest(t, minimalManifestYAML)
	_, err := NewDetector(map[Kind]Manifest{KindClaude: manifest})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("NewDetector() error = %v, want agent mismatch", err)
	}
}

func TestDetectorChoosesPriorityThenDeclarationOrderAndReturnsFlags(t *testing.T) {
	manifest := mustLoadTestManifest(t, `
version: 1
agent: codex
rules:
  - id: low
    priority: 10
    state: working
    region: {type: whole_recent}
    match: {contains: ["signal"]}
    visible_working: true
  - id: first-high
    priority: 100
    state: blocked
    region: {type: whole_recent}
    match: {contains: ["signal"]}
    visible_blocker: true
  - id: second-high
    priority: 100
    state: idle
    region: {type: whole_recent}
    match: {contains: ["signal"]}
    visible_idle: true
`)
	detector, err := NewDetector(map[Kind]Manifest{KindCodex: manifest})
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}
	detection, err := detector.Detect(KindCodex, DetectionInput{Screen: "signal"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detection.State != StateBlocked || detection.RuleID != "first-high" {
		t.Fatalf("detection = state:%q rule:%q, want blocked first-high", detection.State, detection.RuleID)
	}
	if !detection.VisibleBlocker || detection.VisibleIdle || detection.VisibleWorking || detection.SkipStateUpdate || detection.Fallback {
		t.Fatalf("detection flags = idle:%t working:%t blocker:%t skip:%t fallback:%t", detection.VisibleIdle, detection.VisibleWorking, detection.VisibleBlocker, detection.SkipStateUpdate, detection.Fallback)
	}
}

func TestDetectorSkipStateUpdateDoesNotFabricateState(t *testing.T) {
	manifest := mustLoadTestManifest(t, `
version: 1
agent: codex
rules:
  - id: viewer
    priority: 100
    state: unknown
    region: {type: whole_recent}
    match: {contains: ["viewer"]}
    skip_state_update: true
`)
	detector, err := NewDetector(map[Kind]Manifest{KindCodex: manifest})
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}
	detection, err := detector.Detect(KindCodex, DetectionInput{Screen: "viewer"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !detection.SkipStateUpdate || detection.State != "" || detection.RuleID != "viewer" || detection.Fallback {
		t.Fatalf("detection = state:%q rule:%q skip:%t fallback:%t, want empty/viewer/true/false", detection.State, detection.RuleID, detection.SkipStateUpdate, detection.Fallback)
	}
}

func TestDetectorFallsBackToIdleForKnownKind(t *testing.T) {
	manifest := mustLoadTestManifest(t, minimalManifestYAML)
	detector, err := NewDetector(map[Kind]Manifest{KindCodex: manifest})
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}
	detection, err := detector.Detect(KindCodex, DetectionInput{Screen: "nothing matches"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detection.State != StateIdle || !detection.Fallback || detection.Reason != "default_known_agent_idle_fallback" || detection.RuleID != "" {
		t.Fatalf("fallback = state:%q fallback:%t reason:%q rule:%q", detection.State, detection.Fallback, detection.Reason, detection.RuleID)
	}
	if _, err := detector.Detect(KindClaude, DetectionInput{}); err == nil {
		t.Fatal("Detect(unconfigured kind) error = nil")
	}
}

func mustLoadTestManifest(t *testing.T, source string) Manifest {
	t.Helper()
	manifest, err := LoadManifest([]byte(source))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	return manifest
}

func mustLoadTestMatcher(t *testing.T, matcherYAML string) Matcher {
	t.Helper()
	source := `
version: 1
agent: codex
rules:
  - id: test
    priority: 1
    state: idle
    region: {type: whole_recent}
    match:
` + indentYAML(matcherYAML, 6)
	return mustLoadTestManifest(t, source).Rules[0].Matcher
}

func indentYAML(source string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.Trim(source, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
