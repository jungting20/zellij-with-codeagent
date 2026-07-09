package roles

type ArgumentSpec struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type RoleSpec struct {
	Name        string         `json:"name"`
	Usage       string         `json:"usage"`
	Description string         `json:"description"`
	Arguments   []ArgumentSpec `json:"arguments,omitempty"`
}

const (
	RoleCoder             = "coder"
	RoleEditor            = "editor"
	RoleLSP               = "lsp"
	RoleNetworkTracker    = "network-tracker"
	RoleConsoleTracker    = "console-tracker"
	RoleTabNetwork        = "tab-network"
	RoleTabWatcher        = "tab-watcher"
	RoleCodingAgent       = "coding-agent"
	RoleDebateCoordinator = "debate-coordinator"
)

var specs = []RoleSpec{
	{
		Name:        RoleCoder,
		Usage:       "coder",
		Description: "Visualizes coding agent status.",
	},
	{
		Name:        RoleEditor,
		Usage:       "editor <file>",
		Description: "Opens a source file in Neovim for interactive inspection or editing.",
		Arguments: []ArgumentSpec{
			{Name: "file", Required: true, Description: "Source file path to open."},
		},
	},
	{
		Name:        RoleLSP,
		Usage:       "lsp [options] <file>",
		Description: "Prints a TypeScript or TSX call and component tree using LSP data.",
		Arguments: []ArgumentSpec{
			{Name: "file", Required: true, Description: "TypeScript or TSX source file to analyze."},
			{Name: "--max-depth", Required: false, Description: "Relative import expansion depth."},
			{Name: "--format", Required: false, Description: "Output format: text or json."},
			{Name: "--trace-lsp", Required: false, Description: "Print raw LSP traffic."},
		},
	},
	{
		Name:        RoleNetworkTracker,
		Usage:       "network-tracker --url <url>",
		Description: "Visualizes network tracking for a specific browser URL.",
		Arguments: []ArgumentSpec{
			{Name: "--url", Required: true, Description: "Target URL to track network activity for."},
		},
	},
	{
		Name:        RoleConsoleTracker,
		Usage:       "console-tracker --url <url>",
		Description: "Visualizes console log tracking for a specific browser URL.",
		Arguments: []ArgumentSpec{
			{Name: "--url", Required: true, Description: "Target URL to track console logs for."},
		},
	},
	{
		Name:        RoleTabNetwork,
		Usage:       "tab-network [options]",
		Description: "Tracks Chrome tabs and network requests through a selectable TUI.",
		Arguments: []ArgumentSpec{
			{Name: "--port", Required: false, Description: "Chrome remote debugging port. Defaults to 9222."},
			{Name: "--chrome-path", Required: false, Description: "Chrome executable path."},
			{Name: "--user-data-dir", Required: false, Description: "Chrome profile directory used when launching Chrome."},
			{Name: "--no-launch", Required: false, Description: "Attach to an already running Chrome debug port."},
			{Name: "--target-id", Required: false, Description: "Chrome page target ID to track. Defaults to the first page target."},
			{Name: "--filter-url", Required: false, Description: "Show only requests whose URL contains this text."},
			{Name: "--method", Required: false, Description: "Show only requests with this HTTP method."},
			{Name: "--socket", Required: false, Description: "agentd Unix socket path."},
			{Name: "--role-bin", Required: false, Description: "Executable used to run zellij-agent roles."},
			{Name: "--session", Required: false, Description: "Execution session/task id for generated tab panes."},
			{Name: "--spawn-on-new-tab", Required: false, Description: "Create a daemon pane in the same Zellij tab when a new Chrome tab opens."},
			{Name: "--no-spawn-on-new-tab", Required: false, Description: "Disable daemon pane requests for new Chrome tabs."},
			{Name: "--list", Required: false, Description: "List attachable page targets and exit."},
		},
	},
	{
		Name:        RoleTabWatcher,
		Usage:       "tab-watcher [options]",
		Description: "Watches Chrome tabs and starts tab-network panes for newly opened tabs.",
		Arguments: []ArgumentSpec{
			{Name: "--port", Required: false, Description: "Chrome remote debugging port. Defaults to 9222."},
			{Name: "--socket", Required: false, Description: "agentd Unix socket path."},
			{Name: "--cwd", Required: false, Description: "Working directory for generated tab-network panes."},
			{Name: "--session", Required: false, Description: "Execution session/task id for generated tab panes."},
			{Name: "--role-bin", Required: false, Description: "Executable used to run zellij-agent roles."},
			{Name: "--chrome-path", Required: false, Description: "Chrome executable path."},
			{Name: "--user-data-dir", Required: false, Description: "Chrome profile directory used when launching Chrome."},
			{Name: "--no-launch", Required: false, Description: "Attach to an already running Chrome debug port."},
			{Name: "--poll-interval", Required: false, Description: "Chrome target polling interval."},
		},
	},
	{
		Name:        RoleCodingAgent,
		Usage:       "coding-agent <path>",
		Description: "Runs Codex coding agent in the repository containing the target path.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the repository where Codex should run."},
		},
	},
	{
		Name:        RoleDebateCoordinator,
		Usage:       "debate-coordinator <path>",
		Description: "Waits for debate synthesis input, then runs Codex to produce the coordinator summary.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the repository where Codex should run."},
		},
	},
}

func All() []RoleSpec {
	out := make([]RoleSpec, len(specs))
	for i, spec := range specs {
		out[i] = spec
		if spec.Arguments != nil {
			out[i].Arguments = make([]ArgumentSpec, len(spec.Arguments))
			copy(out[i].Arguments, spec.Arguments)
		}
	}
	return out
}

func Lookup(name string) (RoleSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return RoleSpec{}, false
}
