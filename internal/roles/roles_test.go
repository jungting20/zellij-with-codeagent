package roles

import "testing"

func TestAllIncludesRoleDescriptions(t *testing.T) {
	for _, name := range []string{
		RoleCoder,
		RoleEditor,
		RoleLSP,
		RoleNetworkTracker,
		RoleConsoleTracker,
		RoleTabNetwork,
		RoleCodingAgent,
	} {
		spec, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) not found", name)
		}
		if spec.Usage == "" || spec.Description == "" {
			t.Fatalf("Lookup(%q) = %#v, want usage and description", name, spec)
		}
	}

	spec, ok := Lookup(RoleCodingAgent)
	if !ok {
		t.Fatalf("Lookup(%q) not found", RoleCodingAgent)
	}
	if spec.Usage != "coding-agent [--agent kind] [--yolo] <path> [-- agent-args...]" {
		t.Fatalf("Lookup(%q) usage = %q, want generalized coding-agent usage", RoleCodingAgent, spec.Usage)
	}
	if len(spec.Arguments) != 4 || spec.Arguments[0].Name != "path" || !spec.Arguments[0].Required || spec.Arguments[1].Name != "--agent" || spec.Arguments[1].Required || spec.Arguments[2].Name != "--yolo" || spec.Arguments[2].Required || spec.Arguments[3].Name != "agent-args" || spec.Arguments[3].Required {
		t.Fatalf("Lookup(%q) arguments = %#v, want path plus optional agent options", RoleCodingAgent, spec.Arguments)
	}
}

func TestLookupTabNetwork(t *testing.T) {
	spec, ok := Lookup(RoleTabNetwork)
	if !ok {
		t.Fatal("Lookup(RoleTabNetwork) ok = false, want true")
	}
	if spec.Name != "tab-network" {
		t.Fatalf("name = %q, want tab-network", spec.Name)
	}
	if spec.Usage != "tab-network [options]" {
		t.Fatalf("usage = %q, want tab-network [options]", spec.Usage)
	}
	want := []string{"--socket", "--role-bin", "--session", "--spawn-on-new-tab", "--no-spawn-on-new-tab"}
	for _, name := range want {
		if !hasArgument(spec.Arguments, name) {
			t.Fatalf("arguments = %#v, missing %s", spec.Arguments, name)
		}
	}
}

func TestLookupTabWatcher(t *testing.T) {
	spec, ok := Lookup(RoleTabWatcher)
	if !ok {
		t.Fatal("Lookup(RoleTabWatcher) ok = false, want true")
	}
	if spec.Name != "tab-watcher" {
		t.Fatalf("name = %q, want tab-watcher", spec.Name)
	}
	if spec.Usage != "tab-watcher [options]" {
		t.Fatalf("usage = %q, want tab-watcher [options]", spec.Usage)
	}
	want := []string{"--port", "--socket", "--cwd", "--session", "--role-bin", "--chrome-path", "--user-data-dir", "--no-launch", "--poll-interval"}
	for _, name := range want {
		if !hasArgument(spec.Arguments, name) {
			t.Fatalf("arguments = %#v, missing %s", spec.Arguments, name)
		}
	}
}

func hasArgument(args []ArgumentSpec, name string) bool {
	for _, arg := range args {
		if arg.Name == name {
			return true
		}
	}
	return false
}

func TestAllReturnsCopy(t *testing.T) {
	first := All()
	first[0].Name = "mutated"
	first[1].Arguments[0].Name = "mutated"

	second := All()
	if second[0].Name == "mutated" {
		t.Fatal("All returned mutable backing storage")
	}
	if second[1].Arguments[0].Name == "mutated" {
		t.Fatal("All returned mutable argument backing storage")
	}
}

func TestLookupDebateCoordinator(t *testing.T) {
	spec, ok := Lookup(RoleDebateCoordinator)
	if !ok {
		t.Fatal("Lookup(RoleDebateCoordinator) ok = false, want true")
	}
	if spec.Name != "debate-coordinator" {
		t.Fatalf("name = %q, want debate-coordinator", spec.Name)
	}
	if spec.Usage != "debate-coordinator <path>" {
		t.Fatalf("usage = %q, want debate-coordinator <path>", spec.Usage)
	}
	if len(spec.Arguments) != 1 || spec.Arguments[0].Name != "path" || !spec.Arguments[0].Required {
		t.Fatalf("arguments = %#v, want required path", spec.Arguments)
	}
}

func TestLookupDebateProposerCriticJudge(t *testing.T) {
	tests := []struct{ name, usage string }{
		{RoleDebateProposer, "debate-proposer [options] <path> [prompt...]"},
		{RoleDebateCritic, "debate-critic [options] <path> [prompt...]"},
		{RoleDebateJudge, "debate-judge [options] <path> [prompt...]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := Lookup(tt.name)
			if !ok {
				t.Fatalf("Lookup(%q) ok = false, want true", tt.name)
			}
			if spec.Usage != tt.usage {
				t.Errorf("Lookup(%q).Usage = %q, want %q", tt.name, spec.Usage, tt.usage)
			}

			wantArguments := []struct {
				name     string
				required bool
			}{
				{name: "path", required: true},
				{name: "prompt", required: false},
				{name: "--output-format", required: false},
			}
			if len(spec.Arguments) != len(wantArguments) {
				t.Fatalf("Lookup(%q).Arguments = %#v, want %d arguments", tt.name, spec.Arguments, len(wantArguments))
			}
			for i, want := range wantArguments {
				if got := spec.Arguments[i]; got.Name != want.name || got.Required != want.required {
					t.Errorf("Lookup(%q).Arguments[%d] = %#v, want name %q required %t", tt.name, i, got, want.name, want.required)
				}
			}
		})
	}
}

func TestLookupTicketManager(t *testing.T) {
	spec, ok := Lookup(RoleTicketManager)
	if !ok {
		t.Fatal("Lookup(RoleTicketManager) ok = false")
	}
	if spec.Name != "ticket-manager" || spec.Usage != "ticket-manager [options] <path>" || spec.Description == "" {
		t.Fatalf("ticket manager spec = %#v", spec)
	}
	want := map[string]bool{
		"path": true, "--task": true, "--anchor-pane": true,
		"--socket": false, "--zellij-session": false, "--role-bin": false, "--startup-timeout": false,
	}
	if len(spec.Arguments) != len(want) {
		t.Fatalf("arguments = %#v", spec.Arguments)
	}
	for _, arg := range spec.Arguments {
		required, ok := want[arg.Name]
		if !ok || arg.Required != required {
			t.Fatalf("argument = %#v, want required=%t present=%t", arg, required, ok)
		}
	}
}

func TestLookupAgentNext(t *testing.T) {
	spec, ok := Lookup(RoleAgentNext)
	if !ok {
		t.Fatal("Lookup(RoleAgentNext) ok = false, want true")
	}
	if spec.Usage != "agent-next [--socket PATH --timeout DURATION]" {
		t.Fatalf("usage = %q, want agent-next usage", spec.Usage)
	}
	want := []string{"--socket", "--timeout"}
	if len(spec.Arguments) != len(want) {
		t.Fatalf("arguments = %#v, want %d optional arguments", spec.Arguments, len(want))
	}
	for i, name := range want {
		if spec.Arguments[i].Name != name || spec.Arguments[i].Required {
			t.Fatalf("arguments[%d] = %#v, want optional %s", i, spec.Arguments[i], name)
		}
	}
}
