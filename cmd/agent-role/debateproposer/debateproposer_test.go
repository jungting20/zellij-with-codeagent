package debateproposer

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

const expectedSystemPrompt = `당신은 토론의 제안자이자 탐색자다.

주어진 문제를 독립적으로 분석하고 실행 가능한 해결안을 제시하라.

요구사항:

1. 문제의 목표와 제약조건을 먼저 정리한다.
2. 숨겨진 전제나 불확실한 조건을 식별한다.
3. 서로 성격이 다른 해결안 2~3개를 제시한다.
4. 각 해결안의 장점, 단점, 비용, 위험을 비교한다.
5. 마지막에는 가장 추천하는 하나의 초안을 선택한다.
6. 근거가 부족한 내용은 사실처럼 단정하지 말고 가정으로 표시한다.

다른 에이전트가 반박할 수 있도록 판단 근거와 취약점을 숨기지 말라.

출력 형식:

* 문제 정의
* 주요 전제
* 후보안
* 비교
* 최초 권고안
* 불확실한 부분

간결한 출력 규칙:

* 전체 출력은 2,000자 이내로 작성한다.
* 후보안은 2~3개, 각 후보안은 최대 2개 항목으로 제한한다.
* 구체적인 근거는 전체 최대 5개만 남긴다.
* 주제 반복, 도구 로그, 탐색 과정, 긴 파일 목록은 생략한다.
* 위 출력 형식의 6개 섹션은 모두 유지한다.`

func TestRoleConfigSetsContentLimit(t *testing.T) {
	cfg := roleConfig(agyProvider{})
	if cfg.MaxContentChars != 2000 {
		t.Fatalf("MaxContentChars = %d, want 2000", cfg.MaxContentChars)
	}
}

func TestRunInvokesAgyAndRendersJSON(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "agy"), `#!/bin/sh
pwd > "$TEST_CWD_FILE"
printf '%s\n' "$@" > "$TEST_ARGS_FILE"
printf 'proposer answer\n'
`)
	t.Setenv("PATH", binDir)
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TEST_CWD_FILE", cwdFile)
	t.Setenv("TEST_ARGS_FILE", argsFile)
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{"--output-format", "json", repo, "test problem"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runWithIO() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	gotCWD := strings.TrimSpace(readFile(t, cwdFile))
	if gotCWD != repo {
		t.Errorf("agy cwd = %q, want %q", gotCWD, repo)
	}
	wantArgs := []string{"--new-project", "--mode", "plan", "--print", debaterole.ComposePrompt(expectedSystemPrompt, repositoryInputForTest(repo, "test problem"))}
	gotArgs := strings.SplitN(strings.TrimSuffix(readFile(t, argsFile), "\n"), "\n", len(wantArgs))
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("agy args = %#v, want %#v", gotArgs, wantArgs)
	}
	var gotJSON map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &gotJSON); err != nil {
		t.Fatalf("decode stdout JSON: %v; stdout = %q", err, stdout.String())
	}
	wantJSON := map[string]any{
		"schema_version": "debate-role/v1",
		"role":           "debate-proposer",
		"engine":         "agy",
		"status":         "success",
		"content":        "proposer answer",
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Errorf("JSON = %#v, want %#v", gotJSON, wantJSON)
	}
}

func TestRunReportsMissingAgy(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "test problem"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 1 {
		t.Errorf("runWithIO() code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "agy executable not found on PATH") {
		t.Errorf("stderr = %q, want missing agy diagnostic", stderr.String())
	}
}

func TestRunPreservesAgyFailure(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "agy"), `#!/bin/sh
printf 'provider diagnostic\n' >&2
exit 9
`)
	t.Setenv("PATH", binDir)
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "test problem"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 9 {
		t.Errorf("runWithIO() code = %d, want 9", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	for _, want := range []string{"agy failed", "provider diagnostic"} {
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

func repositoryInputForTest(repository, input string) string {
	return "<<<TARGET_REPOSITORY_BEGIN>>>\n" + repository +
		"\n<<<TARGET_REPOSITORY_END>>>\n\n" +
		"Analyze only the target repository above. Do not reuse files or context from another project.\n\n" +
		"<<<USER_INPUT_BEGIN>>>\n" + input + "\n<<<USER_INPUT_END>>>"
}
