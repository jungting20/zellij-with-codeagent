package runtime

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestDefaultExecutionPlanPaneCommandPreservesLiteralID(t *testing.T) {
	for _, id := range []string{
		"ordinary-pane",
		"$(printf SUBSTITUTED)",
		"`printf SUBSTITUTED`",
		"$HOME 'quoted' \"double\" \\path %s 한글",
	} {
		t.Run(id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			args := DefaultExecutionPlanPaneCommand(id)
			output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("run default command: %v; output=%s", err, output)
			}
			if want := "agentd_execution_plan_ready:" + id + "\n"; string(output) != want {
				t.Fatalf("output=%q, want literal marker %q", output, want)
			}
		})
	}
}
