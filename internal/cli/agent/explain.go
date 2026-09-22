package agentcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/transport"
)

type agentExplanationClient interface {
	ExplainAgent(context.Context, string) (transport.ExplainAgentResponse, error)
}

func runExplain(args []string, stdout, stderr io.Writer, newClient ClientFactory) int {
	if len(args) == 1 && isHelp(args[0]) {
		printExplainUsage(stdout)
		return 0
	}
	fs := flag.NewFlagSet("agent explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", defaultTimeout, "request timeout")
	jsonOutput := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		fmt.Fprintln(stderr, "agent explain requires one agent ID")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "agent explain --timeout must be positive")
		return 2
	}
	if newClient == nil {
		fmt.Fprintln(stderr, "agent explain client is not configured")
		return 1
	}
	client := newClient(*socket, *timeout)
	if isNilAgentClient(client) {
		fmt.Fprintln(stderr, "agent explain client is not configured")
		return 1
	}
	explainer, ok := client.(agentExplanationClient)
	if !ok {
		fmt.Fprintln(stderr, "agent explain is unavailable from this client")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	response, err := explainer.ExplainAgent(ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "agent explain failed via socket %s: %v\n", *socket, err)
		return 1
	}
	if *jsonOutput {
		err = json.NewEncoder(stdout).Encode(response)
	} else {
		err = printAgentExplanation(stdout, response)
	}
	if err != nil {
		fmt.Fprintf(stderr, "write agent explanation: %v\n", err)
		return 1
	}
	return 0
}

func printAgentExplanation(w io.Writer, explanation transport.ExplainAgentResponse) error {
	var output strings.Builder
	fmt.Fprintf(&output, "agent=%q kind=%q pane=%q\n", explanation.AgentID, explanation.Kind, explanation.PaneID)
	fmt.Fprintf(&output, "state=%q reason=%q matched_rule=%q\n", explanation.State, explanation.StateReason, explanation.MatchedRule)
	fmt.Fprintf(&output, "state_changed_at=%s observed_at=%s observation_available=%t\n",
		explanationTime(explanation.StateChangedAt), explanationTime(explanation.ObservedAt), explanation.ObservationAvailable)
	fmt.Fprintf(&output, "title_available=%t title_source=%q title_used_for_detection=%t title_observed_at=%s title=%q progress_available=%t\n",
		explanation.TitleAvailable, explanation.TitleSource, explanation.TitleUsedForDetection, explanationTime(explanation.TitleObservedAt), explanation.Title, explanation.ProgressAvailable)
	fmt.Fprintf(&output, "startup_grace=%t pending_idle=%t idle_confirmations=%d\n",
		explanation.StartupGrace, explanation.PendingIdle, explanation.IdleConfirmations)
	if explanation.Detection != nil {
		detection := explanation.Detection.Detection
		fmt.Fprintf(&output, "detection: state=%q rule=%q reason=%q fallback=%t skip_state_update=%t\n",
			detection.State, detection.RuleID, detection.Reason, detection.Fallback, detection.SkipStateUpdate)
		fmt.Fprintf(&output, "visible_idle=%t visible_working=%t visible_blocker=%t\n",
			detection.VisibleIdle, detection.VisibleWorking, detection.VisibleBlocker)
		for _, rule := range explanation.Detection.Rules {
			fmt.Fprintf(&output, "rule=%q priority=%d region=%q lines=%d matched=%t truncated=%t",
				rule.ID, rule.Priority, rule.Region.Type, rule.Region.Lines, rule.Matched, rule.Truncated)
			if rule.Evidence != "" {
				fmt.Fprintf(&output, " evidence=%q", rule.Evidence)
			}
			fmt.Fprintln(&output)
		}
	}
	_, err := io.WriteString(w, output.String())
	return err
}

func explanationTime(value time.Time) string {
	if value.IsZero() {
		return "unavailable"
	}
	return value.Format(time.RFC3339Nano)
}

func printExplainUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: zellij-agent agent explain [--json --socket PATH --timeout DURATION] <agent-id>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Show the last observed detection evidence without changing the agent state.")
	fmt.Fprintln(w, "  --json  Write the explanation as JSON")
	fmt.Fprintf(w, "  --socket PATH  agentd Unix socket path (default %q)\n", cli.DefaultSocketPath)
	fmt.Fprintln(w, "  --timeout DURATION  request timeout (default 10s; must be positive)")
}
