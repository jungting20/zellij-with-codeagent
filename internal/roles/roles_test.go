package roles

import "testing"

func TestAllIncludesRoleDescriptions(t *testing.T) {
	for _, name := range []string{
		RoleCoder,
		RoleEditor,
		RoleLSP,
		RoleNetworkTracker,
		RoleConsoleTracker,
	} {
		spec, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) not found", name)
		}
		if spec.Usage == "" || spec.Description == "" {
			t.Fatalf("Lookup(%q) = %#v, want usage and description", name, spec)
		}
	}
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
