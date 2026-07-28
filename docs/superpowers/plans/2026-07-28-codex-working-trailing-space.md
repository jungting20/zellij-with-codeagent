# Codex Working Trailing-Space Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Codex agents report `working` when Zellij right-pads the visible `• Working (... esc to interrupt)` line.

**Architecture:** Preserve the runtime snapshot and common matcher semantics. Add a regression test that constructs a right-padded screen from the existing Codex working fixture, then narrowly extend the Codex `screen_working_fallback` regular expression to accept trailing whitespace.

**Tech Stack:** Go 1.26.5, Go `testing`, embedded YAML manifests

## Global Constraints

- Change only the Codex screen working fallback; do not add OSC collection.
- Do not alter runtime snapshots or shared `line_regex` matching behavior.
- Preserve the `Conversation interrupted` exclusion and other agent manifests.
- Use `/Users/hwangjungho/.local/share/mise/installs/go/1.26.5/bin/go` because the shell's Homebrew `go` 1.26.4 conflicts with the configured Go 1.26.5 `GOROOT`.

---

### Task 1: Accept Zellij right-padding in the Codex working rule

**Files:**
- Modify: `internal/codingagent/manifests_test.go`
- Modify: `internal/codingagent/manifests/codex.yaml`

**Interfaces:**
- Consumes: `LoadEmbeddedDetector() (*Detector, map[Kind]error)` and `Detector.Detect(Kind, DetectionInput) (Detection, error)`.
- Produces: Existing `screen_working_fallback` behavior extended to right-padded rendered lines; no new public API.

- [ ] **Step 1: Write the failing regression test**

Add a focused test that reads `testdata/codex/working-screen-footer.txt`, appends spaces to every rendered line, detects it as a Codex screen, and requires `StateWorking` with rule `screen_working_fallback`:

```go
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
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
/Users/hwangjungho/.local/share/mise/installs/go/1.26.5/bin/go test ./internal/codingagent -run '^TestCodexWorkingScreenFooterAllowsZellijRightPadding$' -count=1
```

Expected: FAIL because detection returns `idle` with an empty rule instead of `working/screen_working_fallback`.

- [ ] **Step 3: Make the minimal manifest change**

Change the Codex rule expression to allow whitespace after the optional suffix:

```yaml
- line_regex: ['^[•◦][[:space:]]+Working \([^)]*esc to interrupt\)( · .*)?[[:space:]]*$']
```

- [ ] **Step 4: Run focused and package tests and verify GREEN**

Run:

```bash
/Users/hwangjungho/.local/share/mise/installs/go/1.26.5/bin/go test ./internal/codingagent -run '^TestCodexWorkingScreenFooterAllowsZellijRightPadding$' -count=1
/Users/hwangjungho/.local/share/mise/installs/go/1.26.5/bin/go test ./internal/codingagent -count=1
```

Expected: PASS with no failures.

- [ ] **Step 5: Run repository verification**

Run:

```bash
/Users/hwangjungho/.local/share/mise/installs/go/1.26.5/bin/go test ./... -count=1
```

Expected: PASS with no failures.

- [ ] **Step 6: Commit the regression fix**

```bash
git add internal/codingagent/manifests_test.go internal/codingagent/manifests/codex.yaml
git commit -m "fix: Codex working 상태 감지 공백 허용"
```

### Task 2: Build and atomically register the unified CLI

**Files:**
- Build output: `bin/zellij-agent`
- Installed executable: `/Users/hwangjungho/.config/custom-cli/zellij-agent`

**Interfaces:**
- Consumes: `cmd/zellij-agent` unified command entrypoint.
- Produces: Executable custom CLI containing the corrected embedded Codex manifest.

- [ ] **Step 1: Build with the consistent Go toolchain**

```bash
/Users/hwangjungho/.local/share/mise/installs/go/1.26.5/bin/go build -o bin/zellij-agent ./cmd/zellij-agent
```

- [ ] **Step 2: Register atomically on the custom CLI PATH**

```bash
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
```

- [ ] **Step 3: Verify the installed binary**

```bash
cmp bin/zellij-agent ~/.config/custom-cli/zellij-agent
test -x ~/.config/custom-cli/zellij-agent
~/.config/custom-cli/zellij-agent --help
```

Expected: all commands exit zero and help lists the unified command set.
