package planner

import "context"

// ResolveSourceRequest is planner-internal input for resolving a browser URL
// to the top-level source file that renders it.
type ResolveSourceRequest struct {
	URL  string
	CWD  string
	Goal string
}

// ResolveSourceResult is planner-internal metadata. It is not part of the
// agentd /v1/requests execution_plan payload.
type ResolveSourceResult struct {
	URL        string `json:"url"`
	SourcePath string `json:"source_path"`
	CWD        string `json:"cwd"`
	Reason     string `json:"reason,omitempty"`
}

type SourceResolver interface {
	ResolveSource(context.Context, ResolveSourceRequest) (ResolveSourceResult, error)
}
