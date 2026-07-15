package codereview

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestRunForwardsReviewToDebateBackground(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "fixed topic and rounds",
			args: []string{"--rounds", "2"},
			want: []string{"--topic", ReviewTopic, "--rounds", "2", "--start-codex"},
		},
		{
			name: "additional prompt",
			args: []string{"--prompt", "  Pay extra attention to CLI compatibility.  "},
			want: []string{
				"--topic", ReviewTopic + "\n\nAdditional review prompt:\nPay extra attention to CLI compatibility.",
				"--rounds", "1",
				"--start-codex",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotArgs []string
			var gotStdout, gotStderr bool
			var stdout, stderr bytes.Buffer
			backgroundRun := func(args []string, out, errOut io.Writer) int {
				gotArgs = append([]string(nil), args...)
				gotStdout = out == &stdout
				gotStderr = errOut == &stderr
				return 17
			}

			code := run(tt.args, &stdout, &stderr, BackgroundRun(backgroundRun))

			if code != 17 {
				t.Fatalf("run() exit code = %d, want forwarded 17", code)
			}
			if !reflect.DeepEqual(gotArgs, tt.want) {
				t.Fatalf("background args = %#v, want %#v", gotArgs, tt.want)
			}
			if !gotStdout || !gotStderr {
				t.Fatalf("writers forwarded: stdout=%t stderr=%t, want both true", gotStdout, gotStderr)
			}
		})
	}
}

func TestRunDoesNotInvokeDebateBackgroundForLocalExit(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "help", args: []string{"--help"}, wantCode: 0},
		{name: "unexpected positional", args: []string{"surprise"}, wantCode: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr, func([]string, io.Writer, io.Writer) int {
				called = true
				return 0
			})

			if code != tt.wantCode {
				t.Fatalf("run() exit code = %d, want %d; stdout=%q stderr=%q", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if called {
				t.Fatal("background runner invoked, want local exit")
			}
		})
	}
}

func TestRunHelpShowsReviewOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "Usage: zellij-agent code-review [options]") || !strings.Contains(output, "-rounds int") || !strings.Contains(output, "-prompt string") {
		t.Fatalf("stdout = %q, want code-review usage with prompt and rounds", output)
	}
	if strings.Contains(output, "-topic") || strings.Contains(output, "-start-codex") {
		t.Fatalf("stdout = %q, want only code-review options", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
