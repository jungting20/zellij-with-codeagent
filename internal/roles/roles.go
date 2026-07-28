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
	RoleTicketManager     = "ticket-manager"
	RoleDebateCoordinator = "debate-coordinator"
	RoleDebateProposer    = "debate-proposer"
	RoleDebateCritic      = "debate-critic"
	RoleDebateJudge       = "debate-judge"
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
		Usage:       "coding-agent [--agent kind] [--yolo] <path> [-- agent-args...]",
		Description: "Runs a selected coding agent in the repository containing the target path.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the repository where the coding agent should run."},
			{Name: "--agent", Required: false, Description: "Coding agent kind: codex, claude, gemini, or cursor. Defaults to codex."},
			{Name: "--yolo", Required: false, Description: "Bypass coding agent permissions and sandboxing."},
			{Name: "agent-args", Required: false, Description: "Arguments passed to the selected coding agent after --."},
		},
	},
	{
		Name:        RoleTicketManager,
		Usage:       "ticket-manager [options] <path>",
		Description: "Runs a bounded pool of coding agents from the project ticket queue.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the initialized ticket project."},
			{Name: "--task", Required: true, Description: "Logical runtime task ID shared by the manager and coding panes."},
			{Name: "--anchor-pane", Required: true, Description: "Logical manager pane ID used as the same-tab anchor."},
			{Name: "--socket", Required: false, Description: "agentd Unix socket path."},
			{Name: "--zellij-session", Required: false, Description: "Target physical Zellij session name."},
			{Name: "--role-bin", Required: false, Description: "Executable used to launch coding-agent roles."},
			{Name: "--startup-timeout", Required: false, Description: "Anchor and coding-agent readiness timeout."},
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
	{
		Name:        RoleDebateProposer,
		Usage:       "debate-proposer [options] <path> [prompt...]",
		Description: "Proposes and explores debate solutions with agy.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the repository to analyze."},
			{Name: "prompt", Required: false, Description: "Debate input; reads stdin when omitted."},
			{Name: "--output-format", Required: false, Description: "Output format: text or json. Defaults to text."},
		},
	},
	{
		Name:        RoleDebateCritic,
		Usage:       "debate-critic [options] <path> [prompt...]",
		Description: "Red-teams debate proposals with Cursor Agent.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the repository to analyze."},
			{Name: "prompt", Required: false, Description: "Debate input; reads stdin when omitted."},
			{Name: "--output-format", Required: false, Description: "Output format: text or json. Defaults to text."},
		},
	},
	{
		Name:        RoleDebateJudge,
		Usage:       "debate-judge [options] <path> [prompt...]",
		Description: "Judges debate arguments and finalizes a recommendation with Codex.",
		Arguments: []ArgumentSpec{
			{Name: "path", Required: true, Description: "File or directory path inside the repository to analyze."},
			{Name: "prompt", Required: false, Description: "Debate input; reads stdin when omitted."},
			{Name: "--output-format", Required: false, Description: "Output format: text or json. Defaults to text."},
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
