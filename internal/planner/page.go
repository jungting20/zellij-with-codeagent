package planner

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"zellij-with-codeagent/internal/transport"
)

var ErrInvalidResolveSourceRequest = errors.New("planner: invalid resolve source request")

type PagePlanRequest struct {
	URL          string
	CWD          string
	Session      string
	AgentRoleBin string
}

func BuildPagePlan(req PagePlanRequest, resolved ResolveSourceResult) (transport.ExecutionPlanPayload, error) {
	targetURL := strings.TrimSpace(req.URL)
	if targetURL == "" {
		targetURL = strings.TrimSpace(resolved.URL)
	}
	if targetURL == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: url is required", ErrInvalidResolveSourceRequest)
	}
	sourcePath := strings.TrimSpace(resolved.SourcePath)
	if sourcePath == "" {
		return transport.ExecutionPlanPayload{}, fmt.Errorf("%w: source_path is required", ErrInvalidResolveSourceRequest)
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = strings.TrimSpace(resolved.CWD)
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		session = SessionFromURL(targetURL)
	}
	agentRoleBin := strings.TrimSpace(req.AgentRoleBin)
	if agentRoleBin == "" {
		agentRoleBin = "agent-role"
	}

	return transport.ExecutionPlanPayload{
		Session: session,
		Layout:  "triple-horizontal",
		Tabs: []transport.ExecutionPlanTab{
			{
				Name: session,
				Panes: []transport.ExecutionPlanPane{
					{
						ID:      "page-editor",
						Role:    "editor",
						CWD:     cwd,
						Command: []string{agentRoleBin, "editor", sourcePath},
					},
					{
						ID:      "page-lsp",
						Role:    "lsp",
						CWD:     cwd,
						Command: []string{"sh", "-lc", pageLSPCommand(agentRoleBin, sourcePath)},
					},
					{
						ID:      "page-network",
						Role:    "network-tracker",
						CWD:     cwd,
						Command: []string{agentRoleBin, "network-tracker", "--url", targetURL},
					},
					{
						ID:      "page-console",
						Role:    "console-tracker",
						CWD:     cwd,
						Command: []string{agentRoleBin, "console-tracker", "--url", targetURL},
					},
				},
			},
		},
	}, nil
}

func SessionFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "page"
	}
	cleanPath := path.Clean(parsed.Path)
	if cleanPath == "." || cleanPath == "/" {
		return "page-root"
	}
	trimmed := strings.Trim(cleanPath, "/")
	slug := nonSlugChars.ReplaceAllString(strings.ToLower(trimmed), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "page-root"
	}
	return "page-" + slug
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

func pageLSPCommand(agentRoleBin, sourcePath string) string {
	return fmt.Sprintf("%s lsp --max-depth 2 %s; printf '\\n[lsp role finished; shell kept open for inspection]\\n'; exec sh",
		shellQuote(agentRoleBin),
		shellQuote(sourcePath),
	)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
