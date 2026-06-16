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
	URL              string
	CWD              string
	Session          string
	AgentRoleBin     string
	AgentRoleCommand []string
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
	agentRoleCommand := normalizeRoleCommand(req.AgentRoleCommand, req.AgentRoleBin)

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
						Command: appendRoleCommand(agentRoleCommand, "editor", sourcePath),
					},
					{
						ID:      "page-lsp",
						Role:    "lsp",
						CWD:     cwd,
						Command: []string{"sh", "-lc", pageLSPCommand(agentRoleCommand, sourcePath)},
					},
					{
						ID:      "page-network",
						Role:    "network-tracker",
						CWD:     cwd,
						Command: appendRoleCommand(agentRoleCommand, "network-tracker", "--url", targetURL),
					},
					{
						ID:      "page-console",
						Role:    "console-tracker",
						CWD:     cwd,
						Command: appendRoleCommand(agentRoleCommand, "console-tracker", "--url", targetURL),
					},
				},
			},
		},
	}, nil
}

func normalizeRoleCommand(command []string, bin string) []string {
	var normalized []string
	for _, part := range command {
		part = strings.TrimSpace(part)
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	if len(normalized) != 0 {
		return normalized
	}
	bin = strings.TrimSpace(bin)
	if bin == "" {
		bin = "agent-role"
	}
	return []string{bin}
}

func appendRoleCommand(command []string, args ...string) []string {
	result := make([]string, 0, len(command)+len(args))
	result = append(result, command...)
	result = append(result, args...)
	return result
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

func pageLSPCommand(agentRoleCommand []string, sourcePath string) string {
	return fmt.Sprintf("%s lsp --max-depth 2 %s; printf '\\n[lsp role finished; shell kept open for inspection]\\n'; exec sh",
		shellJoin(agentRoleCommand),
		shellQuote(sourcePath),
	)
}

func shellJoin(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, shellQuote(part))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
