package console

import "testing"

func TestRunRequiresURLFlag(t *testing.T) {
	if code := Run(nil); code == 0 {
		t.Fatalf("Run(nil) = %d, want non-zero", code)
	}
}
