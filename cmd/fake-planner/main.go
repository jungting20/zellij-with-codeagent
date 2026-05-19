package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	fakeplanner "zellij-with-codeagent/internal/planner/fake"
	"zellij-with-codeagent/internal/transport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fake-planner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socketPath := flags.String("socket", "", "agentd Unix socket path")
	taskID := flags.String("task-id", fakeplanner.DefaultTaskID, "logical task id")
	agentID := flags.String("agent-id", fakeplanner.DefaultAgentID, "logical agent id")
	tabName := flags.String("tab-name", fakeplanner.DefaultTabName, "Zellij tab name")
	cwd := flags.String("cwd", ".", "pane working directory")
	leaveOpen := flags.Bool("leave-open", false, "leave panes open for manual observation")
	idleTimeout := flags.Duration("idle-timeout", fakeplanner.DefaultIdleTimeout, "idle timeout while waiting for events")
	totalTimeout := flags.Duration("total-timeout", fakeplanner.DefaultTotalTimeout, "total planner run timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *socketPath == "" {
		fmt.Fprintln(stderr, "fake-planner requires --socket <path>")
		flags.Usage()
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *totalTimeout+time.Second)
	defer cancel()

	client := transport.NewClient(transport.ClientOptions{SocketPath: *socketPath})
	planner := fakeplanner.New(client, fakeplanner.Options{
		TaskID:       *taskID,
		AgentID:      *agentID,
		TabName:      *tabName,
		CWD:          *cwd,
		LeaveOpen:    *leaveOpen,
		IdleTimeout:  *idleTimeout,
		TotalTimeout: *totalTimeout,
	})
	result, err := planner.Run(ctx)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(result); encodeErr != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", encodeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "fake planner failed: %v\n", err)
		return 1
	}
	return 0
}
