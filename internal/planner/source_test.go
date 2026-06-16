package planner

import (
	"encoding/json"
	"testing"
)

func TestResolveSourceResultJSONUsesPlannerInternalSourcePath(t *testing.T) {
	result := ResolveSourceResult{
		URL:        "http://localhost:8000/example/aa",
		SourcePath: "/tmp/app/src/pages/example/aa.tsx",
		CWD:        "/tmp/app",
		Reason:     "provided by mock resolver",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["source_path"] != result.SourcePath {
		t.Fatalf("source_path = %q, want %q", decoded["source_path"], result.SourcePath)
	}
	if _, ok := decoded["resolved_source"]; ok {
		t.Fatalf("resolved_source legacy field present in %s", data)
	}
}
