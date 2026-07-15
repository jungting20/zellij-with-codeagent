package debatebackground

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"zellij-with-codeagent/internal/backgrounddebate"
	"zellij-with-codeagent/internal/debaterole"
)

func renderResult(result backgrounddebate.Result, outputFormat string) ([]byte, error) {
	if outputFormat == "json" {
		return renderJSON(result)
	}
	return renderText(result), nil
}

func renderJSON(result backgrounddebate.Result) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return nil, fmt.Errorf("encode JSON result: %w", err)
	}
	return output.Bytes(), nil
}

func renderText(result backgrounddebate.Result) []byte {
	var output strings.Builder
	fmt.Fprintln(&output, "# Background Debate")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Status")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, result.Status)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Topic")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, result.Topic)
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "## Rounds (%d/%d completed)\n", result.RoundsCompleted, result.RoundsRequested)
	for _, round := range result.Rounds {
		fmt.Fprintln(&output)
		fmt.Fprintf(&output, "### Round %d\n", round.Round)
		renderRole(&output, "Proposer", round.Proposer)
		renderRole(&output, "Critic", round.Critic)
		renderRole(&output, "Judge", round.Judge)
	}
	if result.Failure != nil {
		fmt.Fprintln(&output)
		fmt.Fprintln(&output, "## Failure")
		fmt.Fprintln(&output)
		fmt.Fprintf(&output, "- Kind: `%s`\n", result.Failure.Kind)
		if result.Failure.Round != 0 {
			fmt.Fprintf(&output, "- Round: %d\n", result.Failure.Round)
		}
		if result.Failure.Role != "" {
			fmt.Fprintf(&output, "- Role: `%s`\n", result.Failure.Role)
		}
		if result.Failure.ExitCode != nil {
			fmt.Fprintf(&output, "- Exit code: %d\n", *result.Failure.ExitCode)
		}
		fmt.Fprintf(&output, "- Message: %s\n", result.Failure.Message)
	}
	if strings.TrimSpace(result.FinalContent) != "" {
		fmt.Fprintln(&output)
		fmt.Fprintln(&output, "## Final Recommendation")
		fmt.Fprintln(&output)
		fmt.Fprintln(&output, result.FinalContent)
	}
	return []byte(output.String())
}

func renderRole(output *strings.Builder, heading string, result *debaterole.Result) {
	if result == nil {
		return
	}
	fmt.Fprintln(output)
	fmt.Fprintf(output, "#### %s\n", heading)
	fmt.Fprintln(output)
	fmt.Fprintln(output, result.Content)
}
