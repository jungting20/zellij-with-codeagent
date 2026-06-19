package debatecoordinator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSynthesisBlockParsesMarkerAndPrompt(t *testing.T) {
	input := strings.NewReader(`noise before
<<<DEBATE_SYNTHESIS_BEGIN>>>
Completion-Marker: <<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>
Topic: marker design

[debate-a]
answer from a
<<<DEBATE_SYNTHESIS_END>>>
noise after
`)

	block, err := readSynthesisBlock(input)
	if err != nil {
		t.Fatalf("readSynthesisBlock() error = %v", err)
	}
	if block.CompletionMarker != "<<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>" {
		t.Fatalf("marker = %q", block.CompletionMarker)
	}
	if !strings.Contains(block.Prompt, "Topic: marker design") || !strings.Contains(block.Prompt, "answer from a") {
		t.Fatalf("prompt = %q, want topic and answer", block.Prompt)
	}
	if strings.Contains(block.Prompt, "DEBATE_SYNTHESIS_BEGIN") || strings.Contains(block.Prompt, "DEBATE_SYNTHESIS_END") {
		t.Fatalf("prompt = %q, wrapper markers should be excluded", block.Prompt)
	}
}

func TestReadSynthesisBlockRequiresCompletionMarker(t *testing.T) {
	input := strings.NewReader("<<<DEBATE_SYNTHESIS_BEGIN>>>\nTopic: no marker\n<<<DEBATE_SYNTHESIS_END>>>\n")

	_, err := readSynthesisBlock(input)
	if err == nil || !strings.Contains(err.Error(), "completion marker") {
		t.Fatalf("error = %v, want completion marker error", err)
	}
}

func TestReadSynthesisBlockParsesBase64Marker(t *testing.T) {
	input := strings.NewReader(`<<<DEBATE_SYNTHESIS_BEGIN>>>
Completion-Marker-Base64: PDw8QUdFTlRfREVCQVRFX0RPTkUgZGViYXRlPWRlYmF0ZV8xIHJvdW5kPTEgYWdlbnQ9Y29vcmRpbmF0b3IgdG9rZW49YWJjPj4+
Topic: encoded marker
<<<DEBATE_SYNTHESIS_END>>>
`)

	block, err := readSynthesisBlock(input)
	if err != nil {
		t.Fatalf("readSynthesisBlock() error = %v", err)
	}
	if block.CompletionMarker != "<<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>" {
		t.Fatalf("marker = %q, want decoded completion marker", block.CompletionMarker)
	}
}

func TestPrepareCodexCommandUsesExecWithPromptOnStdin(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	cmd, err := prepareCodexCommand(repo, "summarize this")
	if err != nil {
		t.Fatalf("prepareCodexCommand() error = %v", err)
	}
	if cmd.Path != codexPath {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, codexPath)
	}
	if strings.Join(cmd.Args[1:], " ") != "exec --cd "+repo+" -" {
		t.Fatalf("cmd.Args = %#v, want codex exec --cd repo -", cmd.Args)
	}
	if cmd.Dir != repo {
		t.Fatalf("cmd.Dir = %q, want repo", cmd.Dir)
	}
}

func TestRunWithIOExecutesCodexAfterBlockAndPrintsMarker(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	promptPath := filepath.Join(t.TempDir(), "debate-coordinator-prompt.txt")
	script := "#!/bin/sh\nprintf 'fake codex synthesis\\n'\n/bin/cat >" + promptPath + "\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
	t.Setenv("PATH", binDir)

	input := strings.NewReader(`<<<DEBATE_SYNTHESIS_BEGIN>>>
Completion-Marker: <<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>
Topic: fake
<<<DEBATE_SYNTHESIS_END>>>
`)
	var stdout, stderr strings.Builder

	code := runWithIO([]string{repo}, input, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runWithIO() code = %d, stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "debate_coordinator_ready") ||
		!strings.Contains(output, "fake codex synthesis") ||
		!strings.Contains(output, "<<<AGENT_DEBATE_DONE debate=debate_1 round=1 agent=coordinator token=abc>>>") {
		t.Fatalf("stdout = %q, want ready, codex output, and marker", output)
	}
}
