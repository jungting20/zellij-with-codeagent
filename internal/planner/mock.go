package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrMissingMockSource = errors.New("planner: mock source is required")

type MockSourceResolver struct {
	SourcePath string
}

func (r MockSourceResolver) ResolveSource(_ context.Context, req ResolveSourceRequest) (ResolveSourceResult, error) {
	sourcePath := strings.TrimSpace(r.SourcePath)
	if sourcePath == "" {
		return ResolveSourceResult{}, ErrMissingMockSource
	}
	if strings.TrimSpace(req.URL) == "" {
		return ResolveSourceResult{}, fmt.Errorf("%w: url is required", ErrInvalidResolveSourceRequest)
	}
	return ResolveSourceResult{
		URL:        req.URL,
		SourcePath: sourcePath,
		CWD:        req.CWD,
		Reason:     "provided by --mock-source",
	}, nil
}
