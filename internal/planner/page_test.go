package planner

import (
	"errors"
	"strings"
	"testing"
)

func TestMockSourceResolverReturnsProvidedSource(t *testing.T) {
	resolved, err := (MockSourceResolver{SourcePath: "/tmp/app/src/pages/example/aa.tsx"}).ResolveSource(t.Context(), ResolveSourceRequest{
		URL: "http://localhost:8000/example/aa",
		CWD: "/tmp/app",
	})
	if err != nil {
		t.Fatalf("ResolveSource() error = %v", err)
	}
	if resolved.SourcePath != "/tmp/app/src/pages/example/aa.tsx" || resolved.CWD != "/tmp/app" || resolved.URL != "http://localhost:8000/example/aa" {
		t.Fatalf("resolved = %#v, want mock source result", resolved)
	}
}

func TestMockSourceResolverRequiresSource(t *testing.T) {
	_, err := (MockSourceResolver{}).ResolveSource(t.Context(), ResolveSourceRequest{URL: "http://localhost:8000/example/aa"})
	if !errors.Is(err, ErrMissingMockSource) {
		t.Fatalf("ResolveSource() error = %v, want ErrMissingMockSource", err)
	}
}

func TestBuildPagePlanCreatesFourPagePanes(t *testing.T) {
	payload, err := BuildPagePlan(PagePlanRequest{
		URL:          "http://localhost:8000/example/aa",
		CWD:          "/tmp/app",
		AgentRoleBin: "/tmp/runtime/bin/agent-role",
	}, ResolveSourceResult{
		URL:        "http://localhost:8000/example/aa",
		SourcePath: "/tmp/app/src/pages/example/aa.tsx",
		CWD:        "/tmp/app",
	})
	if err != nil {
		t.Fatalf("BuildPagePlan() error = %v", err)
	}
	if payload.Session != "page-example-aa" || payload.Layout != "triple-horizontal" {
		t.Fatalf("payload header = %#v, want page-example-aa triple-horizontal", payload)
	}
	if len(payload.Tabs) != 1 || len(payload.Tabs[0].Panes) != 4 {
		t.Fatalf("tabs = %#v, want one tab with four panes", payload.Tabs)
	}
	panes := payload.Tabs[0].Panes
	if panes[0].ID != "page-editor" || panes[0].Command[2] != "/tmp/app/src/pages/example/aa.tsx" {
		t.Fatalf("editor pane = %#v, want source path", panes[0])
	}
	if panes[1].ID != "page-lsp" || panes[1].Command[0] != "sh" || panes[1].Command[1] != "-lc" {
		t.Fatalf("lsp pane = %#v, want shell wrapped lsp command", panes[1])
	}
	if panes[2].ID != "page-network" || panes[2].Command[3] != "http://localhost:8000/example/aa" {
		t.Fatalf("network pane = %#v, want target URL", panes[2])
	}
	if panes[3].ID != "page-console" || panes[3].Command[3] != "http://localhost:8000/example/aa" {
		t.Fatalf("console pane = %#v, want target URL", panes[3])
	}
}

func TestBuildPagePlanUsesRoleCommandPrefix(t *testing.T) {
	payload, err := BuildPagePlan(PagePlanRequest{
		URL:              "http://localhost:8000/example/aa",
		CWD:              "/tmp/app",
		AgentRoleCommand: []string{"/tmp/bin/zellij-agent", "role"},
	}, ResolveSourceResult{
		URL:        "http://localhost:8000/example/aa",
		SourcePath: "/tmp/app/src/pages/example/aa.tsx",
		CWD:        "/tmp/app",
	})
	if err != nil {
		t.Fatalf("BuildPagePlan() error = %v", err)
	}

	panes := payload.Tabs[0].Panes
	if got := panes[0].Command; len(got) != 4 || got[0] != "/tmp/bin/zellij-agent" || got[1] != "role" || got[2] != "editor" {
		t.Fatalf("editor command = %#v, want zellij-agent role editor prefix", got)
	}
	if got := panes[1].Command[2]; !strings.Contains(got, "'/tmp/bin/zellij-agent' 'role' lsp") {
		t.Fatalf("lsp command = %q, want zellij-agent role lsp prefix", got)
	}
}

func TestSessionFromURL(t *testing.T) {
	if got := SessionFromURL("http://localhost:8000/example/aa"); got != "page-example-aa" {
		t.Fatalf("SessionFromURL() = %q, want page-example-aa", got)
	}
	if got := SessionFromURL("http://localhost:8000/"); got != "page-root" {
		t.Fatalf("SessionFromURL(root) = %q, want page-root", got)
	}
}
