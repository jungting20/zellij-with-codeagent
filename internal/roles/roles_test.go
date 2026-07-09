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
	if len(spec.Arguments) != 1 || spec.Arguments[0].Name != "path" || !spec.Arguments[0].Required {
		t.Fatalf("Lookup(%q) arguments = %#v, want required path argument", RoleCodingAgent, spec.Arguments)
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
