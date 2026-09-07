package work

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAutoTestCommandPreservesArgumentsAndDisplay(t *testing.T) {
	for _, command := range [][]string{
		{"printf", "%s\\n", "it's fine"},
		{"printf", "%s\\n", "$(printf SUBSTITUTED) `printf SUBSTITUTED` $HOME %s 한글"},
		{"printf", "quoted' format %%s\\n"},
		{"sh", "-c", "exit 7"},
	} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Compare the generated shell's output with direct argv execution.
			direct, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
			status := "0"
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 7 {
					status = "7"
				} else {
					t.Fatalf("direct command: %v", err)
				}
			}
			plan, err := BuildPlan(PlanRequest{
				Goal: "test quoting", CWD: t.TempDir(), AutoTest: true,
				Project: ProjectDetection{FeedbackEnabled: true, TestCommand: command},
			})
			if err != nil {
				t.Fatal(err)
			}
			args := plan.Tabs[0].Panes[1].Command
			output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("run test pane: %v; output=%s", err, output)
			}
			label := command
			if len(label) > 2 {
				label = label[:2]
			}
			want := "$ " + strings.Join(command, " ") + "\n" + string(direct) +
				strings.Join(label, " ") + " finished with exit=" + status + "\n"
			if string(output) != want {
				t.Fatalf("output=%q, want %q", output, want)
			}
		})
	}
}
