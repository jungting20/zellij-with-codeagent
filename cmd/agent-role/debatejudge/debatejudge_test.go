package debatejudge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"zellij-with-codeagent/internal/debaterole"
)

const expectedSystemPrompt = `당신은 토론의 심판이자 최종 설계자다.

제안자의 최초안과 비판자의 반박을 독립적으로 평가하여 최종안을 작성하라.

중요한 원칙:

1. 다수결이나 표현의 자신감으로 판단하지 않는다.
2. 구체적인 근거, 논리적 일관성, 실행 가능성을 기준으로 판단한다.
3. 제안자와 비판자 모두 틀릴 수 있다고 가정한다.
4. 사실, 추론, 가정, 의견을 구분한다.
5. 비판자의 지적을 무조건 반영하지 않는다.
6. 핵심 정보가 부족하더라도 가능한 범위에서 조건부 결론을 내린다.
7. 단순히 두 답변을 요약하지 말고 개선된 최종안을 새로 작성한다.

평가 기준:

* 문제 적합성
* 근거의 품질
* 논리적 일관성
* 실행 가능성
* 비용과 복잡도
* 실패 위험
* 확장성과 유지보수성

출력 형식:

1. 핵심 쟁점
2. 제안자 주장 평가
3. 비판자 주장 평가
4. 채택한 주장과 기각한 주장
5. 최종 권고안
6. 실행 단계
7. 남아 있는 위험과 검증 방법
8. 결론의 신뢰도: 높음 / 중간 / 낮음`

func TestRunInvokesCodexWithPromptOnStdinAndRendersJSON(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "codex"), `#!/bin/sh
pwd > "$TEST_CWD_FILE"
printf '%s\n' "$@" > "$TEST_ARGS_FILE"
while IFS= read -r line; do
  printf '%s\n' "$line"
done > "$TEST_STDIN_FILE"
printf 'judge answer\n'
`)
	t.Setenv("PATH", binDir)
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	argsFile := filepath.Join(t.TempDir(), "args")
	stdinFile := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("TEST_CWD_FILE", cwdFile)
	t.Setenv("TEST_ARGS_FILE", argsFile)
	t.Setenv("TEST_STDIN_FILE", stdinFile)
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{"--output-format", "json", repo}, strings.NewReader("proposal and critique\n"), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runWithIO() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if got := strings.TrimSpace(readFile(t, cwdFile)); got != repo {
		t.Errorf("codex cwd = %q, want %q", got, repo)
	}
	wantArgs := []string{"exec", "--sandbox", "read-only", "--ask-for-approval", "never", "--cd", repo, "-"}
	gotArgs := strings.Split(strings.TrimSuffix(readFile(t, argsFile), "\n"), "\n")
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("codex args = %#v, want %#v", gotArgs, wantArgs)
	}
	wantStdin := debaterole.ComposePrompt(expectedSystemPrompt, "proposal and critique")
	if got := readFile(t, stdinFile); got != wantStdin {
		t.Errorf("codex stdin = %q, want %q", got, wantStdin)
	}
	var gotJSON map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &gotJSON); err != nil {
		t.Fatalf("decode stdout JSON: %v; stdout = %q", err, stdout.String())
	}
	wantJSON := map[string]any{
		"schema_version": "debate-role/v1",
		"role":           "debate-judge",
		"engine":         "codex",
		"status":         "success",
		"content":        "judge answer",
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Errorf("JSON = %#v, want %#v", gotJSON, wantJSON)
	}
}

func TestRunReportsMissingCodex(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "proposal and critique"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 1 {
		t.Errorf("runWithIO() code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "codex executable not found on PATH") {
		t.Errorf("stderr = %q, want missing codex diagnostic", stderr.String())
	}
}

func TestRunPreservesCodexFailure(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "codex"), `#!/bin/sh
printf 'provider diagnostic\n' >&2
exit 11
`)
	t.Setenv("PATH", binDir)
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "proposal and critique"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 11 {
		t.Errorf("runWithIO() code = %d, want 11", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	for _, want := range []string{"codex failed", "provider diagnostic"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want substring %q", stderr.String(), want)
		}
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newTestRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
