package debatecritic

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"zellij-with-codeagent/internal/debaterole"
)

const expectedSystemPrompt = `당신은 토론의 비판자이자 레드팀이다.

제안자의 결론을 지지하거나 요약하는 것이 목적이 아니다. 제안이 실제 환경에서 실패할 수 있는 이유를 찾아내는 것이 목적이다.

다음 관점에서 검토하라:

1. 잘못되었거나 검증되지 않은 전제
2. 논리적 비약과 내부 모순
3. 누락된 요구사항과 이해관계자
4. 예외 상황과 실패 시나리오
5. 보안, 비용, 운영, 유지보수 위험
6. 현실적으로 실행하기 어려운 부분
7. 더 단순하거나 효과적인 대안

규칙:

* 반박에는 반드시 이유나 구체적인 반례를 붙인다.
* 표현이나 문체가 아니라 내용과 의사결정을 검토한다.
* 억지로 반대하지 않는다.
* 타당한 부분은 인정하되 검증이 필요한 부분과 구분한다.
* 치명적 문제와 사소한 문제를 분리한다.
* 가능하면 반박에 대응하는 수정안도 제시한다.

출력 형식:

* 제안에서 타당한 부분
* 치명적인 문제
* 중요한 누락
* 실패 시나리오
* 반례
* 수정 제안
* 제안자에게 묻고 싶은 핵심 질문

간결한 출력 규칙:

* 전체 출력은 2,000자 이내로 작성한다.
* 타당한 부분, 치명적인 문제, 중요한 누락, 수정 제안, 핵심 질문은 각각 최대 3개로 제한한다.
* 실패 시나리오와 반례는 각각 최대 2개로 제한한다.
* 의사결정을 바꾸는 문제를 우선하고 제안 원문을 길게 반복하지 않는다.
* 도구 로그, 탐색 과정, 긴 파일 목록은 생략한다.
* 위 출력 형식의 7개 섹션은 모두 유지한다.`

func TestRoleConfigSetsContentLimit(t *testing.T) {
	cfg := roleConfig(agentProvider{})
	if cfg.MaxContentChars != 2000 {
		t.Fatalf("MaxContentChars = %d, want 2000", cfg.MaxContentChars)
	}
}

func TestRunInvokesAgentAndPrintsResult(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "agent"), `#!/bin/sh
pwd > "$TEST_CWD_FILE"
printf '%s\n' "$@" > "$TEST_ARGS_FILE"
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"critic answer"}'
`)
	t.Setenv("PATH", binDir)
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TEST_CWD_FILE", cwdFile)
	t.Setenv("TEST_ARGS_FILE", argsFile)
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "test proposal"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runWithIO() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "critic answer\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "critic answer\n")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if got := strings.TrimSpace(readFile(t, cwdFile)); got != repo {
		t.Errorf("agent cwd = %q, want %q", got, repo)
	}
	wantArgs := []string{"--print", "--mode", "ask", "--output-format", "json", "--trust", "--workspace", repo, debaterole.ComposePrompt(expectedSystemPrompt, repositoryInputForTest(repo, "test proposal"))}
	gotArgs := strings.SplitN(strings.TrimSuffix(readFile(t, argsFile), "\n"), "\n", len(wantArgs))
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("agent args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestRunRejectsInvalidAgentResult(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		wantStderr string
	}{
		{name: "error result", response: `{"type":"result","subtype":"error","is_error":true,"result":"failed"}`, wantStderr: "invalid agent JSON result"},
		{name: "empty result", response: `{"type":"result","subtype":"success","is_error":false,"result":""}`, wantStderr: "invalid agent JSON result"},
		{name: "malformed JSON", response: `not-json`, wantStderr: "decode agent JSON result"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			writeExecutable(t, filepath.Join(binDir, "agent"), "#!/bin/sh\nprintf '%s\\n' '"+tt.response+"'\n")
			t.Setenv("PATH", binDir)
			repo := newTestRepository(t)
			var stdout, stderr bytes.Buffer

			code := runWithIO([]string{repo, "test proposal"}, bytes.NewReader(nil), &stdout, &stderr)

			if code == 0 {
				t.Errorf("runWithIO() code = 0, want non-zero")
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunPreservesAgentDiagnosticForInvalidResult(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "malformed JSON", response: `not-json`},
		{name: "unsuccessful JSON", response: `{"type":"result","subtype":"error","is_error":true,"result":"failed"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			writeExecutable(t, filepath.Join(binDir, "agent"), "#!/bin/sh\nprintf 'provider diagnostic\\n' >&2\nprintf '%s\\n' '"+tt.response+"'\n")
			t.Setenv("PATH", binDir)
			repo := newTestRepository(t)
			var stdout, stderr bytes.Buffer

			code := runWithIO([]string{repo, "test proposal"}, bytes.NewReader(nil), &stdout, &stderr)

			if code == 0 {
				t.Errorf("runWithIO() code = 0, want non-zero")
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), "provider diagnostic") {
				t.Errorf("stderr = %q, want provider diagnostic", stderr.String())
			}
		})
	}
}

func TestRunReportsMissingAgent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "test proposal"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 1 {
		t.Errorf("runWithIO() code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "agent executable not found on PATH") {
		t.Errorf("stderr = %q, want missing agent diagnostic", stderr.String())
	}
}

func TestRunPreservesAgentFailure(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "agent"), `#!/bin/sh
printf 'provider diagnostic\n' >&2
exit 10
`)
	t.Setenv("PATH", binDir)
	repo := newTestRepository(t)
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{repo, "test proposal"}, bytes.NewReader(nil), &stdout, &stderr)

	if code != 10 {
		t.Errorf("runWithIO() code = %d, want 10", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	for _, want := range []string{"agent failed", "provider diagnostic"} {
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
