package zellij

import (
	"slices"
	"testing"
)

func TestCreateCommandsPreserveFocusWhenRequested(t *testing.T) {
	for _, noFocus := range []bool{false, true} {
		for _, command := range []CommandSpec{
			createTabCommand("zellij", "session", CreateTabRequest{NoFocus: noFocus, Command: []string{"codex"}}),
			createPaneCommand("zellij", "session", CreatePaneRequest{NoFocus: noFocus, Command: []string{"codex"}}),
		} {
			flag := slices.Index(command.Args, "--no-focus")
			if (flag >= 0) != noFocus || (flag >= 0 && flag > slices.Index(command.Args, "--")) {
				t.Fatalf("noFocus=%v command=%v", noFocus, command.Args)
			}
		}
	}
}
