package ticketworkercli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/planner"
	"zellij-with-codeagent/internal/ticketworker"
	"zellij-with-codeagent/internal/transport"
)

type AgentClient interface {
	SubmitExecutionPlan(context.Context, string, transport.ExecutionPlanPayload) (transport.ExecutionPlanResponse, error)
}

type ClientFactory func(string, time.Duration) AgentClient

type Dependencies struct {
	StartDirectory string
	Now            func() time.Time
	Executable     []string
	NewClient      ClientFactory
}

const (
	ExitOK                = 0
	ExitDatabase          = 1
	ExitUsage             = 2
	ExitNotFound          = 3
	ExitInvalidTransition = 4
	ExitDuplicate         = 5
	ExitEmptyQueue        = 6
	ExitValidation        = 7
)

type errorOutput struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies Dependencies) int {
	if len(args) == 1 && isHelp(args[0]) {
		printUsage(stdout)
		return ExitOK
	}
	if len(args) == 0 {
		return reportUsage(stderr, false, "missing command")
	}
	if args[0] == "start" {
		return runStart(ctx, args[1:], stdout, stderr, dependencies)
	}
	root, err := ticketworker.FindRoot(dependencies.StartDirectory)
	if err != nil {
		return reportError(stderr, hasJSON(args), err)
	}
	if args[0] == "init" {
		if len(args) != 1 {
			return reportUsage(stderr, false, "init does not accept arguments")
		}
		if err := ticketworker.InitializeProject(ctx, root, dependencies.Now); err != nil {
			return reportError(stderr, false, err)
		}
		fmt.Fprintf(stdout, "initialized ticket-worker database: %s\n", ticketworker.DatabasePath(root))
		fmt.Fprintf(stdout, "initialized ticket-worker config: %s\n", ticketworker.ConfigPath(root))
		return ExitOK
	}

	store, err := ticketworker.OpenExisting(ctx, root, dependencies.Now)
	if err != nil {
		return reportError(stderr, hasJSON(args), err)
	}
	defer store.Close()

	switch args[0] {
	case "add":
		return runAdd(ctx, store, args[1:], stdout, stderr)
	case "fast-add":
		return runFastAdd(ctx, store, args[1:], stdout, stderr)
	case "list":
		return runList(ctx, store, args[1:], stdout, stderr)
	case "next":
		return runNext(ctx, store, args[1:], stdout, stderr)
	case "show":
		return runShow(ctx, store, args[1:], stdout, stderr)
	case "done":
		return runTransition(ctx, store, ticketworker.ActionDone, args[1:], stdout, stderr)
	case "cancel":
		return runTransition(ctx, store, ticketworker.ActionCancel, args[1:], stdout, stderr)
	case "reopen":
		return runTransition(ctx, store, ticketworker.ActionReopen, args[1:], stdout, stderr)
	default:
		return reportUsage(stderr, hasJSON(args), "unknown command: "+args[0])
	}
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent ticket-worker <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init    Initialize the project-local SQLite ticket queue")
	fmt.Fprintln(w, "  add     Register an approved spec and implementation plan")
	fmt.Fprintln(w, "  fast-add")
	fmt.Fprintln(w, "          Register a ticket without spec and plan artifacts")
	fmt.Fprintln(w, "  list    List tickets, optionally filtered by status")
	fmt.Fprintln(w, "  next    Atomically claim the oldest ready ticket")
	fmt.Fprintln(w, "  show    Show one ticket by ID")
	fmt.Fprintln(w, "  start   Start the ticket manager pane")
	fmt.Fprintln(w, "  done    Move an in_progress ticket to done")
	fmt.Fprintln(w, "  cancel  Cancel a ready or in_progress ticket")
	fmt.Fprintln(w, "  reopen  Move a done or cancelled ticket to ready")
}

func runStart(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies Dependencies) int {
	flags := newFlagSet("start")
	socketPath := flags.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := flags.Duration("timeout", 15*time.Second, "request timeout")
	zellijSessionFlag := flags.String("zellij-session", "", "physical Zellij session")
	if err := flags.Parse(args); err != nil {
		return reportUsage(stderr, false, err.Error())
	}
	if flags.NArg() != 0 {
		return reportUsage(stderr, false, "start does not accept positional arguments")
	}
	if *timeout <= 0 {
		return reportUsage(stderr, false, "start --timeout must be positive")
	}
	zellijSession, err := cli.ResolveZellijSession(*zellijSessionFlag)
	if err != nil {
		return reportError(stderr, false, err)
	}
	root, err := ticketworker.FindRoot(dependencies.StartDirectory)
	if err != nil {
		return reportError(stderr, false, err)
	}
	store, err := ticketworker.OpenExisting(ctx, root, dependencies.Now)
	if err != nil {
		return reportError(stderr, false, err)
	}
	if err := store.Close(); err != nil {
		return reportError(stderr, false, fmt.Errorf("close ticket database: %w", err))
	}
	if _, err := ticketworker.LoadConfig(root); err != nil {
		return reportError(stderr, false, fmt.Errorf("load ticket-worker config: %w", err))
	}
	payload, err := ticketworker.BuildStartPlan(ticketworker.StartPlanRequest{
		Root:          root,
		ZellijSession: zellijSession,
		SocketPath:    *socketPath,
		Executable:    dependencies.Executable,
	})
	if err != nil {
		return reportError(stderr, false, err)
	}
	requestID := ticketworker.StartRequestID(payload.Session)
	if err := validateStartEnvelope(requestID, payload); err != nil {
		return reportError(stderr, false, err)
	}
	if dependencies.NewClient == nil {
		return reportError(stderr, false, errors.New("ticket-worker start client is not configured"))
	}
	submitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	response, err := dependencies.NewClient(*socketPath, *timeout).SubmitExecutionPlan(submitCtx, requestID, payload)
	if err != nil {
		return reportError(stderr, false, fmt.Errorf("ticket-worker submit failed via socket %s: %w", *socketPath, err))
	}
	return reportExecutionPlan(stdout, stderr, response)
}

func validateStartEnvelope(requestID string, payload transport.ExecutionPlanPayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode ticket-worker start plan: %w", err)
	}
	envelope, err := json.Marshal(transport.RequestEnvelope{
		Type:      transport.RequestTypeExecutionPlan,
		RequestID: requestID,
		Payload:   raw,
	})
	if err != nil {
		return fmt.Errorf("encode ticket-worker start envelope: %w", err)
	}
	if _, err := planner.ParseExecutionPlanEnvelope(envelope); err != nil {
		return fmt.Errorf("validate ticket-worker start plan: %w", err)
	}
	return nil
}

func reportExecutionPlan(stdout, stderr io.Writer, response transport.ExecutionPlanResponse) int {
	var output bytes.Buffer
	totalPanes := 0
	for _, tab := range response.Tabs {
		totalPanes += len(tab.Panes)
	}
	fmt.Fprintf(&output, "request=%s session=%s layout=%s tabs=%d panes=%d\n", response.RequestID, response.Session, response.Layout, len(response.Tabs), totalPanes)
	for _, tab := range response.Tabs {
		fmt.Fprintf(&output, "tab=%s panes=%d\n", tab.Name, len(tab.Panes))
		for _, pane := range tab.Panes {
			fmt.Fprintf(&output, "- %s role=%s status=%s", pane.ID, pane.Role, pane.Status)
			if pane.ZellijPaneID != "" {
				fmt.Fprintf(&output, " zellij_pane_id=%s", pane.ZellijPaneID)
			}
			fmt.Fprintln(&output)
		}
	}
	if _, err := stdout.Write(output.Bytes()); err != nil {
		return reportError(stderr, false, fmt.Errorf("write output: %w", err))
	}
	return ExitOK
}

func runAdd(ctx context.Context, store *ticketworker.Store, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("add")
	title := flags.String("title", "", "ticket title")
	summary := flags.String("summary", "", "ticket summary")
	spec := flags.String("spec", "", "approved design path")
	plan := flags.String("plan", "", "approved implementation plan path")
	worktreeBranch := flags.String("worktree-branch", "", "worktree branch name")
	agent := flags.String("agent", "codex", "coding agent kind")
	prompt := flags.String("prompt", "", "coding-agent prompt")
	jsonOutput := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args); err != nil {
		return reportUsage(stderr, hasJSON(args), err.Error())
	}
	if len(flags.Args()) != 0 {
		return reportUsage(stderr, *jsonOutput, "add does not accept positional arguments")
	}
	if !visited(flags, "title") || !visited(flags, "summary") || !visited(flags, "spec") || !visited(flags, "plan") || !visited(flags, "worktree-branch") || !visited(flags, "prompt") {
		return reportUsage(stderr, *jsonOutput, "add requires --title, --summary, --spec, --plan, --worktree-branch, and --prompt")
	}
	created, err := store.Add(ctx, ticketworker.CreateInput{
		Title:          *title,
		Summary:        *summary,
		SpecPath:       *spec,
		PlanPath:       *plan,
		WorktreeBranch: *worktreeBranch,
		Agent:          *agent,
		Prompt:         *prompt,
	})
	if err != nil {
		return reportError(stderr, *jsonOutput, err)
	}
	return reportTicket(stdout, stderr, *jsonOutput, created)
}

func runFastAdd(ctx context.Context, store *ticketworker.Store, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("fast-add")
	title := flags.String("title", "", "ticket title")
	summary := flags.String("summary", "", "ticket summary")
	worktreeBranch := flags.String("worktree-branch", "", "worktree branch name")
	agent := flags.String("agent", "codex", "coding agent kind")
	prompt := flags.String("prompt", "", "coding-agent prompt")
	jsonOutput := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args); err != nil {
		return reportUsage(stderr, hasJSON(args), err.Error())
	}
	if len(flags.Args()) != 0 {
		return reportUsage(stderr, *jsonOutput, "fast-add does not accept positional arguments")
	}
	if !visited(flags, "title") || !visited(flags, "summary") || !visited(flags, "worktree-branch") || !visited(flags, "prompt") {
		return reportUsage(stderr, *jsonOutput, "fast-add requires --title, --summary, --worktree-branch, and --prompt")
	}
	created, err := store.FastAdd(ctx, ticketworker.CreateInput{
		Title:          *title,
		Summary:        *summary,
		WorktreeBranch: *worktreeBranch,
		Agent:          *agent,
		Prompt:         *prompt,
	})
	if err != nil {
		return reportError(stderr, *jsonOutput, err)
	}
	return reportTicket(stdout, stderr, *jsonOutput, created)
}

func runList(ctx context.Context, store *ticketworker.Store, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("list")
	status := flags.String("status", "", "filter by ticket status")
	noPrompt := flags.Bool("no-prompt", false, "omit the prompt column from human-readable output")
	jsonOutput := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args); err != nil {
		return reportUsage(stderr, hasJSON(args), err.Error())
	}
	if len(flags.Args()) != 0 {
		return reportUsage(stderr, *jsonOutput, "list does not accept positional arguments")
	}
	var filter *ticketworker.Status
	if visited(flags, "status") {
		parsed := ticketworker.Status(*status)
		filter = &parsed
	}
	tickets, err := store.List(ctx, filter)
	if err != nil {
		return reportError(stderr, *jsonOutput, err)
	}
	return reportTickets(stdout, stderr, *jsonOutput, *noPrompt, tickets)
}

func runNext(ctx context.Context, store *ticketworker.Store, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("next")
	jsonOutput := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args); err != nil {
		return reportUsage(stderr, hasJSON(args), err.Error())
	}
	if len(flags.Args()) != 0 {
		return reportUsage(stderr, *jsonOutput, "next does not accept positional arguments")
	}
	next, err := store.Next(ctx)
	if err != nil {
		return reportError(stderr, *jsonOutput, err)
	}
	return reportTicket(stdout, stderr, *jsonOutput, next)
}

func runShow(ctx context.Context, store *ticketworker.Store, args []string, stdout, stderr io.Writer) int {
	id, jsonOutput, err := parseIDArgs("show", args)
	if err != nil {
		return reportUsage(stderr, hasJSON(args), err.Error())
	}
	found, err := store.Get(ctx, id)
	if err != nil {
		return reportError(stderr, jsonOutput, err)
	}
	return reportTicket(stdout, stderr, jsonOutput, found)
}

func runTransition(ctx context.Context, store *ticketworker.Store, action ticketworker.Action, args []string, stdout, stderr io.Writer) int {
	id, jsonOutput, err := parseIDArgs(string(action), args)
	if err != nil {
		return reportUsage(stderr, hasJSON(args), err.Error())
	}
	updated, err := store.Transition(ctx, id, action)
	if err != nil {
		return reportError(stderr, jsonOutput, err)
	}
	return reportTicket(stdout, stderr, jsonOutput, updated)
}

func parseIDArgs(command string, args []string) (int64, bool, error) {
	flags := newFlagSet(command)
	jsonOutput := flags.Bool("json", false, "write JSON")
	flagArgs, positional := splitFlags(args)
	if err := flags.Parse(flagArgs); err != nil {
		return 0, false, err
	}
	if len(positional) != 1 {
		return 0, *jsonOutput, fmt.Errorf("%s requires one positive integer ID", command)
	}
	id, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, *jsonOutput, fmt.Errorf("%s requires one positive base-10 integer ID", command)
	}
	return id, *jsonOutput, nil
}

func splitFlags(args []string) ([]string, []string) {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, 1)
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			positionals = append(positionals, arg)
		}
	}
	return flags, positionals
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func visited(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == name {
			found = true
		}
	})
	return found
}

func reportTicket(stdout, stderr io.Writer, jsonOutput bool, value ticketworker.Ticket) int {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(value); err != nil {
			return reportError(stderr, true, fmt.Errorf("write output: %w", err))
		}
		return ExitOK
	}
	if _, err := fmt.Fprintf(stdout, "ID: %d\nStatus: %s\nTitle: %s\nSummary: %s\nSpec: %s\nPlan: %s\nWorktree branch: %s\nAgent: %s\nPrompt:\n%s\n", value.ID, value.Status, value.Title, value.Summary, value.SpecPath, value.PlanPath, value.WorktreeBranch, value.Agent, value.Prompt); err != nil {
		return reportError(stderr, false, fmt.Errorf("write output: %w", err))
	}
	return ExitOK
}

func escapeListField(value string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	).Replace(value)
}

func reportTickets(stdout, stderr io.Writer, jsonOutput, noPrompt bool, values []ticketworker.Ticket) int {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(values); err != nil {
			return reportError(stderr, true, fmt.Errorf("write output: %w", err))
		}
		return ExitOK
	}
	if len(values) == 0 {
		if _, err := fmt.Fprintln(stdout, "No tickets."); err != nil {
			return reportError(stderr, false, fmt.Errorf("write output: %w", err))
		}
		return ExitOK
	}
	for _, value := range values {
		if noPrompt {
			if _, err := fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\t%s\n", value.ID, value.Status, value.Agent, value.Title, value.WorktreeBranch, value.PlanPath); err != nil {
				return reportError(stderr, false, fmt.Errorf("write output: %w", err))
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", value.ID, value.Status, value.Agent, value.Title, value.WorktreeBranch, value.PlanPath, escapeListField(value.Prompt)); err != nil {
			return reportError(stderr, false, fmt.Errorf("write output: %w", err))
		}
	}
	return ExitOK
}

func reportUsage(stderr io.Writer, jsonOutput bool, message string) int {
	if jsonOutput {
		_ = json.NewEncoder(stderr).Encode(errorOutput{Error: message, Code: "usage"})
	} else {
		_, _ = fmt.Fprintln(stderr, message)
	}
	return ExitUsage
}

func reportError(stderr io.Writer, jsonOutput bool, err error) int {
	exitCode, errorCode := classifyError(err)
	if jsonOutput {
		_ = json.NewEncoder(stderr).Encode(errorOutput{Error: err.Error(), Code: errorCode})
	} else {
		_, _ = fmt.Fprintln(stderr, err)
	}
	return exitCode
}

func classifyError(err error) (int, string) {
	switch {
	case errors.Is(err, ticketworker.ErrInvalidStatus):
		return ExitUsage, "usage"
	case errors.Is(err, ticketworker.ErrNotFound):
		return ExitNotFound, "not_found"
	case errors.Is(err, ticketworker.ErrInvalidTransition):
		return ExitInvalidTransition, "invalid_transition"
	case errors.Is(err, ticketworker.ErrDuplicatePlan):
		return ExitDuplicate, "duplicate"
	case errors.Is(err, ticketworker.ErrEmptyQueue):
		return ExitEmptyQueue, "empty_queue"
	case errors.Is(err, ticketworker.ErrInvalidArtifact),
		errors.Is(err, ticketworker.ErrInvalidPrompt),
		errors.Is(err, ticketworker.ErrInvalidAgent),
		errors.Is(err, ticketworker.ErrInvalidWorktreeBranch),
		errors.Is(err, ticketworker.ErrRepositoryNotFound),
		errors.Is(err, ticketworker.ErrNotInitialized):
		return ExitValidation, "validation"
	default:
		return ExitDatabase, "database"
	}
}

func hasJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "-json" || strings.HasPrefix(arg, "--json=") {
			return true
		}
	}
	return false
}
